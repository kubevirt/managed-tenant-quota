package mtq_controller

import (
	"context"
	"encoding/base64"
	"fmt"
	v13 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
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
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/quota/v1/evaluator/core"
	"k8s.io/utils/clock"
	v1alpha1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
	"kubevirt.io/client-go/log"
	corev1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	v1alpha12 "kubevirt.io/managed-tenant-quota/pkg/apis/core/v1alpha1"
	v1alpha13 "kubevirt.io/managed-tenant-quota/pkg/generated/clientset/versioned/typed/core/v1alpha1"
	"kubevirt.io/managed-tenant-quota/pkg/util"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"
)

const (
	FailedToReleaseMigrationReason                         string                                                = "NotEnoughResourceToRelease"
	VirtualMachineInstanceMigrationRejectedByResourceQuota v1alpha1.VirtualMachineInstanceMigrationConditionType = "migrationRejectedByResourceQuota"
)

type ManagedQuotaController struct {
	nsCache               *NamespaceCache
	internalLock          *sync.Mutex
	podInformer           cache.SharedIndexInformer
	migrationInformer     cache.SharedIndexInformer
	resourceQuotaInformer cache.SharedIndexInformer
	vmmrqInformer         cache.SharedIndexInformer
	vmiInformer           cache.SharedIndexInformer
	migrationQueue        workqueue.RateLimitingInterface
	virtCli               kubecli.KubevirtClient
	mtqCli                v1alpha13.VirtualMachineMigrationResourceQuotaV1alpha1Client
	recorder              record.EventRecorder
	podEvaluator          v12.Evaluator
	limitedResources      []resourcequota.LimitedResource
}

func NewManagedQuotaController(virtCli kubecli.KubevirtClient, mtqCli v1alpha13.VirtualMachineMigrationResourceQuotaV1alpha1Client, migrationInformer cache.SharedIndexInformer, podInformer cache.SharedIndexInformer, resourceQuotaInformer cache.SharedIndexInformer, vmmrqInformer cache.SharedIndexInformer, vmiInformer cache.SharedIndexInformer) *ManagedQuotaController {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&v14.EventSinkImpl{Interface: virtCli.CoreV1().Events(v1.NamespaceAll)})

	ctrl := ManagedQuotaController{
		virtCli:               virtCli,
		mtqCli:                mtqCli,
		migrationInformer:     migrationInformer,
		podInformer:           podInformer,
		resourceQuotaInformer: resourceQuotaInformer,
		vmmrqInformer:         vmmrqInformer,
		vmiInformer:           vmiInformer,
		migrationQueue:        workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "migration-queue"),
		internalLock:          &sync.Mutex{},
		nsCache:               NewNamespaceCache(),
		recorder:              eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "mtq-controller"}),
		podEvaluator:          core.NewPodEvaluator(nil, clock.RealClock{}),
		limitedResources: []resourcequota.LimitedResource{
			{
				Resource:      "pods",
				MatchContains: []string{"cpu", "memory"}, //TODO: check if more resources should be considered
			},
		},
	}

	_, err := ctrl.migrationInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: ctrl.deleteMigration,
		UpdateFunc: ctrl.updateMigration,
	})
	if err != nil {
		return nil
	}
	_, err = ctrl.vmmrqInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    ctrl.addVmmrq,
		UpdateFunc: ctrl.updateVmmrq,
	})

	if err != nil {
		return nil
	}

	return &ctrl
}

// When a virtualMachineMigrationResourceQuota is added, figure out if there are pending migration in the namespace
// if there are we should push them into the queue to accelerate the target creation process
func (ctrl *ManagedQuotaController) addVmmrq(obj interface{}) {
	vmmrq := obj.(*v1alpha12.VirtualMachineMigrationResourceQuota)
	log.Log.V(4).Object(vmmrq).Infof("VMMRQ " + vmmrq.Name + " added")
	objs, _ := ctrl.migrationInformer.GetIndexer().ByIndex(cache.NamespaceIndex, vmmrq.Namespace)
	for _, obj := range objs {
		migration := obj.(*v1alpha1.VirtualMachineInstanceMigration)
		if migration.Status.Conditions == nil {
			continue
		}
		for _, cond := range migration.Status.Conditions {
			if cond.Type == VirtualMachineInstanceMigrationRejectedByResourceQuota {
				ctrl.enqueueMigration(migration)
			}
		}
	}
	return
}

