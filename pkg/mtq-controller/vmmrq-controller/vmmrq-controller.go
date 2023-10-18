package vmmrq_controller

import (
	"context"
	"fmt"
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
	api "k8s.io/kubernetes/pkg/apis/core"
	v13 "k8s.io/kubernetes/pkg/apis/core/v1"
	"k8s.io/kubernetes/pkg/quota/v1/evaluator/core"
	limitrange "k8s.io/kubernetes/plugin/pkg/admission/limitranger"
	"k8s.io/utils/clock"
	v1alpha1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
	"kubevirt.io/client-go/log"
	corev1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"kubevirt.io/containerized-data-importer/pkg/util/cert/fetcher"
	"kubevirt.io/kubevirt/pkg/util/migrations"
	virtconfig "kubevirt.io/kubevirt/pkg/virt-config"
	"kubevirt.io/kubevirt/pkg/virt-controller/services"
	v1alpha13 "kubevirt.io/managed-tenant-quota/pkg/generated/clientset/versioned/typed/core/v1alpha1"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-controller/namespace-lock-utils"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/utils"
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
	Immediate           enqueueState = "Immediate"
	Forget              enqueueState = "Forget"
	BackOff             enqueueState = "BackOff"
	AddAfterFiveSeconds enqueueState = "AddAfterFiveSeconds"
)

type VmmrqController struct {
	mtqNs                                        string
	stop                                         <-chan struct{}
	nsCache                                      *namespace_lock_utils.NamespaceCache
	internalLock                                 *sync.Mutex
	nsLockMap                                    *namespace_lock_utils.NamespaceLockMap
	podInformer                                  cache.SharedIndexInformer
	limitRangeInformer                           cache.SharedIndexInformer
	migrationInformer                            cache.SharedIndexInformer
	resourceQuotaInformer                        cache.SharedIndexInformer
	vmmrqInformer                                cache.SharedIndexInformer
	vmiInformer                                  cache.SharedIndexInformer
	kubeVirtInformer                             cache.SharedIndexInformer
	crdInformer                                  cache.SharedIndexInformer
	pvcInformer                                  cache.SharedIndexInformer
	nsInformer                                   cache.SharedIndexInformer
	clusterConfig                                *virtconfig.ClusterConfig
	virtualMachineMigrationResourceQuotaInformer cache.SharedIndexInformer
	migrationQueue                               workqueue.RateLimitingInterface
	virtCli                                      kubecli.KubevirtClient
	mtqCli                                       v1alpha13.MtqV1alpha1Client
	recorder                                     record.EventRecorder
	podEvaluator                                 v12.Evaluator
	serverBundleFetcher                          *fetcher.ConfigMapCertBundleFetcher
	templateSvc                                  services.TemplateService
}

func NewVmmrqController(virtCli kubecli.KubevirtClient,
	mtqNs string, mtqCli v1alpha13.MtqV1alpha1Client,
	vmiInformer cache.SharedIndexInformer,
	migrationInformer cache.SharedIndexInformer,
	podInformer cache.SharedIndexInformer,
	limitRangeInformer cache.SharedIndexInformer,
	kubeVirtInformer cache.SharedIndexInformer,
	crdInformer cache.SharedIndexInformer,
	pvcInformer cache.SharedIndexInformer,
	resourceQuotaInformer cache.SharedIndexInformer,
	vmmrqInformer cache.SharedIndexInformer,
	nsInformer cache.SharedIndexInformer,
) *VmmrqController {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&v14.EventSinkImpl{Interface: virtCli.CoreV1().Events(v1.NamespaceAll)})

	ctrl := VmmrqController{
		mtqNs:                 mtqNs,
		virtCli:               virtCli,
		mtqCli:                mtqCli,
		migrationInformer:     migrationInformer,
		podInformer:           podInformer,
		limitRangeInformer:    limitRangeInformer,
		resourceQuotaInformer: resourceQuotaInformer,
		vmmrqInformer:         vmmrqInformer,
		vmiInformer:           vmiInformer,
		kubeVirtInformer:      kubeVirtInformer,
		crdInformer:           crdInformer,
		pvcInformer:           pvcInformer,
		nsInformer:            nsInformer,
		serverBundleFetcher:   &fetcher.ConfigMapCertBundleFetcher{Name: "mtq-lock-signer-bundle", Client: virtCli.CoreV1().ConfigMaps(mtqNs)},
		migrationQueue:        workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "migration-queue"),
		internalLock:          &sync.Mutex{},
		nsLockMap:             &namespace_lock_utils.NamespaceLockMap{M: make(map[string]*sync.Mutex), Mutex: &sync.Mutex{}},
		nsCache:               namespace_lock_utils.NewNamespaceCache(),
		recorder:              eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: utils.ControllerPodName}),
		podEvaluator:          core.NewPodEvaluator(nil, clock.RealClock{}),
		templateSvc:           nil,
	}

	return &ctrl
}

