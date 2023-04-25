package mtq_controller

import (
	"context"
	"fmt"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sadmission "k8s.io/apiserver/pkg/admission"
	quotaplugin "k8s.io/apiserver/pkg/admission/plugin/resourcequota"
	"k8s.io/apiserver/pkg/admission/plugin/resourcequota/apis/resourcequota"
	v12 "k8s.io/apiserver/pkg/quota/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/quota/v1/evaluator/core"
	"k8s.io/utils/clock"
	v1alpha1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
	"kubevirt.io/client-go/log"
	corev1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	v1alpha12 "kubevirt.io/managed-tenant-quota/pkg/apis/core/v1alpha1"
	util "kubevirt.io/managed-tenant-quota/pkg/util"
	"os"
	"reflect"
	"strings"
	"time"
)

const (
	unknownTypeErrFmt                                                                                            = "Managed Quota controller expected object of type %s but found object of unknown type"
	VirtualMachineInstanceMigrationRejectedByResourceQuota v1alpha1.VirtualMachineInstanceMigrationConditionType = "migrationRejectedByResourceQuota"
)

type ManagedQuotaController struct {
	podInformer                                  cache.SharedIndexInformer
	migrationInformer                            cache.SharedIndexInformer
	resourceQuotaInformer                        cache.SharedIndexInformer
	virtualMachineMigrationResourceQuotaInformer cache.SharedIndexInformer
	vmiInformer                                  cache.SharedIndexInformer
	migrationQueue                               workqueue.RateLimitingInterface
	vmmrqStatusUpdater                           *util.VMMRQStatusUpdater
	virtCli                                      kubecli.KubevirtClient
}

