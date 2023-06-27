package mtq_controller

import (
	"context"
	"fmt"
	"io/ioutil"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sadmission "k8s.io/apiserver/pkg/admission"
	quotaplugin "k8s.io/apiserver/pkg/admission/plugin/resourcequota"
	"k8s.io/apiserver/pkg/admission/plugin/resourcequota/apis/resourcequota"
	v12 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/kubernetes/scheme"
	v14 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubernetes/pkg/quota/v1/evaluator/core"
	"k8s.io/utils/clock"
	v1alpha1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
	"kubevirt.io/client-go/log"
	corev1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"kubevirt.io/containerized-data-importer/pkg/util/cert/fetcher"
	virtconfig "kubevirt.io/kubevirt/pkg/virt-config"
	"kubevirt.io/kubevirt/pkg/virt-controller/services"
	v1alpha13 "kubevirt.io/managed-tenant-quota/pkg/generated/clientset/versioned/typed/core/v1alpha1"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/utils"
	"kubevirt.io/managed-tenant-quota/pkg/util"
	webhooklock "kubevirt.io/managed-tenant-quota/pkg/validating-webhook-lock"
	v1alpha12 "kubevirt.io/managed-tenant-quota/staging/src/kubevirt.io/managed-tenant-quota-api/pkg/apis/core/v1alpha1"
	"reflect"
	"strings"
	"sync"
	"time"
)

const (
	FailedToReleaseMigrationReason string = "NotEnoughResourceToRelease"
)

type enqueueState string

const (
	Immediate enqueueState = "Immediate"
	Forget    enqueueState = "Forget"
	BackOff   enqueueState = "BackOff"
)

type ManagedQuotaController struct {
	mtqNs                 string
	stop                  <-chan struct{}
	nsCache               *NamespaceCache
	internalLock          *sync.Mutex
	nsLockMap             *namespaceLockMap
	podInformer           cache.SharedIndexInformer
	migrationInformer     cache.SharedIndexInformer
	resourceQuotaInformer cache.SharedIndexInformer
	vmmrqInformer         cache.SharedIndexInformer
	vmiInformer           cache.SharedIndexInformer
	migrationQueue        workqueue.RateLimitingInterface
	virtCli               kubecli.KubevirtClient
	mtqCli                v1alpha13.MtqV1alpha1Client
	recorder              record.EventRecorder
	podEvaluator          v12.Evaluator
	serverBundleFetcher   *fetcher.ConfigMapCertBundleFetcher
	templateSvc           services.TemplateService
}

func NewManagedQuotaController(virtCli kubecli.KubevirtClient,
	stop <-chan struct{},
	mtqNs string, mtqCli v1alpha13.MtqV1alpha1Client,
	podInformer cache.SharedIndexInformer,
	resourceQuotaInformer cache.SharedIndexInformer,
	vmmrqInformer cache.SharedIndexInformer,
	serverBundleFetcher *fetcher.ConfigMapCertBundleFetcher,
) *ManagedQuotaController {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&v14.EventSinkImpl{Interface: virtCli.CoreV1().Events(v1.NamespaceAll)})

	ctrl := ManagedQuotaController{
		mtqNs:                 mtqNs,
		virtCli:               virtCli,
		stop:                  stop,
		mtqCli:                mtqCli,
		migrationInformer:     nil,
		podInformer:           podInformer,
		resourceQuotaInformer: resourceQuotaInformer,
		vmmrqInformer:         vmmrqInformer,
		vmiInformer:           nil,
		serverBundleFetcher:   serverBundleFetcher,
		migrationQueue:        workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "migration-queue"),
		internalLock:          &sync.Mutex{},
		nsLockMap:             &namespaceLockMap{m: make(map[string]*sync.Mutex), mutex: &sync.Mutex{}},
		nsCache:               NewNamespaceCache(),
		recorder:              eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: utils.ControllerPodName}),
		podEvaluator:          core.NewPodEvaluator(nil, clock.RealClock{}),
		templateSvc:           nil,
	}

	ctrl.vmmrqInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    ctrl.addVmmrq,
		UpdateFunc: ctrl.updateVmmrq,
	})

	return &ctrl
}

