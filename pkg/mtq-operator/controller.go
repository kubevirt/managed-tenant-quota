package mtq_operator

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	k6tv1 "kubevirt.io/api/core/v1"
	mtqcerts "kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/cert"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/utils"
	"kubevirt.io/managed-tenant-quota/staging/src/kubevirt.io/managed-tenant-quota-api/pkg/apis/core/v1alpha1"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	"github.com/kelseyhightower/envconfig"
	"kubevirt.io/controller-lifecycle-operator-sdk/pkg/sdk/callbacks"
	sdkr "kubevirt.io/controller-lifecycle-operator-sdk/pkg/sdk/reconciler"
	mtqcluster "kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/cluster"
	mtqnamespaced "kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/namespaced"

	"kubevirt.io/managed-tenant-quota/pkg/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	finalizerName = "operator.mtq.kubevirt.io"

	createVersionLabel = "operator.mtq.kubevirt.io/createVersion"
	updateVersionLabel = "operator.mtq.kubevirt.io/updateVersion"
	// LastAppliedConfigAnnotation is the annotation that holds the last resource state which we put on resources under our governance
	LastAppliedConfigAnnotation = "operator.mtq.kubevirt.io/lastAppliedConfiguration"

	certPollInterval = 1 * time.Minute
)

var (
	log = logf.Log.WithName("mtq-operator")
)

// Add creates a new MTQ Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	r, err := newReconciler(mgr)
	if err != nil {
		return err
	}
	return r.add(mgr)
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) (*ReconcileMTQ, error) {
	var namespacedArgs mtqnamespaced.FactoryArgs
	namespace := util.GetNamespace()
	restClient := mgr.GetClient()

	clusterArgs := &mtqcluster.FactoryArgs{
		Namespace: namespace,
		Client:    restClient,
		Logger:    log,
	}

	err := envconfig.Process("", &namespacedArgs)

	if err != nil {
		return nil, err
	}

	namespacedArgs.Namespace = namespace
	namespacedArgs.KVNamespace = getKVNS()

	log.Info("", "VARS", fmt.Sprintf("%+v", namespacedArgs))

	scheme := mgr.GetScheme()
	uncachedClient, err := client.New(mgr.GetConfig(), client.Options{
		Scheme: scheme,
		Mapper: mgr.GetRESTMapper(),
	})
	if err != nil {
		return nil, err
	}

	recorder := mgr.GetEventRecorderFor("operator-controller")

	r := &ReconcileMTQ{
		client:         restClient,
		uncachedClient: uncachedClient,
		scheme:         scheme,
		recorder:       recorder,
		namespace:      namespace,
		clusterArgs:    clusterArgs,
		namespacedArgs: &namespacedArgs,
	}
	callbackDispatcher := callbacks.NewCallbackDispatcher(log, restClient, uncachedClient, scheme, namespace)
	r.reconciler = sdkr.NewReconciler(r, log, restClient, callbackDispatcher, scheme, createVersionLabel, updateVersionLabel, LastAppliedConfigAnnotation, certPollInterval, finalizerName, false, recorder)

	r.registerHooks()
	return r, nil
}

var _ reconcile.Reconciler = &ReconcileMTQ{}

// ReconcileMTQ reconciles a MTQ object
type ReconcileMTQ struct {
	// This Client, initialized using mgr.client() above, is a split Client
	// that reads objects from the cache and writes to the apiserver
	client client.Client

	// use this for getting any resources not in the install namespace or cluster scope
	uncachedClient client.Client
	scheme         *runtime.Scheme
	recorder       record.EventRecorder
	controller     controller.Controller

	namespace      string
	clusterArgs    *mtqcluster.FactoryArgs
	namespacedArgs *mtqnamespaced.FactoryArgs

	certManager CertManager
	reconciler  *sdkr.Reconciler
}

// SetController sets the controller dependency
func (r *ReconcileMTQ) SetController(controller controller.Controller) {
	r.controller = controller
	r.reconciler.WithController(controller)
}