// When a virtualMachineMigrationResourceQuota is added, figure out if there are pending migration in the namespace
// if there are we should push them into the queue to accelerate the target creation process.
func (ctrl *VmmrqController) addVmmrq(obj interface{}) {
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
func (ctrl *VmmrqController) updateVmmrq(old, cur interface{}) {
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

func (ctrl *VmmrqController) deleteMigration(obj interface{}) {
	ctrl.enqueueMigration(obj)
}

func (ctrl *VmmrqController) updateMigration(_, curr interface{}) {
	ctrl.enqueueMigration(curr)
}
func (ctrl *VmmrqController) enqueueMigration(obj interface{}) {
	logger := log.Log
	migration := obj.(*v1alpha1.VirtualMachineInstanceMigration)
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(migration)
	if err != nil {
		logger.Object(migration).Reason(err).Error("Failed to extract key from migration.")
		return
	}
	ctrl.migrationQueue.Add(key)
}

func (ctrl *VmmrqController) runWorker() {
	for ctrl.Execute() {
	}
}

func (ctrl *VmmrqController) Execute() bool {
	key, quit := ctrl.migrationQueue.Get()
	if quit {
		return false
	}
	defer ctrl.migrationQueue.Done(key)

	err, enqueueState := ctrl.execute(key.(string))
	if err != nil {
		log.Log.Reason(err).Infof(fmt.Sprintf("VmmrqController: Error with key: %v", key))
	}
	switch enqueueState {
	case BackOff:
		ctrl.migrationQueue.AddRateLimited(key)
	case Forget:
		ctrl.migrationQueue.Forget(key)
	case Immediate:
		ctrl.migrationQueue.Add(key)
	case AddAfterFiveSeconds:
		ctrl.migrationQueue.AddAfter(key, time.Second*5)
	}

	return true
}

func (ctrl *VmmrqController) execute(key string) (error, enqueueState) {
	migrationObj, migrationExists, err := ctrl.migrationInformer.GetStore().GetByKey(key)
	if err != nil {
		return err, Forget
	}
	migartionNS, migrationName, err := parseKey(key)
	if err != nil {
		return err, Forget
	}
	ctrl.nsLockMap.Lock(migartionNS)
	defer ctrl.nsLockMap.Unlock(migartionNS)
	vmmrqObjsList, err := ctrl.mtqCli.VirtualMachineMigrationResourceQuotas(migartionNS).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return err, BackOff
	}
	if len(vmmrqObjsList.Items) != 1 {
		return fmt.Errorf("there should be 1 virtualMachineMigrationResourceQuota in %v namespace", migartionNS), Forget
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
		} else if !vmiExists { //vmi for migration doesn't exist.
			return fmt.Errorf("VMI doesn't exist for migration"), BackOff
		}
		vmi = vmiObj.(*v1alpha1.VirtualMachineInstance)
		isBlockedMigration, err = ctrl.isBlockedMigration(vmi, migration)
		if err != nil {
			return err, BackOff
		}
	}
	reachedMaxParallelMigrations, err := ctrl.reachedMaxParallelMigration(vmi.Status.NodeName)
	if err != nil {
		return err, Immediate
	}

	finalEnqueueState := Forget
	if !isBlockedMigration {
		delete(vmmrq.Status.MigrationsToBlockingResourceQuotas, migrationName)
	} else if reachedMaxParallelMigrations {
		delete(vmmrq.Status.MigrationsToBlockingResourceQuotas, migrationName)
		finalEnqueueState = AddAfterFiveSeconds
	} else if isBlockedMigration {
		finalEnqueueState = Immediate
		targetPod, err := ctrl.templateSvc.RenderLaunchManifest(vmi)
		if err != nil {
			return err, Immediate
		}
		lrWithMaxDefaults, err := ctrl.getLimitRangeWithMaxDefaults(migartionNS)
		if err != nil {
			return err, Immediate
		}
		err = addLimitRangeDefault(lrWithMaxDefaults, targetPod)
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
	case namespace_lock_utils.Locked:
		nsLocked = true
	case namespace_lock_utils.Unlocked:
		nsLocked = false
	case namespace_lock_utils.Unknown: //expensive api call
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
		ctrl.nsCache.MarkLockStateLocked(migartionNS)
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
		ctrl.nsCache.MarkLockStateUnlocked(migartionNS)
	}
	return nil, finalEnqueueState
}