// When a virtualMachineMigrationResourceQuota is added, figure out if there are pending migration in the namespace
// if there are we should push them into the queue to accelerate the target creation process
func (ctrl *ManagedQuotaController) addVmmrq(obj interface{}) {
	err := ctrl.setKVInformersAndTmplSrvIfNeeded()
	if err != nil {
		return
	}
	atLeastOneMigration := false
	vmmrq := obj.(*v1alpha12.VirtualMachineMigrationResourceQuota)
	log.Log.V(4).Object(vmmrq).Infof("VMMRQ " + vmmrq.Name + " added")
	objs, _ := ctrl.migrationInformer.GetIndexer().ByIndex(cache.NamespaceIndex, vmmrq.Namespace)
	for _, obj := range objs {
		migration := obj.(*v1alpha1.VirtualMachineInstanceMigration)
		if migration.Status.Conditions == nil {
			continue
		}
		for _, cond := range migration.Status.Conditions {
			if cond.Type == v1alpha1.VirtualMachineInstanceMigrationRejectedByResourceQuota {
				ctrl.enqueueMigration(migration)
				atLeastOneMigration = true
			}
		}
	}
	if !atLeastOneMigration { //should update vmmrq.status even if there aren't any blocked migrations
		ctrl.migrationQueue.Add(vmmrq.Namespace + "/fake")
	}
	return
}

// When a virtualMachineMigrationResourceQuota is updated , figure out if there are pending migration in the namespace
// if there are we should push them into the queue to accelerate the target creation process
func (ctrl *ManagedQuotaController) updateVmmrq(old, cur interface{}) {
	err := ctrl.setKVInformersAndTmplSrvIfNeeded()
	if err != nil {
		return
	}
	atLeastOneMigration := false
	curVmmrq := cur.(*v1alpha12.VirtualMachineMigrationResourceQuota)
	oldVmmrq := old.(*v1alpha12.VirtualMachineMigrationResourceQuota)
	if equality.Semantic.DeepEqual(curVmmrq.Spec, oldVmmrq.Spec) {
		// Periodic rsync will send update events for all known resourceQuotas.
		// Also, resourceAllocation change will update resourceQuotas
		return
	}
	log.Log.V(4).Object(curVmmrq).Infof("VMMRQ " + curVmmrq.Name + " updated")
	objs, _ := ctrl.migrationInformer.GetIndexer().ByIndex(cache.NamespaceIndex, curVmmrq.Namespace)
	for _, obj := range objs {
		migration := obj.(*v1alpha1.VirtualMachineInstanceMigration)
		if migration.Status.Conditions == nil {
			continue
		}
		for _, cond := range migration.Status.Conditions {
			if cond.Type == v1alpha1.VirtualMachineInstanceMigrationRejectedByResourceQuota {
				ctrl.enqueueMigration(migration)
				atLeastOneMigration = true
			}
		}
	}
	if !atLeastOneMigration { //should update vmmrq.status even if there aren't any blocked migrations
		ctrl.migrationQueue.Add(curVmmrq.Namespace + "/fake")
	}
	return
}

func (ctrl *ManagedQuotaController) deleteMigration(obj interface{}) {
	ctrl.enqueueMigration(obj)
}

func (ctrl *ManagedQuotaController) updateMigration(_, curr interface{}) {
	ctrl.enqueueMigration(curr)
}
func (ctrl *ManagedQuotaController) enqueueMigration(obj interface{}) {
	logger := log.Log
	migration := obj.(*v1alpha1.VirtualMachineInstanceMigration)
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(migration)
	if err != nil {
		logger.Object(migration).Reason(err).Error("Failed to extract key from migration.")
		return
	}
	ctrl.migrationQueue.Add(key)
}

func (ctrl *ManagedQuotaController) runWorker() {
	for ctrl.Execute() {
	}
}