// Reconcile reads that state of the cluster for a MTQ object and makes changes based on the state read
// and what is in the MTQ.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileMTQ) Reconcile(_ context.Context, request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling MTQ CR")
	operatorVersion := r.namespacedArgs.OperatorVersion
	cr := &v1alpha1.MTQ{}
	crKey := client.ObjectKey{Namespace: "", Name: request.NamespacedName.Name}
	err := r.client.Get(context.TODO(), crKey, cr)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Info("MTQ CR does not exist")
			return reconcile.Result{}, nil
		}
		reqLogger.Error(err, "Failed to get MTQ object")
		return reconcile.Result{}, err
	}

	return r.reconciler.Reconcile(request, operatorVersion, reqLogger)
}

func (r *ReconcileMTQ) add(mgr manager.Manager) error {
	// Create a new controller
	c, err := controller.New("mtq-operator-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	r.SetController(c)

	if err = r.reconciler.WatchCR(); err != nil {
		return err
	}

	cm, err := NewCertManager(mgr, r.namespace)
	if err != nil {
		return err
	}

	r.certManager = cm

	return nil
}

// createOperatorConfig creates operator config map
func (r *ReconcileMTQ) createOperatorConfig(cr client.Object) error {
	mtqCR := cr.(*v1alpha1.MTQ)
	installerLabels := utils.GetRecommendedInstallerLabelsFromCr(mtqCR)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils.ConfigMapName,
			Namespace: r.namespace,
			Labels:    map[string]string{"operator.mtq.kubevirt.io": ""},
		},
	}
	utils.SetRecommendedLabels(cm, installerLabels, utils.ControllerPodName)

	if err := controllerutil.SetControllerReference(cr, cm, r.scheme); err != nil {
		return err
	}

	return r.client.Create(context.TODO(), cm)
}

func (r *ReconcileMTQ) getConfigMap() (*corev1.ConfigMap, error) {
	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Name: utils.ConfigMapName, Namespace: r.namespace}

	if err := r.client.Get(context.TODO(), key, cm); err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	return cm, nil
}

func (r *ReconcileMTQ) getCertificateDefinitions(mtq *v1alpha1.MTQ) []mtqcerts.CertificateDefinition {
	args := &mtqcerts.FactoryArgs{Namespace: r.namespace}

	if mtq != nil && mtq.Spec.CertConfig != nil {
		if mtq.Spec.CertConfig.CA != nil {
			if mtq.Spec.CertConfig.CA.Duration != nil {
				args.SignerDuration = &mtq.Spec.CertConfig.CA.Duration.Duration
			}

			if mtq.Spec.CertConfig.CA.RenewBefore != nil {
				args.SignerRenewBefore = &mtq.Spec.CertConfig.CA.RenewBefore.Duration
			}
		}

		if mtq.Spec.CertConfig.Server != nil {
			if mtq.Spec.CertConfig.Server.Duration != nil {
				args.TargetDuration = &mtq.Spec.CertConfig.Server.Duration.Duration
			}

			if mtq.Spec.CertConfig.Server.RenewBefore != nil {
				args.TargetRenewBefore = &mtq.Spec.CertConfig.Server.RenewBefore.Duration
			}
		}
	}

	return mtqcerts.CreateCertificateDefinitions(args)
}

func getKVNS() string {
	virtCli, err := util.GetVirtCli()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := ctx.Done()
	kubevirtInformer := util.KubeVirtInformer(virtCli)
	go kubevirtInformer.Run(stop)
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}
	if !cache.WaitForCacheSync(stop, kubevirtInformer.HasSynced) {
		log.Error(err, "couldn't fetch kv install namespace")
		os.Exit(1)
	}
	objs := kubevirtInformer.GetIndexer().List()
	if len(objs) != 1 {
		log.Error(err, "Single KV object should exist in the cluster.")
		os.Exit(1)
	}
	kv := (objs[0]).(*k6tv1.KubeVirt)

	return kv.Namespace
}