func NewManagedQuotaController(VMMRQStatusUpdater *util.VMMRQStatusUpdater, cli kubecli.KubevirtClient, migrationInformer cache.SharedIndexInformer, podInformer cache.SharedIndexInformer, resourceQuotaInformer cache.SharedIndexInformer, virtualMachineMigrationResourceQuotaInformer cache.SharedIndexInformer, vmiInformer cache.SharedIndexInformer) *ManagedQuotaController {
	ctrl := ManagedQuotaController{
		virtCli:               cli,
		migrationInformer:     migrationInformer,
		podInformer:           podInformer,
		resourceQuotaInformer: resourceQuotaInformer,
		virtualMachineMigrationResourceQuotaInformer: virtualMachineMigrationResourceQuotaInformer,
		vmiInformer:        vmiInformer,
		migrationQueue:     workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "migration-queue"),
		vmmrqStatusUpdater: VMMRQStatusUpdater,
	}

	_, err := ctrl.migrationInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: ctrl.deleteMigration,
		UpdateFunc: ctrl.updateMigration,
	})

	if err != nil {
		return nil
	}

	return &ctrl
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
	var migration *v1alpha1.VirtualMachineInstanceMigration
	var vmi *v1alpha1.VirtualMachineInstance

	if migrationExists {
		migration = migrationObj.(*v1alpha1.VirtualMachineInstanceMigration)
		vmiObj, vmiExists, err := ctrl.vmiInformer.GetStore().GetByKey(fmt.Sprintf("%s/%s", migration.Namespace, migration.Spec.VMIName))
		if err != nil {
			return err
		} else if !vmiExists { //vmi for migration doesn't exist
			return fmt.Errorf("VMI doesn't exist for migration")
		}
		vmi = vmiObj.(*v1alpha1.VirtualMachineInstance)
	}

	vmmrqObjsList, err := ctrl.virtualMachineMigrationResourceQuotaInformer.GetIndexer().ByIndex(cache.NamespaceIndex, migration.Namespace)
	if err != nil {
		return err
	}
	if len(vmmrqObjsList) != 1 {
		return fmt.Errorf("there should be 1 virtualMachineMigrationResourceQuota in %v namespace", migration.Namespace)
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

	if !isBlockedMigration {
		rqListDeletedFromMap := vmmrq.Status.MigrationsToBlockingResourceQuotas[migration.Name]
		delete(vmmrq.Status.MigrationsToBlockingResourceQuotas, migration.Name)
		blockingRQsList := getAllBlockingRQsInNS(vmmrq.Status.MigrationsToBlockingResourceQuotas)
		nonBlockingRQs := getNonBlockingRQ(rqListDeletedFromMap, *blockingRQsList) //check which RQs don't block any other migration
		vmmrq.Status.OriginalBlockingResourceQuotas = removeRQsFromBlockingRQList(vmmrq.Status.OriginalBlockingResourceQuotas, nonBlockingRQs)
		log.Log.Reason(err).Infof(fmt.Sprintf("OriginalBlockingResourceQuotas  map now : %+v", vmmrq.Status.OriginalBlockingResourceQuotas))
	} else if isBlockedMigration {
		sourcePod, err := CurrentVMIPod(vmi, ctrl.podInformer)
		if err != nil {
			return err
		}
		currBlockingRQList, err := ctrl.getCurrBlockingRQInNS(migration.Namespace, sourcePod, vmmrq.Spec.AdditionalMigrationResources)
		if err != nil {
			return err
		}

		vmmrq.Status.MigrationsToBlockingResourceQuotas[migration.Name] = currBlockingRQList
		oldBlockingRQList := vmmrq.Status.OriginalBlockingResourceQuotas
		allBlockingRQsInNS, err := ctrl.getAllBlockingRQsInNS(vmmrq.Status.MigrationsToBlockingResourceQuotas, migration.Namespace)
		finalBlockingRQList := newBlockingRQs(oldBlockingRQList, allBlockingRQsInNS)
		vmmrq.Status.OriginalBlockingResourceQuotas = finalBlockingRQList
	}

	lockNS := shouldLockNS(vmmrq)
	namespaceLocked := namespaceLocked(migration.Namespace)
	if lockNS && !namespaceLocked {
		//TODO: lock namespace
	}

	rqListToRestore := ctrl.getRQListToRestore(vmmrq, prevVmmrq)
	if len(rqListToRestore) > 0 {
		log.Log.Reason(err).Infof(fmt.Sprintf("OriginalBlockingResourceQuotas  restoring : %+v", rqListToRestore))
	}
	err = ctrl.restoreOriginalRQs(rqListToRestore, prevVmmrq.Status.OriginalBlockingResourceQuotas, migration.Namespace)
	if err != nil {
		return err
	}
	if shouldUpdateVmmrq(vmmrq, prevVmmrq) {
		err := ctrl.vmmrqStatusUpdater.UpdateStatus(vmmrq)
		if err != nil {
			return err
		}
	}

	err = ctrl.addResourcesToRQs(vmmrq.Status.OriginalBlockingResourceQuotas, &vmmrq.Spec.AdditionalMigrationResources, migration.Namespace)
	if err != nil {
		return err
	}
	if !lockNS && namespaceLocked {
		//TODO: unlock namespace  restore memory to virtualMachineMigrationResourceQuota if not done yet
	}

	return nil
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

	virtualMachineMigrationResourceQuotaInformer, err := util.GetVirtualMachineMigrationResourceQuota(mtqCli)
	if err != nil {
		os.Exit(1)
	}
	go migrationInformer.Run(stop)
	go podInformer.Run(stop)
	go resourceQuotaInformer.Run(stop)
	go virtualMachineMigrationResourceQuotaInformer.Run(stop)
	go vmiInformer.Run(stop)

	VMMRQStatusUpdater := util.NewVMMRQStatusUpdater(virtCli, mtqCli)

	ctrl := NewManagedQuotaController(VMMRQStatusUpdater, virtCli, migrationInformer, podInformer, resourceQuotaInformer, virtualMachineMigrationResourceQuotaInformer, vmiInformer)

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

func namespaceLocked(ns string) bool {
	//TODO: implement
	return true
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

func (ctrl *ManagedQuotaController) getCurrBlockingRQInNS(ns string, podToCreate *v1.Pod, listToAdd v1.ResourceList) ([]string, error) {
	currRQListObj, err := ctrl.resourceQuotaInformer.GetIndexer().ByIndex(cache.NamespaceIndex, ns)
	if err != nil {
		return nil, err
	}
	var currRQListItems []string

	podToCreateAttr := k8sadmission.NewAttributesRecord(podToCreate, nil, apiextensions.Kind("Pod").WithVersion("version"), podToCreate.Namespace, podToCreate.Name, corev1beta1.Resource("pods").WithVersion("version"), "", k8sadmission.Create, &metav1.CreateOptions{}, false, nil)
	podEvaluator := core.NewPodEvaluator(nil, clock.RealClock{})
	limitedResources := []resourcequota.LimitedResource{
		{
			Resource:      "pods",
			MatchContains: []string{"cpu", "memory"}, //TODO: check if more resources should be considered
		},
	}
	for _, obj := range currRQListObj {
		resourceQuota := obj.(*v1.ResourceQuota)
		resourceQuotaCopy := resourceQuota.DeepCopy()
		//Checking if the resourceQuota is blocking us
		_, errWithCurrRQ := admitPodToQuota(resourceQuotaCopy, podToCreateAttr, podEvaluator, limitedResources)
		//checking if the additional resources in vmmRQ will unblock us
		rqAfterResourcesAddition := addResourcesToRQ(*resourceQuotaCopy, &listToAdd)
		_, errWithMofifiedRQ := admitPodToQuota(rqAfterResourcesAddition, podToCreateAttr, podEvaluator, limitedResources)
		if errWithCurrRQ != nil && strings.Contains(errWithCurrRQ.Error(), "exceeded quota") && errWithMofifiedRQ == nil { //TODO:think about this logic so we won't be stock or let the user know
			currRQListItems = append(currRQListItems, (*resourceQuota).Name)
		} else if errWithMofifiedRQ != nil && strings.Contains(errWithMofifiedRQ.Error(), "exceeded quota") {
			return nil, errWithMofifiedRQ
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