func (ctrl *ManagedQuotaController) Execute() bool {
	key, quit := ctrl.migrationQueue.Get()
	if quit {
		return false
	}
	defer ctrl.migrationQueue.Done(key)

	err, enqueueState := ctrl.execute(key.(string))
	if err != nil {
		log.Log.Reason(err).Infof(fmt.Sprintf("ManagedQuotaController: Error with key: %v", key))
	}
	switch enqueueState {
	case BackOff:
		ctrl.migrationQueue.AddRateLimited(key)
	case Forget:
		ctrl.migrationQueue.Forget(key)
	case Immediate:
		ctrl.migrationQueue.Add(key)
	}

	return true
}

func (ctrl *ManagedQuotaController) execute(key string) (error, enqueueState) {
	migrationObj, migrationExists, err := ctrl.migrationInformer.GetStore().GetByKey(key)
	if err != nil {
		return err, BackOff
	}
	migartionNS, migrationName, err := parseKey(key)
	if err != nil {
		return err, BackOff
	}
	ctrl.nsLockMap.Lock(migartionNS)
	defer ctrl.nsLockMap.Unlock(migartionNS)
	vmmrqObjsList, err := ctrl.mtqCli.VirtualMachineMigrationResourceQuotas(migartionNS).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return err, BackOff
	}
	if len(vmmrqObjsList.Items) != 1 {
		return fmt.Errorf("there should be 1 virtualMachineMigrationResourceQuota in %v namespace", migartionNS), BackOff
	}

	vmmrq := &vmmrqObjsList.Items[0]
	if vmmrq.Status.MigrationsToBlockingResourceQuotas == nil {
		vmmrq.Status.MigrationsToBlockingResourceQuotas = make(map[string][]string)
	}
	if !areResourceMapsEqual(vmmrq.Spec.AdditionalMigrationResources, vmmrq.Status.AdditionalMigrationResources) {
		vmmrq.Status.AdditionalMigrationResources = vmmrq.Spec.AdditionalMigrationResources
		_, err := ctrl.mtqCli.VirtualMachineMigrationResourceQuotas(migartionNS).UpdateStatus(context.Background(), vmmrq, metav1.UpdateOptions{})
		if err != nil {
			return err, Immediate
		}
	}
	prevVmmrq := vmmrq.DeepCopy()

	var migration *v1alpha1.VirtualMachineInstanceMigration
	var vmi *v1alpha1.VirtualMachineInstance
	isBlockedMigration := false

	if migrationExists {
		migration = migrationObj.(*v1alpha1.VirtualMachineInstanceMigration)
		vmiObj, vmiExists, err := ctrl.vmiInformer.GetStore().GetByKey(fmt.Sprintf("%s/%s", migartionNS, migration.Spec.VMIName))
		if err != nil {
			return err, BackOff
		} else if !vmiExists { //vmi for migration doesn't exist
			return fmt.Errorf("VMI doesn't exist for migration"), BackOff
		}
		vmi = vmiObj.(*v1alpha1.VirtualMachineInstance)
		isBlockedMigration, err = ctrl.isBlockedMigration(vmi, migration)
		if err != nil {
			return err, BackOff
		}
	}

	finalEnqueueState := Forget
	if !isBlockedMigration {
		delete(vmmrq.Status.MigrationsToBlockingResourceQuotas, migrationName)
	} else if isBlockedMigration {
		finalEnqueueState = Immediate
		targetPod, err := ctrl.templateSvc.RenderLaunchManifest(vmi)
		if err != nil {
			return err, Immediate
		}
		currBlockingRQList, err := ctrl.getCurrBlockingRQInNS(vmmrq, migration, targetPod, vmmrq.Status.AdditionalMigrationResources)
		if err != nil {
			return err, Immediate
		} else if len(currBlockingRQList) == 0 {
			delete(vmmrq.Status.MigrationsToBlockingResourceQuotas, migrationName)
		} else {
			vmmrq.Status.MigrationsToBlockingResourceQuotas[migrationName] = currBlockingRQList
		}
	}

	allBlockingRQsInNS, err := ctrl.getAllBlockingRQsInNS(vmmrq, migartionNS)
	if err != nil {
		return err, Immediate
	}

	vmmrq.Status.OriginalBlockingResourceQuotas = allBlockingRQsInNS
	rqListToRestore := ctrl.getRQListToRestore(vmmrq, prevVmmrq)
	shouldLockNS := shouldLockNS(vmmrq)
	var nsLocked bool

	switch ctrl.nsCache.GetLockState(migartionNS) {
	case Locked:
		nsLocked = true
	case Unlocked:
		nsLocked = false
	case Unknown: //expensive api call
		nsLocked, err = webhooklock.NamespaceLocked(migartionNS, ctrl.mtqNs, ctrl.virtCli)
		if err != nil {
			return err, Immediate
		}
	}
	if shouldLockNS && !nsLocked {
		caBundle, err := ctrl.serverBundleFetcher.BundleBytes() //todo use case no need to fetch caBundle every lock just make sure it's not expired
		if err != nil {
			return err, Immediate
		}
		err = webhooklock.LockNamespace(migartionNS, ctrl.mtqNs, ctrl.virtCli, caBundle)
		if err != nil {
			return err, Immediate
		}
		ctrl.nsCache.markLockStateLocked(migartionNS)
	}
	err = ctrl.restoreOriginalRQs(rqListToRestore, migartionNS)
	if err != nil {
		return err, Immediate
	}
	if shouldUpdateVmmrq(vmmrq, prevVmmrq) {
		_, err := ctrl.mtqCli.VirtualMachineMigrationResourceQuotas(migartionNS).UpdateStatus(context.Background(), vmmrq, metav1.UpdateOptions{})
		if err != nil {
			return err, Immediate
		}
	}

	err = ctrl.addResourcesToRQs(vmmrq, migartionNS)
	if err != nil {
		return err, Immediate
	}
	if !shouldLockNS && nsLocked {
		err := webhooklock.UnlockNamespace(migartionNS, ctrl.virtCli)
		if err != nil {
			return err, Immediate
		}
		ctrl.nsCache.markLockStateUnlocked(migartionNS)
	}
	return nil, finalEnqueueState
}