func (ctrl *VmmrqController) getRQListToRestore(currVmmrq *v1alpha12.VirtualMachineMigrationResourceQuota, prevVmmrq *v1alpha12.VirtualMachineMigrationResourceQuota) []v1alpha12.ResourceQuotaNameAndSpec {
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

func (ctrl *VmmrqController) isBlockedMigration(vmi *v1alpha1.VirtualMachineInstance, migration *v1alpha1.VirtualMachineInstanceMigration) (bool, error) {
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
func (ctrl *VmmrqController) getAllBlockingRQsInNS(vmmrq *v1alpha12.VirtualMachineMigrationResourceQuota, ns string) ([]v1alpha12.ResourceQuotaNameAndSpec, error) {
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

func (ctrl *VmmrqController) getLimitRangeWithMaxDefaults(ns string) (*v1.LimitRange, error) {
	maxDefaults := make(v1.ResourceList)
	maxRequests := make(v1.ResourceList)
	currLRListObj, err := ctrl.limitRangeInformer.GetIndexer().ByIndex(cache.NamespaceIndex, ns)
	if err != nil {
		return nil, err
	}
	for _, lrObj := range currLRListObj {
		lr := lrObj.(*v1.LimitRange)
		for _, limitRangeItem := range lr.Spec.Limits {
			if limitRangeItem.Type != v1.LimitTypeContainer {
				continue
			}
			for resourceName, quantity := range limitRangeItem.Default {
				if val, exists := maxDefaults[resourceName]; !exists || quantity.Cmp(val) > 0 {
					maxDefaults[resourceName] = quantity
				}
			}
			for resourceName, quantity := range limitRangeItem.DefaultRequest {
				if val, exists := maxRequests[resourceName]; !exists || quantity.Cmp(val) > 0 {
					maxRequests[resourceName] = quantity
				}
			}
		}
	}

	return &v1.LimitRange{
		Spec: v1.LimitRangeSpec{
			Limits: []v1.LimitRangeItem{
				{
					Default:        maxDefaults,
					DefaultRequest: maxRequests,
					Type:           v1.LimitTypeContainer,
				},
			},
		},
	}, nil
}

func (ctrl *VmmrqController) Run(threadiness int, stop <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	_, err := ctrl.migrationInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: ctrl.deleteMigration,
		UpdateFunc: ctrl.updateMigration,
	})
	if err != nil {
		return err
	}
	_, err = ctrl.vmmrqInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    ctrl.addVmmrq,
		UpdateFunc: ctrl.updateVmmrq,
	})
	if err != nil {
		return err
	}

	objs := ctrl.kubeVirtInformer.GetIndexer().List()
	if len(objs) != 1 {
		return fmt.Errorf("single KV object should exist in the cluster")
	}
	kv := (objs[0]).(*v1alpha1.KubeVirt)

	clusterConfig, err := virtconfig.NewClusterConfig(ctrl.crdInformer, ctrl.kubeVirtInformer, kv.Namespace)
	if err != nil {
		return err
	}
	ctrl.clusterConfig = clusterConfig

	fakeVal := "NotImportantWeJustNeedTheTargetResources"
	ctrl.templateSvc = services.NewTemplateService(fakeVal,
		240,
		fakeVal,
		fakeVal,
		fakeVal,
		fakeVal,
		fakeVal,
		fakeVal,
		ctrl.pvcInformer.GetStore(),
		ctrl.virtCli,
		clusterConfig,
		107,
		fakeVal,
		ctrl.resourceQuotaInformer.GetStore(),
		ctrl.nsInformer.GetStore(),
	)
	log.Log.Info("Starting Vmmrq controller")
	defer log.Log.Info("Shutting down Vmmrq controller")

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