// When a virtualMachineMigrationResourceQuota is updated , figure out if there are pending migration in the namespace
// if there are we should push them into the queue to accelerate the target creation process
func (ctrl *ManagedQuotaController) updateVmmrq(old, cur interface{}) {
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
			if cond.Type == VirtualMachineInstanceMigrationRejectedByResourceQuota {
				ctrl.enqueueMigration(migration)
			}
		}
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

	err := ctrl.execute(key.(string))
	if err != nil {
		log.Log.Reason(err).Infof("ManagedQuotaController: reenqueuing %v", key)
		ctrl.migrationQueue.AddRateLimited(key) //TODO: Divide it to two types of errors one for immediate enqueue and one for rateLimited
	} else {
		log.Log.Infof("ManagedQuotaController: processed obj: %v", key)
		ctrl.migrationQueue.Forget(key)
	}

	return true
}

func (ctrl *ManagedQuotaController) execute(key string) error {
	migrationObj, migrationExists, err := ctrl.migrationInformer.GetStore().GetByKey(key)
	if err != nil {
		return err
	}
	migartionNS, migrationName, err := parseKey(key)
	if err != nil {
		return err
	}
	var migration *v1alpha1.VirtualMachineInstanceMigration
	var vmi *v1alpha1.VirtualMachineInstance
	if migrationExists {
		migration = migrationObj.(*v1alpha1.VirtualMachineInstanceMigration)
		vmiObj, vmiExists, err := ctrl.vmiInformer.GetStore().GetByKey(fmt.Sprintf("%s/%s", migartionNS, migration.Spec.VMIName))
		if err != nil {
			return err
		} else if !vmiExists { //vmi for migration doesn't exist
			return fmt.Errorf("VMI doesn't exist for migration")
		}
		vmi = vmiObj.(*v1alpha1.VirtualMachineInstance)
	} else {
		return nil
	}

	vmmrqObjsList, err := ctrl.vmmrqInformer.GetIndexer().ByIndex(cache.NamespaceIndex, migartionNS)
	if err != nil {
		return err
	}
	if len(vmmrqObjsList) != 1 {
		return fmt.Errorf("there should be 1 virtualMachineMigrationResourceQuota in %v namespace", migartionNS)
	}

	vmmrq := vmmrqObjsList[0].(*v1alpha12.VirtualMachineMigrationResourceQuota)
	if vmmrq.Status.MigrationsToBlockingResourceQuotas == nil {
		vmmrq.Status.MigrationsToBlockingResourceQuotas = make(map[string][]string)
	}
	prevVmmrq := vmmrq.DeepCopy()

	isBlockedMigration, err := ctrl.isBlockedMigration(vmi, migration)
	if err != nil {
		return err
	}

	if !isBlockedMigration { //todo: if the migration is done forget key
		rqListDeletedFromMap := vmmrq.Status.MigrationsToBlockingResourceQuotas[migrationName]
		delete(vmmrq.Status.MigrationsToBlockingResourceQuotas, migrationName)
		blockingRQsList := getAllBlockingRQsInNS(vmmrq.Status.MigrationsToBlockingResourceQuotas)
		nonBlockingRQs := getNonBlockingRQ(rqListDeletedFromMap, *blockingRQsList) //check which RQs don't block any other migration
		vmmrq.Status.OriginalBlockingResourceQuotas = removeRQsFromBlockingRQList(vmmrq.Status.OriginalBlockingResourceQuotas, nonBlockingRQs)
		log.Log.Reason(err).Infof(fmt.Sprintf("OriginalBlockingResourceQuotas  map now : %+v", vmmrq.Status.OriginalBlockingResourceQuotas))
	} else if isBlockedMigration {
		//todo: check if there is modified resourceQuota that will be stuck forever and if so release it
		sourcePod, err := CurrentVMIPod(vmi, ctrl.podInformer)
		if err != nil {
			return err
		}
		currBlockingRQList, err := ctrl.getCurrBlockingRQInNS(vmmrq, migration, sourcePod, vmmrq.Spec.AdditionalMigrationResources)
		if err != nil {
			return err
		}
		vmmrq.Status.MigrationsToBlockingResourceQuotas[migrationName] = currBlockingRQList
		oldBlockingRQList := vmmrq.Status.OriginalBlockingResourceQuotas
		allBlockingRQsInNS, err := ctrl.getAllBlockingRQsInNS(vmmrq.Status.MigrationsToBlockingResourceQuotas, migartionNS)
		finalBlockingRQList := newBlockingRQs(oldBlockingRQList, allBlockingRQsInNS)
		vmmrq.Status.OriginalBlockingResourceQuotas = finalBlockingRQList
	}
	rqListToRestore := ctrl.getRQListToRestore(vmmrq, prevVmmrq)
	shouldLockNS := shouldLockNS(vmmrq)
	var nsLocked bool

	ctrl.internalLock.Lock()
	defer ctrl.internalLock.Unlock()

	switch ctrl.nsCache.GetLockState(migartionNS) {
	case Locked:
		nsLocked = true
	case Unlocked:
		nsLocked = false
	case Unknown: //expensive api call
		nsLocked, err = namespaceLocked(migartionNS, ctrl.virtCli)
		if err != nil {
			return err
		}
	}
	if shouldLockNS && !nsLocked {
		err := lockNamespace(migartionNS, ctrl.virtCli)
		if err != nil {
			return err
		}
		ctrl.nsCache.markLockStateLocked(migartionNS)
	}
	err = ctrl.restoreOriginalRQs(rqListToRestore, prevVmmrq.Status.OriginalBlockingResourceQuotas, migartionNS)
	if err != nil {
		return err
	}
	if shouldUpdateVmmrq(vmmrq, prevVmmrq) {
		_, err := ctrl.mtqCli.VirtualMachineMigrationResourceQuotas(migartionNS).UpdateStatus(context.Background(), vmmrq, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	err = ctrl.addResourcesToRQs(vmmrq.Status.OriginalBlockingResourceQuotas, &vmmrq.Spec.AdditionalMigrationResources, migartionNS)
	if err != nil {
		return err
	}
	if !shouldLockNS && nsLocked {
		err := unlockNamespace(migartionNS, ctrl.virtCli)
		if err != nil {
			return err
		}
		ctrl.nsCache.markLockStateUnlocked(migartionNS)
	}
	return nil //todo requeue migration
}

func (ctrl *ManagedQuotaController) getRQListToRestore(currVmmrq *v1alpha12.VirtualMachineMigrationResourceQuota, prevVmmrq *v1alpha12.VirtualMachineMigrationResourceQuota) []v1alpha12.ResourceQuotaNameAndSpec {
	var rqToReduceList []v1alpha12.ResourceQuotaNameAndSpec
	for _, rqInCurr := range prevVmmrq.Status.OriginalBlockingResourceQuotas {
		found := false
		for _, rqInPrev := range currVmmrq.Status.OriginalBlockingResourceQuotas {
			if rqInCurr.Name == rqInPrev.Name {
				found = true
			}
		}
		if !found {
			rqToReduceList = append(rqToReduceList, rqInCurr)
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
		if cond.Type == VirtualMachineInstanceMigrationRejectedByResourceQuota {
			return true, nil
		}
	}
	return false, nil
}
func (ctrl *ManagedQuotaController) getAllBlockingRQsInNS(migrationsToBlockingRQMap map[string][]string, ns string) ([]v1alpha12.ResourceQuotaNameAndSpec, error) {
	var blockingRQInNS []v1alpha12.ResourceQuotaNameAndSpec
	blockingRQlist := getAllBlockingRQsInNS(migrationsToBlockingRQMap)
	currRQListObj, err := ctrl.resourceQuotaInformer.GetIndexer().ByIndex(cache.NamespaceIndex, ns)
	if err != nil {
		return nil, err
	}
	for _, blockingRQ := range *blockingRQlist {
		for _, obj := range currRQListObj {
			rqInNS := obj.(*v1.ResourceQuota)
			rqNameAndSpec := v1alpha12.ResourceQuotaNameAndSpec{
				Name: rqInNS.Name,
				Spec: rqInNS.Spec,
			}
			if rqInNS.Name == blockingRQ {
				blockingRQInNS = append(blockingRQInNS, rqNameAndSpec)
			}
		}
	}
	return blockingRQInNS, nil
}

func Run(threadiness int, stop <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	virtCli, err := util.GetVirtCli()
	if err != nil {
		return nil
	}
	mtqCli, err := util.GetMTQCli()
	if err != nil {
		klog.Fatalf("Unable to get mtqCli \n")
	}
	migrationInformer, err := util.GetMigrationInformer(virtCli)
	if err != nil {
		os.Exit(1)
	}
	podInformer, err := util.GetPodInformer(virtCli)
	if err != nil {
		os.Exit(1)
	}
	resourceQuotaInformer, err := util.GetResourceQuotaInformer(virtCli)
	if err != nil {
		os.Exit(1)
	}
	vmiInformer, err := util.GetVMIInformer(virtCli)
	if err != nil {
		os.Exit(1)
	}

	virtualMachineMigrationResourceQuotaInformer, err := util.GetVirtualMachineMigrationResourceQuotaInformer(mtqCli)
	if err != nil {
		os.Exit(1)
	}
	go migrationInformer.Run(stop)
	go podInformer.Run(stop)
	go resourceQuotaInformer.Run(stop)
	go virtualMachineMigrationResourceQuotaInformer.Run(stop)
	go vmiInformer.Run(stop)

	ctrl := NewManagedQuotaController(virtCli, mtqCli, migrationInformer, podInformer, resourceQuotaInformer, virtualMachineMigrationResourceQuotaInformer, vmiInformer)

	log.Log.Info("Starting Managed Quota controller")
	defer log.Log.Info("Shutting down Managed Quota controller")

	if !cache.WaitForCacheSync(stop, ctrl.migrationInformer.HasSynced, ctrl.podInformer.HasSynced, ctrl.resourceQuotaInformer.HasSynced, virtualMachineMigrationResourceQuotaInformer.HasSynced, vmiInformer.HasSynced) {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	for i := 0; i < threadiness; i++ {
		go wait.Until(ctrl.runWorker, time.Second, stop)
	}

	<-stop
	return nil

}

func CurrentVMIPod(vmi *v1alpha1.VirtualMachineInstance, podInformer cache.SharedIndexInformer) (*v1.Pod, error) {
	// current pod is the most recent pod created on the current VMI node
	// OR the most recent pod created if no VMI node is set.
	// Get all pods from the namespace
	objs, err := podInformer.GetIndexer().ByIndex(cache.NamespaceIndex, vmi.Namespace)
	if err != nil {
		return nil, err
	}
	pods := []*v1.Pod{}
	for _, obj := range objs {
		pod := obj.(*v1.Pod)
		pods = append(pods, pod)
	}

	var curPod *v1.Pod = nil
	for _, pod := range pods {
		if !IsControlledBy(pod, vmi) {
			continue
		}

		if vmi.Status.NodeName != "" &&
			vmi.Status.NodeName != pod.Spec.NodeName {
			// This pod isn't scheduled to the current node.
			// This can occur during the initial migration phases when
			// a new target node is being prepared for the VMI.
			continue
		}

		if curPod == nil || curPod.CreationTimestamp.Before(&pod.CreationTimestamp) {
			curPod = pod
		}
	}

	return curPod, nil
}

func IsControlledBy(pod *v1.Pod, vmi *v1alpha1.VirtualMachineInstance) bool {
	if controllerRef := GetControllerOf(pod); controllerRef != nil {
		return controllerRef.UID == vmi.UID
	}
	return false
}

// GetControllerOf returns the controllerRef if controllee has a controller,
// otherwise returns nil.
func GetControllerOf(pod *v1.Pod) *metav1.OwnerReference {
	controllerRef := metav1.GetControllerOf(pod)
	if controllerRef != nil {
		return controllerRef
	}
	// We may find pods that are only using CreatedByLabel and not set with an OwnerReference
	if createdBy := pod.Labels[v1alpha1.CreatedByLabel]; len(createdBy) > 0 {
		name := pod.Annotations[v1alpha1.DomainAnnotation]
		uid := types.UID(createdBy)
		vmi := v1alpha1.NewVMI(name, uid)
		return metav1.NewControllerRef(vmi, v1alpha1.VirtualMachineInstanceGroupVersionKind)
	}
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

func admitPodToQuota(resourceQuota *v1.ResourceQuota, attributes k8sadmission.Attributes, evaluator v12.Evaluator, limitedResources []resourcequota.LimitedResource) ([]v1.ResourceQuota, error) {
	return quotaplugin.CheckRequest([]v1.ResourceQuota{*resourceQuota}, attributes, evaluator, limitedResources)
}

func shouldLockNS(vmmrq *v1alpha12.VirtualMachineMigrationResourceQuota) bool {
	return len(vmmrq.Status.OriginalBlockingResourceQuotas) > 0
}

func getAllBlockingRQsInNS(migrationsToBlockingRQs map[string][]string) *[]string {
	var blockingRQs []string
	for _, blockingRQ := range migrationsToBlockingRQs {
		blockingRQs = append(blockingRQs, blockingRQ...)
	}
	return &blockingRQs
}

func (ctrl *ManagedQuotaController) restoreOriginalRQs(rqSpecAndNameListToRestore []v1alpha12.ResourceQuotaNameAndSpec, originalRQList []v1alpha12.ResourceQuotaNameAndSpec, namespace string) error {
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
		log.Log.Infof(fmt.Sprintf("ManagedQuotaController: updating: %v", quotaNameAndSpec.Name))

		rqToModifyObj, exists, err := ctrl.resourceQuotaInformer.GetStore().GetByKey(key)
		if err != nil {
			return err
		}

		if exists {
			rqToModify := rqToModifyObj.(*v1.ResourceQuota)
			origRQ := getFromListByName(originalRQList, rqToModify.Name)
			if reflect.DeepEqual(rqToModify.Spec.Hard, origRQ.Spec.Hard) {
				continue //already restored
			}
			rqToModify.Spec.Hard = origRQ.Spec.Hard
			_, err = ctrl.virtCli.CoreV1().ResourceQuotas(rqToModify.Namespace).Update(context.Background(), rqToModify, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (ctrl *ManagedQuotaController) addResourcesToRQs(rqNameAndSpeclist []v1alpha12.ResourceQuotaNameAndSpec, additionalMigrationResources *v1.ResourceList, namespace string) error {
	if len(rqNameAndSpeclist) == 0 {
		return nil
	}
	for _, quotaNameAndSpec := range rqNameAndSpeclist {
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
		log.Log.Infof(fmt.Sprintf("ManagedQuotaController: updating: %v", quotaNameAndSpec.Name))

		rqToModifyObj, exists, err := ctrl.resourceQuotaInformer.GetStore().GetByKey(key)
		if err != nil {
			return err
		}

		if exists {
			rqToModify := rqToModifyObj.(*v1.ResourceQuota)
			if !reflect.DeepEqual(rqToModify.Spec.Hard, quotaNameAndSpec.Spec.Hard) {
				continue //already modified
			}
			rqToModify.Spec.Hard = addResourcesToRQ(*rqToModify, additionalMigrationResources).Spec.Hard
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

func (ctrl *ManagedQuotaController) getCurrBlockingRQInNS(vmmrq *v1alpha12.VirtualMachineMigrationResourceQuota, m *v1alpha1.VirtualMachineInstanceMigration, podToCreate *v1.Pod, listToAdd v1.ResourceList) ([]string, error) {
	currRQListObj, err := ctrl.resourceQuotaInformer.GetIndexer().ByIndex(cache.NamespaceIndex, m.Namespace)
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
		if origRQNameAndSpec := getRQNameAndSpecIfExist(vmmrq.Status.OriginalBlockingResourceQuotas, resourceQuota.Name); origRQNameAndSpec != nil {
			resourceQuota.Spec = origRQNameAndSpec.Spec
		}
		resourceQuotaCopy := resourceQuota.DeepCopy()
		//Checking if the resourceQuota is blocking us
		_, errWithCurrRQ := admitPodToQuota(resourceQuotaCopy, podToCreateAttr, ctrl.podEvaluator, ctrl.limitedResources)
		//checking if the additional resources in vmmRQ will unblock us
		rqAfterResourcesAddition := addResourcesToRQ(*resourceQuotaCopy, &listToAdd)
		_, errWithModifiedRQ := admitPodToQuota(rqAfterResourcesAddition, podToCreateAttr, ctrl.podEvaluator, ctrl.limitedResources)
		if errWithCurrRQ != nil && strings.Contains(errWithCurrRQ.Error(), "exceeded quota") && errWithModifiedRQ == nil {
			currRQListItems = append(currRQListItems, (*resourceQuota).Name)
		} else if errWithModifiedRQ != nil && strings.Contains(errWithModifiedRQ.Error(), "exceeded quota") {
			evtMsg := fmt.Sprintf("Warning: Currently VMMRQ: %v in namespace: %v doesn't have enough resoures to release blocked migration: %v", vmmrq.Name, m.Namespace, m.Name)
			ctrl.recorder.Eventf(podToCreate, v1.EventTypeWarning, FailedToReleaseMigrationReason, evtMsg, errWithModifiedRQ.Error())
			return nil, errWithModifiedRQ
		}
	}
	return currRQListItems, nil
}

func newBlockingRQs(oldBlockingRQList, migrationBlockingRQList []v1alpha12.ResourceQuotaNameAndSpec) []v1alpha12.ResourceQuotaNameAndSpec {
	var newBlockingRQList []v1alpha12.ResourceQuotaNameAndSpec
	for _, modifiedRQ := range oldBlockingRQList {
		newBlockingRQList = append(newBlockingRQList, modifiedRQ)
	}
	// Add new ResourceQuotas from migrationBlockingRQList to newRQList
	for _, migrationBlockingRQ := range migrationBlockingRQList {
		found := false
		for _, oldBlockingRQ := range oldBlockingRQList {
			if oldBlockingRQ.Name == migrationBlockingRQ.Name {
				found = true
			}
		}
		if !found {
			newBlockingRQList = append(newBlockingRQList, migrationBlockingRQ)
		}
	}
	return newBlockingRQList
}

func addResourcesToRQ(rq v1.ResourceQuota, rl *v1.ResourceList) *v1.ResourceQuota {
	newRQ := &v1.ResourceQuota{
		Spec: v1.ResourceQuotaSpec{
			Hard: rq.Spec.Hard,
		},
	}
	for rqResourceName, _ := range newRQ.Spec.Hard {
		for vmmrqResourceName, quantityToAdd := range *rl {
			if resourceNamesMatch(rqResourceName, vmmrqResourceName) {
				quantity := newRQ.Spec.Hard[rqResourceName]
				quantity.Add(quantityToAdd)
				newRQ.Spec.Hard[rqResourceName] = quantity
			}
		}
	}
	return newRQ
}

func resourceNamesMatch(rqResourceName v1.ResourceName, vmmrqResourceName v1.ResourceName) bool {
	return (rqResourceName == v1.ResourceRequestsMemory && vmmrqResourceName == v1.ResourceMemory) ||
		(rqResourceName == v1.ResourceRequestsCPU && vmmrqResourceName == v1.ResourceCPU)
}

func getFromListByName(rql []v1alpha12.ResourceQuotaNameAndSpec, name string) *v1alpha12.ResourceQuotaNameAndSpec {
	for _, rq := range rql {
		if rq.Name == name {
			return &rq
		}
	}
	return nil
}

func getNonBlockingRQ(RQListRemoved []string, blockingRQList []string) []string {
	noLongerBlockingRQS := make([]string, 0)
	for _, rqRemoved := range RQListRemoved {
		found := false
		for _, blockingRQ := range blockingRQList {
			if rqRemoved == blockingRQ {
				found = true
				break
			}
		}
		if !found {
			noLongerBlockingRQS = append(noLongerBlockingRQS, rqRemoved)
		}
	}
	return noLongerBlockingRQS
}

func removeRQsFromBlockingRQList(blockingRQsList []v1alpha12.ResourceQuotaNameAndSpec, rqListToRemove []string) []v1alpha12.ResourceQuotaNameAndSpec {
	var newBlockingRQsList []v1alpha12.ResourceQuotaNameAndSpec
	for _, rq := range blockingRQsList {
		found := false
		for _, rqToRemove := range rqListToRemove {
			if rq.Name == rqToRemove {
				found = true
			}
		}
		if !found {
			newBlockingRQsList = append(newBlockingRQsList, rq)
		}
	}

	return newBlockingRQsList
}

func lockNamespace(ns string, cli kubecli.KubevirtClient) error {
	lockingValidationWebHook := v13.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "lock." + ns + ".com",
		},
		Webhooks: []v13.ValidatingWebhook{getLockingValidatingWebhook(ns)},
	}
	_, err := cli.AdmissionregistrationV1().ValidatingWebhookConfigurations().Create(context.Background(), &lockingValidationWebHook, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

func unlockNamespace(ns string, cli kubecli.KubevirtClient) error {
	err := cli.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(context.Background(), "lock."+ns+".com", metav1.DeleteOptions{})
	if err != nil {
		return err
	}
	return nil
}

func namespaceLocked(ns string, cli kubecli.KubevirtClient) (bool, error) {
	webhookName := "lock." + ns + ".com"
	ValidatingWH, err := cli.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(context.Background(), webhookName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, err
	} else if !isOurValidatingWebhookConfiguration(ValidatingWH, ns) {
		return false, fmt.Errorf("Can't lock namespace, ValidatingWebhookConfiguration with name " + webhookName + " and diffrent logic already exist")
	}
	return true, nil
}

func isOurValidatingWebhookConfiguration(existingVWHC *v13.ValidatingWebhookConfiguration, ns string) bool {
	expectedVWH := getLockingValidatingWebhook(ns)
	if len(existingVWHC.Webhooks) != 1 {
		return false
	}

	existingVWH := existingVWHC.Webhooks[0]

	expectedClientConfigSvc := expectedVWH.ClientConfig.Service
	existingClientConfigSvc := existingVWH.ClientConfig.Service
	expectedRules := expectedVWH.Rules
	existingRules := existingVWH.Rules

	if existingVWH.Name != expectedVWH.Name ||
		!reflect.DeepEqual(existingVWH.AdmissionReviewVersions, expectedVWH.AdmissionReviewVersions) ||
		*existingVWH.FailurePolicy != *expectedVWH.FailurePolicy ||
		*existingVWH.SideEffects != *expectedVWH.SideEffects ||
		*existingVWH.MatchPolicy != *expectedVWH.MatchPolicy {
		return false
	}
	if !reflect.DeepEqual(expectedClientConfigSvc, existingClientConfigSvc) {
		return false
	}

	if !reflect.DeepEqual(expectedRules, existingRules) {
		return false
	}

	return true
}

func getLockingValidatingWebhook(ns string) v13.ValidatingWebhook {
	fail := v13.Fail
	sideEffects := v13.SideEffectClassNone
	equivalent := v13.Equivalent
	namespacedScope := v13.NamespacedScope
	path := "/validate-pods"
	port := int32(443)
	//TODO: add this dynamically once the operator is ready
	caBundle, _ := base64.StdEncoding.DecodeString("LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURkRENDQWx5Z0F3SUJBZ0lVVnA3Q0FQZ2VLcmU5OUNoeVh3emk4SVN6VFA0d0RRWUpLb1pJaHZjTkFRRUwKQlFBd1VqRUxNQWtHQTFVRUJoTUNRVlV4RURBT0JnTlZCQWdUQjBWNFlXMXdiR1V4RWpBUUJnTlZCQWNUQ1UxbApiR0p2ZFhKdVpURVFNQTRHQTFVRUNoTUhSWGhoYlhCc1pURUxNQWtHQTFVRUN4TUNRMEV3SGhjTk1qTXdORE13Ck1EY3hOREF3V2hjTk1qZ3dOREk0TURjeE5EQXdXakJTTVFzd0NRWURWUVFHRXdKQlZURVFNQTRHQTFVRUNCTUgKUlhoaGJYQnNaVEVTTUJBR0ExVUVCeE1KVFdWc1ltOTFjbTVsTVJBd0RnWURWUVFLRXdkRmVHRnRjR3hsTVFzdwpDUVlEVlFRTEV3SkRRVENDQVNJd0RRWUpLb1pJaHZjTkFRRUJCUUFEZ2dFUEFEQ0NBUW9DZ2dFQkFLVU5VQnBQCmtaT0l1QkE3MFV0Mk1VUG0yYXF1dExtNzZhcFg2Ykl2aURkYVJqVXhLU3c3TVV2RmVsUVNzL010YkVNSk5jN2kKN1FtOTVXdzdwV3l2eXZUS1N1eDJtV09tS3ZwMlRma0o4R2ZhVDhEMENPUU03cGhubHNZNHp5L0JBYTUrNU9heApKV3N4K0E4V2JWTS9rTnJhenBlVXFOTEQ2aU9QOFBNb2ZsTnJidXdTbCtPMFMrMUVOUE1wQ2hPTmM5OGdSQy9TCndaZkVUTW9XMy9aVUxoMjRjSkY0MVFNanU2YklFWCt4QlRjYTlad2lxc2dTSGthZi9XeklhMDA0UnRLTkU5cWsKNUtYMThhTXMwNXhPQTZsSHVXeUxaRFdXbTI2aFc0Z3ZrUmFrZVZIdVl3Q2IwZ0FDVUcrVkEyd1c1bWhUTm96eAphTm1xSm1IMTViaUdvODBDQXdFQUFhTkNNRUF3RGdZRFZSMFBBUUgvQkFRREFnRUdNQThHQTFVZEV3RUIvd1FGCk1BTUJBZjh3SFFZRFZSME9CQllFRkIyTXF2TXd3Y2E1ZWpHQlN6OG1aT29GcEJ2dk1BMEdDU3FHU0liM0RRRUIKQ3dVQUE0SUJBUUJqYlhLQXQwNGlQUC9NWUZmWFoxeHIwRDJnNnRRWlE1Lzk1SXhPbFVHVEdycDZCc3dFVG9UWQpneHhZU0VEVnZMVTU2RzZwZENBUklMVCtUVnB2YVVKNlV0UlJFbHF1ZFRqK3lwREhQRzdSaW9ONE1xYVlxWisrCkFjdDhwNGhKY3dCLzU1Yll6TU0vUlNBUDNyUDhSTm13Si9VUXNkczdBZElkaTZzRE9ZR0ZUN3FMbXlYdUQ0TU8KVDA0dEozazJVcW5wb2Z0QmM0ZlpsZ1JPTkZDRW9jWFlEanFlODdiU1F4K3k1eGlrb1FBZUt2VU1SMHBSZ0RUTwpMcHY3QWEvazQyZTI5MmlTM2xFZ004N1ROZ1d4T3dYK3FTZEhPQmxscnU1R09Qa0dTaHRsS2Fha0NRaW54cUNzCkE1ZjUyOFdLenpwbjNWM0J5eUhJRElXa0dZck1aNEtlCi0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K")
	return v13.ValidatingWebhook{
		Name:                    "lock." + ns + ".com",
		AdmissionReviewVersions: []string{"v1", "v1beta1"},
		FailurePolicy:           &fail,
		SideEffects:             &sideEffects,
		MatchPolicy:             &equivalent,
		Rules: []v13.RuleWithOperations{
			{
				Operations: []v13.OperationType{
					v13.Update,
				},
				Rule: v13.Rule{
					APIGroups:   []string{"*"},
					APIVersions: []string{"*"},
					Scope:       &namespacedScope,
					Resources:   []string{"virtualmachinemigrationresourcequotas"},
				},
			},
			{
				Operations: []v13.OperationType{
					v13.Update, v13.Delete, v13.Create,
				},
				Rule: v13.Rule{
					APIGroups:   []string{"*"},
					APIVersions: []string{"*"},
					Scope:       &namespacedScope,
					Resources:   []string{"virtualmachinemigrationresourcequotas"},
				},
			},
			{
				Operations: []v13.OperationType{
					v13.Create,
				},
				Rule: v13.Rule{
					APIGroups:   []string{"*"},
					APIVersions: []string{"*"},
					Scope:       &namespacedScope,
					Resources:   []string{"pods"},
				},
			},
		},
		ClientConfig: v13.WebhookClientConfig{
			Service: &v13.ServiceReference{
				Namespace: "default",
				Name:      "example-webhook",
				Path:      &path,
				Port:      &port,
			},
			CABundle: caBundle,
		},
	}
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
			return &rq
		}
	}
	return nil
}