func (ctrl *ManagedQuotaController) getRQListToRestore(currVmmrq *v1alpha12.VirtualMachineMigrationResourceQuota, prevVmmrq *v1alpha12.VirtualMachineMigrationResourceQuota) []v1alpha12.ResourceQuotaNameAndSpec {
	var rqToReduceList []v1alpha12.ResourceQuotaNameAndSpec
	for _, rqInPrev := range prevVmmrq.Status.OriginalBlockingResourceQuotas {
		found := false
		for _, rqInCurr := range currVmmrq.Status.OriginalBlockingResourceQuotas {
			if rqInCurr.Name == rqInPrev.Name {
				found = true
			}
		}
		if !found {
			rqToReduceList = append(rqToReduceList, rqInPrev)
		}
	}
	return rqToReduceList
}

func (ctrl *ManagedQuotaController) isBlockedMigration(vmi *v1alpha1.VirtualMachineInstance, migration *v1alpha1.VirtualMachineInstanceMigration) (bool, error) {
	if migration == nil || migration.Status.Phase != v1alpha1.MigrationPending {
		return false, nil
	}

	targetPods, err := listMatchingTargetPods(migration, vmi, ctrl.podInformer)
	if err != nil {
		return false, err
	} else if len(targetPods) > 0 {
		return false, nil
	}

	for _, cond := range migration.Status.Conditions {
		if cond.Type == v1alpha1.VirtualMachineInstanceMigrationRejectedByResourceQuota {
			return true, nil
		}
	}
	return false, nil
}
func (ctrl *ManagedQuotaController) getAllBlockingRQsInNS(vmmrq *v1alpha12.VirtualMachineMigrationResourceQuota, ns string) ([]v1alpha12.ResourceQuotaNameAndSpec, error) {
	var blockingRQInNS []v1alpha12.ResourceQuotaNameAndSpec
	blockingRQlist := flatStringToStringMapNoDups(vmmrq.Status.MigrationsToBlockingResourceQuotas)
	currRQListObj, err := ctrl.resourceQuotaInformer.GetIndexer().ByIndex(cache.NamespaceIndex, ns)
	if err != nil {
		return nil, err
	}
	for _, blockingRQ := range blockingRQlist {
		if origRQNameAndSpec := getRQNameAndSpecIfExist(vmmrq.Status.OriginalBlockingResourceQuotas, blockingRQ); origRQNameAndSpec != nil {
			blockingRQInNS = append(blockingRQInNS, *origRQNameAndSpec)
			continue
		}
		for _, obj := range currRQListObj {
			rqInNS := obj.(*v1.ResourceQuota)
			if rqInNS.Name == blockingRQ {
				rqNameAndSpec := v1alpha12.ResourceQuotaNameAndSpec{
					Name: rqInNS.Name,
					Spec: rqInNS.Spec,
				}
				blockingRQInNS = append(blockingRQInNS, rqNameAndSpec)
			}
		}
	}
	return blockingRQInNS, nil
}