func (ctrl *VmmrqController) restoreOriginalRQs(rqSpecAndNameListToRestore []v1alpha12.ResourceQuotaNameAndSpec, namespace string) error {
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
			log.Log.Infof("VmmrqController: Failed to extract key from resourceQuota.")
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

func (ctrl *VmmrqController) addResourcesToRQs(currVmmrq *v1alpha12.VirtualMachineMigrationResourceQuota, namespace string) error {
	for _, quotaNameAndSpec := range currVmmrq.Status.OriginalBlockingResourceQuotas {
		key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(
			&v1.ResourceQuota{
				ObjectMeta: metav1.ObjectMeta{
					Name:      quotaNameAndSpec.Name,
					Namespace: namespace,
				},
			})
		if err != nil {
			log.Log.Infof("VmmrqController: Failed to extract key from resourceQuota.")
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

func (ctrl *VmmrqController) getCurrBlockingRQInNS(vmmrq *v1alpha12.VirtualMachineMigrationResourceQuota, m *v1alpha1.VirtualMachineInstanceMigration, podToCreate *v1.Pod, resourceListToAdd v1.ResourceList) ([]string, error) {
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
			podToCreate.Namespace = m.Namespace // for some reason renderLaunchManifest doesn't set the pod namespace
			ctrl.recorder.Eventf(podToCreate, v1.EventTypeWarning, FailedToReleaseMigrationReason, evtMsg, errWithModifiedRQ.Error())
			return []string{}, nil
		} else {
			log.Log.Info(fmt.Sprintf("errWithCurrRQ:%v errWithModifiedRQ: %v", errWithCurrRQ, errWithModifiedRQ))
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

func (ctrl *VmmrqController) shouldSetUpKVInformersAndTmplSrv() bool {
	return ctrl.templateSvc == nil
}

func addLimitRangeDefault(lrWithMaxDefaults *v1.LimitRange, targetPod *v1.Pod) error {
	apiTargetPod := &api.Pod{}
	err := v13.Convert_v1_Pod_To_core_Pod(targetPod, apiTargetPod, nil)
	if err != nil {
		return err
	}
	err = limitrange.PodMutateLimitFunc(lrWithMaxDefaults, apiTargetPod)
	if err != nil {
		return err
	}
	err = v13.Convert_core_Pod_To_v1_Pod(apiTargetPod, targetPod, nil)
	if err != nil {
		return err
	}
	return nil
}

func (ctrl *VmmrqController) outboundMigrationsOnNode(node string, runningMigrations []*v1alpha1.VirtualMachineInstanceMigration) int {
	sum := 0
	for _, migration := range runningMigrations {
		if vmi, exists, _ := ctrl.vmiInformer.GetStore().GetByKey(migration.Namespace + "/" + migration.Spec.VMIName); exists {
			if vmi.(*v1alpha1.VirtualMachineInstance).Status.NodeName == node {
				sum = sum + 1
			}
		}
	}
	return sum
}

// findRunningMigrations calcules how many migrations are running or in flight to be triggered to running
// Migrations which are in running phase are added alongside with migrations which are still pending but
// where we already see a target pod.
func (ctrl *VmmrqController) findRunningMigrations() ([]*v1alpha1.VirtualMachineInstanceMigration, error) {
	// Don't start new migrations if we wait for migration object updates because of new target pods
	notFinishedMigrations := migrations.ListUnfinishedMigrations(ctrl.migrationInformer)
	var runningMigrations []*v1alpha1.VirtualMachineInstanceMigration
	for _, migration := range notFinishedMigrations {
		if migration.IsRunning() {
			runningMigrations = append(runningMigrations, migration)
			continue
		}
		vmi, exists, err := ctrl.vmiInformer.GetStore().GetByKey(migration.Namespace + "/" + migration.Spec.VMIName)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		pods, err := ctrl.listMatchingTargetPods(migration, vmi.(*v1alpha1.VirtualMachineInstance))
		if err != nil {
			return nil, err
		}
		if len(pods) > 0 {
			runningMigrations = append(runningMigrations, migration)
		}
	}
	return runningMigrations, nil
}

func (ctrl *VmmrqController) listMatchingTargetPods(migration *v1alpha1.VirtualMachineInstanceMigration, vmi *v1alpha1.VirtualMachineInstance) ([]*v1.Pod, error) {

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

	objs, err := ctrl.podInformer.GetIndexer().ByIndex(cache.NamespaceIndex, migration.Namespace)
	if err != nil {
		return nil, err
	}

	var pods []*v1.Pod
	for _, obj := range objs {
		pod := obj.(*v1.Pod)
		if selector.Matches(labels.Set(pod.ObjectMeta.Labels)) {
			pods = append(pods, pod)
		}
	}

	return pods, nil
}

func (ctrl *VmmrqController) reachedMaxParallelMigration(nodeName string) (bool, error) {
	runningMigrations, err := ctrl.findRunningMigrations()
	if err != nil {
		return false, fmt.Errorf("failed to determin the number of running migrations: %v", err)
	}

	if len(runningMigrations) >= int(*ctrl.clusterConfig.GetMigrationConfiguration().ParallelMigrationsPerCluster) {
		return true, nil
	}

	outboundMigrations := ctrl.outboundMigrationsOnNode(nodeName, runningMigrations)

	if outboundMigrations >= int(*ctrl.clusterConfig.GetMigrationConfiguration().ParallelOutboundMigrationsPerNode) {
		return true, nil
	}

	return false, nil
}