func Run(threadiness int, stop <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	nsBytes, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return err
	}
	mtqNs := string(nsBytes)
	virtCli, err := util.GetVirtCli()
	if err != nil {
		return err
	}

	mtqCli := util.GetMTQCli()
	podInformer := util.GetLauncherPodInformer(virtCli)
	resourceQuotaInformer := util.GetResourceQuotaInformer(virtCli)
	virtualMachineMigrationResourceQuotaInformer := util.GetVirtualMachineMigrationResourceQuotaInformer(mtqCli)

	go podInformer.Run(stop)
	go resourceQuotaInformer.Run(stop)
	go virtualMachineMigrationResourceQuotaInformer.Run(stop)

	serverBundleFetcher := &fetcher.ConfigMapCertBundleFetcher{
		Name:   "mtq-lock-signer-bundle",
		Client: virtCli.CoreV1().ConfigMaps(mtqNs),
	}

	if !cache.WaitForCacheSync(stop,
		podInformer.HasSynced,
		resourceQuotaInformer.HasSynced,
		virtualMachineMigrationResourceQuotaInformer.HasSynced,
	) {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	ctrl := NewManagedQuotaController(virtCli,
		stop,
		mtqNs,
		mtqCli,
		podInformer,
		resourceQuotaInformer,
		virtualMachineMigrationResourceQuotaInformer,
		serverBundleFetcher,
	)
	_ = ctrl.setKVInformersAndTmplSrvIfNeeded()

	log.Log.Info("Starting Managed Quota controller")
	defer log.Log.Info("Shutting down Managed Quota controller")

	for i := 0; i < threadiness; i++ {
		go wait.Until(ctrl.runWorker, time.Second, stop)
	}

	<-stop
	return nil

}

func listMatchingTargetPods(migration *v1alpha1.VirtualMachineInstanceMigration, vmi *v1alpha1.VirtualMachineInstance, podInformer cache.SharedIndexInformer) ([]*v1.Pod, error) {
	selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{
			v1alpha1.CreatedByLabel:    string(vmi.UID),
			v1alpha1.AppLabel:          "virt-launcher",
			v1alpha1.MigrationJobLabel: string(migration.UID),
		},
	})
	if err != nil {
		return nil, err
	}

	objs, err := podInformer.GetIndexer().ByIndex(cache.NamespaceIndex, migration.Namespace)
	if err != nil {
		return nil, err
	}

	pods := []*v1.Pod{}
	for _, obj := range objs {
		pod := obj.(*v1.Pod)
		if selector.Matches(labels.Set(pod.ObjectMeta.Labels)) {
			pods = append(pods, pod)
		}
	}

	return pods, nil
}

func admitPodToQuota(resourceQuota *v1.ResourceQuota, attributes k8sadmission.Attributes, evaluator v12.Evaluator, limitedResource resourcequota.LimitedResource) ([]v1.ResourceQuota, error) {
	return quotaplugin.CheckRequest([]v1.ResourceQuota{*resourceQuota}, attributes, evaluator, []resourcequota.LimitedResource{limitedResource})
}

func shouldLockNS(vmmrq *v1alpha12.VirtualMachineMigrationResourceQuota) bool {
	return len(vmmrq.Status.OriginalBlockingResourceQuotas) > 0
}

func flatStringToStringMapNoDups(m map[string][]string) []string {
	stringFound := make(map[string]bool)
	for _, s := range m {
		for _, rq := range s {
			stringFound[rq] = true
		}
	}

	flatMapNoDups := make([]string, 0, len(stringFound))
	for rq := range stringFound {
		flatMapNoDups = append(flatMapNoDups, rq)
	}

	return flatMapNoDups
}

func (ctrl *ManagedQuotaController) restoreOriginalRQs(rqSpecAndNameListToRestore []v1alpha12.ResourceQuotaNameAndSpec, namespace string) error {
	if len(rqSpecAndNameListToRestore) == 0 {
		return nil
	}
	for _, quotaNameAndSpec := range rqSpecAndNameListToRestore {
		key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(
			&v1.ResourceQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name:      quotaNameAndSpec.Name,
					Namespace: namespace,
				},
			})
		if err != nil {
			log.Log.Infof("ManagedQuotaController: Failed to extract key from resourceQuota.")
			return err
		}

		rqToModifyObj, exists, err := ctrl.resourceQuotaInformer.GetStore().GetByKey(key)
		if err != nil {
			return err
		}

		if exists {
			rqToModify := rqToModifyObj.(*v1.ResourceQuota)
			if areResourceMapsEqual(quotaNameAndSpec.Spec.Hard, rqToModify.Spec.Hard) {
				continue //already restored
			}
			rqToModify.Spec.Hard = quotaNameAndSpec.Spec.Hard
			_, err = ctrl.virtCli.CoreV1().ResourceQuotas(rqToModify.Namespace).Update(context.Background(), rqToModify, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (ctrl *ManagedQuotaController) addResourcesToRQs(currVmmrq *v1alpha12.VirtualMachineMigrationResourceQuota, namespace string) error {
	for _, quotaNameAndSpec := range currVmmrq.Status.OriginalBlockingResourceQuotas {
		key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(
			&v1.ResourceQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name:      quotaNameAndSpec.Name,
					Namespace: namespace,
				},
			})
		if err != nil {
			log.Log.Infof("ManagedQuotaController: Failed to extract key from resourceQuota.")
			return err
		}

		rqToModifyObj, exists, err := ctrl.resourceQuotaInformer.GetStore().GetByKey(key)
		if err != nil {
			return err
		}

		if exists {
			rqToModify := rqToModifyObj.(*v1.ResourceQuota)
			if !areResourceMapsEqual(quotaNameAndSpec.Spec.Hard, rqToModify.Spec.Hard) {
				continue //already modified
			}
			rqToModify.Spec.Hard = addResourcesToRQ(*rqToModify, &currVmmrq.Status.AdditionalMigrationResources).Spec.Hard
			_, err = ctrl.virtCli.CoreV1().ResourceQuotas(rqToModify.Namespace).Update(context.Background(), rqToModify, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func shouldUpdateVmmrq(currVmmrq *v1alpha12.VirtualMachineMigrationResourceQuota, prevVmmrq *v1alpha12.VirtualMachineMigrationResourceQuota) bool {
	return !reflect.DeepEqual(currVmmrq.Status.OriginalBlockingResourceQuotas, prevVmmrq.Status.OriginalBlockingResourceQuotas) ||
		!reflect.DeepEqual(currVmmrq.Status.MigrationsToBlockingResourceQuotas, prevVmmrq.Status.MigrationsToBlockingResourceQuotas)
}

func (ctrl *ManagedQuotaController) getCurrBlockingRQInNS(vmmrq *v1alpha12.VirtualMachineMigrationResourceQuota, m *v1alpha1.VirtualMachineInstanceMigration, podToCreate *v1.Pod, resourceListToAdd v1.ResourceList) ([]string, error) {
	currRQListObj, err := ctrl.resourceQuotaInformer.GetIndexer().ByIndex(cache.NamespaceIndex, m.Namespace)
	if err != nil {
		return nil, err
	}
	currPodLimitedResource, err := getCurrLauncherLimitedResource(ctrl.podEvaluator, podToCreate)
	if err != nil {
		return nil, err
	}
	var currRQListItems []string
	podToCreateAttr := k8sadmission.NewAttributesRecord(podToCreate, nil,
		apiextensions.Kind("Pod").WithVersion("version"), podToCreate.Namespace, podToCreate.Name,
		corev1beta1.Resource("pods").WithVersion("version"), "", k8sadmission.Create,
		&metav1.CreateOptions{}, false, nil)

	for _, obj := range currRQListObj {
		resourceQuota := obj.(*v1.ResourceQuota)
		resourceQuotaCopy := resourceQuota.DeepCopy()
		if origRQNameAndSpec := getRQNameAndSpecIfExist(vmmrq.Status.OriginalBlockingResourceQuotas, resourceQuota.Name); origRQNameAndSpec != nil {
			resourceQuotaCopy.Spec = origRQNameAndSpec.Spec
			resourceQuotaCopy.Status.Hard = origRQNameAndSpec.Spec.Hard
		}

		//Checking if the resourceQuota is blocking us
		_, errWithCurrRQ := admitPodToQuota(resourceQuotaCopy, podToCreateAttr, ctrl.podEvaluator, currPodLimitedResource)
		//checking if the additional resources in vmmRQ will unblock us
		rqAfterResourcesAddition := addResourcesToRQ(*resourceQuotaCopy, &resourceListToAdd)
		_, errWithModifiedRQ := admitPodToQuota(rqAfterResourcesAddition, podToCreateAttr, ctrl.podEvaluator, currPodLimitedResource)
		if errWithCurrRQ != nil && strings.Contains(errWithCurrRQ.Error(), "exceeded quota") && errWithModifiedRQ == nil {
			currRQListItems = append(currRQListItems, (*resourceQuota).Name)
		} else if errWithModifiedRQ != nil && strings.Contains(errWithModifiedRQ.Error(), "exceeded quota") {
			evtMsg := fmt.Sprintf("Warning: Currently VMMRQ: %v in namespace: %v doesn't have enough resoures to release blocked migration: %v", vmmrq.Name, m.Namespace, m.Name)
			ctrl.recorder.Eventf(podToCreate, v1.EventTypeWarning, FailedToReleaseMigrationReason, evtMsg, errWithModifiedRQ.Error())
			return []string{}, nil
		}
	}
	return currRQListItems, nil
}

func addResourcesToRQ(rq v1.ResourceQuota, rl *v1.ResourceList) *v1.ResourceQuota {
	newRQ := &v1.ResourceQuota{
		Spec: v1.ResourceQuotaSpec{
			Hard: rq.Spec.Hard,
		},
		Status: v1.ResourceQuotaStatus{
			Hard: rq.Spec.Hard,
			Used: rq.Status.Used,
		},
	}
	for rqResourceName, currQuantity := range newRQ.Spec.Hard {
		for vmmrqResourceName, quantityToAdd := range *rl {
			if rqResourceName == vmmrqResourceName {
				currQuantity.Add(quantityToAdd)
				newRQ.Spec.Hard[rqResourceName] = currQuantity
				newRQ.Status.Hard[rqResourceName] = currQuantity
			}
		}
	}
	return newRQ
}

func parseKey(key string) (string, string, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("error: input key: %v is not in the expected format", key)
	}
	migartionNS := parts[0]
	migrationName := parts[1]
	return migartionNS, migrationName, nil
}

func getRQNameAndSpecIfExist(blockingRQsList []v1alpha12.ResourceQuotaNameAndSpec, name string) *v1alpha12.ResourceQuotaNameAndSpec {
	for _, rq := range blockingRQsList {
		if rq.Name == name {
			// addResourcesToRQ func modify resourceList within vmmrq.Status.OriginalBlockingResourceQuotas so we must use DeepCopy
			return rq.DeepCopy()
		}
	}
	return nil
}

func areResourceMapsEqual(map1, map2 map[v1.ResourceName]resource.Quantity) bool {
	if len(map1) != len(map2) {
		return false
	}

	for key, value1 := range map1 {
		value2, exists := map2[key]
		if !exists {
			return false
		}

		if !value1.Equal(value2) {
			return false
		}
	}

	return true
}

func getCurrLauncherLimitedResource(podEvaluator v12.Evaluator, podToCreate *v1.Pod) (resourcequota.LimitedResource, error) {
	launcherLimitedResource := resourcequota.LimitedResource{
		Resource:      "pods",
		MatchContains: []string{},
	}
	usage, err := podEvaluator.Usage(podToCreate)
	if err != nil {
		return launcherLimitedResource, err
	}
	for k, _ := range usage {
		launcherLimitedResource.MatchContains = append(launcherLimitedResource.MatchContains, string(k))
	}
	return launcherLimitedResource, nil
}

func (ctrl *ManagedQuotaController) setKVInformersAndTmplSrvIfNeeded() error {
	if ctrl.shouldSetUpKVInformersAndTmplSrv() && kvInstalled(ctrl.virtCli) {
		err := ctrl.setKVInformersAndTmplSrv()
		if err != nil {
			return err
		}
	}
	return nil
}

func (ctrl *ManagedQuotaController) setKVInformersAndTmplSrv() error {
	ctrl.vmiInformer = util.GetVMIInformer(ctrl.virtCli)
	ctrl.migrationInformer = util.GetMigrationInformer(ctrl.virtCli)
	kubeVirtInformer := util.KubeVirtInformer(ctrl.virtCli)
	crdInformer := util.CRDInformer(ctrl.virtCli)
	pvcInformer := util.PersistentVolumeClaim(ctrl.virtCli)

	go ctrl.migrationInformer.Run(ctrl.stop)
	go ctrl.vmiInformer.Run(ctrl.stop)
	go kubeVirtInformer.Run(ctrl.stop)
	go crdInformer.Run(ctrl.stop)
	go pvcInformer.Run(ctrl.stop)

	if !cache.WaitForCacheSync(ctrl.stop,
		ctrl.migrationInformer.HasSynced,
		ctrl.vmiInformer.HasSynced,
		crdInformer.HasSynced,
		kubeVirtInformer.HasSynced,
	) {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	ctrl.migrationInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: ctrl.deleteMigration,
		UpdateFunc: ctrl.updateMigration,
	})

	objs := kubeVirtInformer.GetIndexer().List()
	if len(objs) != 1 {
		return fmt.Errorf("single KV object should exist in the cluster")
	}
	kv := (objs[0]).(*v1alpha1.KubeVirt)

	clusterConfig, err := virtconfig.NewClusterConfig(crdInformer, kubeVirtInformer, kv.Namespace)
	if err != nil {
		return err
	}

	fakeVal := "NotImportantWeJustNeedTheTargetResources"
	ctrl.templateSvc = services.NewTemplateService(fakeVal,
		240,
		fakeVal,
		fakeVal,
		fakeVal,
		fakeVal,
		fakeVal,
		fakeVal,
		pvcInformer.GetStore(),
		ctrl.virtCli,
		clusterConfig,
		107,
		fakeVal,
	)
	log.Log.Infof("Virt Informers and TmplSrv are set")
	return nil
}

func (ctrl *ManagedQuotaController) shouldSetUpKVInformersAndTmplSrv() bool {
	return ctrl.templateSvc == nil
}

func kvInstalled(virtCli kubecli.KubevirtClient) bool {
	_, err := virtCli.VirtualMachineInstance(metav1.NamespaceAll).List(context.Background(), &metav1.ListOptions{})
	return err == nil
}
