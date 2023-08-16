package vmmrq_controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/quota/v1/evaluator/core"
	"k8s.io/utils/clock"
	v12 "kubevirt.io/api/core/v1"
	"kubevirt.io/managed-tenant-quota/staging/src/kubevirt.io/managed-tenant-quota-api/pkg/apis/core/v1alpha1"
	"kubevirt.io/managed-tenant-quota/tests"
)

var _ = Describe("Test validation of mtq controller", func() {
	ns := "test-namespace"
	getFakeRQInformer := func(rqListInNS *v1.ResourceQuotaList, ns string) cache.SharedIndexInformer {
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{},
			&v1.ResourceQuota{},
			0, // resync period
			cache.Indexers{
				cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
			},
		)

		indexer := informer.GetIndexer()

		// Add the ResourceQuota object to the indexer
		for _, rq := range rqListInNS.Items {
			err := indexer.Add(rq.DeepCopy())
			Expect(err).ToNot(HaveOccurred())
		}
		return informer
	}

	It("Test that getRQListToRestore func restore only rqs that been modified and are no longer blocking", func() {
		currVmmrq := &v1alpha1.VirtualMachineMigrationResourceQuota{
			Status: v1alpha1.VirtualMachineMigrationResourceQuotaStatus{
				OriginalBlockingResourceQuotas: []v1alpha1.ResourceQuotaNameAndSpec{
					{
						Name: "rq1",
						Spec: v1.ResourceQuotaSpec{
							Hard: v1.ResourceList{
								v1.ResourceCPU:    *resource.NewQuantity(1, resource.DecimalSI),
								v1.ResourceMemory: *resource.NewQuantity(2, resource.BinarySI),
							},
						},
					},
					{
						Name: "rq2",
						Spec: v1.ResourceQuotaSpec{
							Hard: v1.ResourceList{
								v1.ResourceCPU:    *resource.NewQuantity(3, resource.DecimalSI),
								v1.ResourceMemory: *resource.NewQuantity(4, resource.BinarySI),
							},
						},
					},
				},
			},
		}

		prevVmmrq := &v1alpha1.VirtualMachineMigrationResourceQuota{
			Status: v1alpha1.VirtualMachineMigrationResourceQuotaStatus{
				OriginalBlockingResourceQuotas: []v1alpha1.ResourceQuotaNameAndSpec{
					{
						Name: "rq1",
						Spec: v1.ResourceQuotaSpec{
							Hard: v1.ResourceList{
								v1.ResourceCPU:    *resource.NewQuantity(1, resource.DecimalSI),
								v1.ResourceMemory: *resource.NewQuantity(2, resource.BinarySI),
							},
						},
					},
					{
						Name: "rq3",
						Spec: v1.ResourceQuotaSpec{
							Hard: v1.ResourceList{
								v1.ResourceCPU:    *resource.NewQuantity(5, resource.DecimalSI),
								v1.ResourceMemory: *resource.NewQuantity(6, resource.BinarySI),
							},
						},
					},
				},
			},
		}

		ctrl := &VmmrqController{}
		rqList := ctrl.getRQListToRestore(currVmmrq, prevVmmrq)

		Expect(rqList).To(HaveLen(1))
		Expect(rqList[0].Name).To(Equal("rq3"))
		Expect(prevVmmrq.Status.OriginalBlockingResourceQuotas[1].Spec).To(Equal(rqList[0].Spec))
	})

	It("Test GetAllBlockingRQsInNS func considered all rqs without dups", func() {
		vmmrq := &v1alpha1.VirtualMachineMigrationResourceQuota{
			Status: v1alpha1.VirtualMachineMigrationResourceQuotaStatus{
				MigrationsToBlockingResourceQuotas: map[string][]string{
					"migration1": {"rq1", "rq2"},
					"migration2": {"rq2", "rq3"},
				},
				OriginalBlockingResourceQuotas: []v1alpha1.ResourceQuotaNameAndSpec{
					{
						Name: "rq1",
						Spec: v1.ResourceQuotaSpec{
							Hard: v1.ResourceList{
								v1.ResourceCPU:    *resource.NewQuantity(1, resource.DecimalSI),
								v1.ResourceMemory: *resource.NewQuantity(2, resource.BinarySI),
							},
						},
					},
					{
						Name: "rq2",
						Spec: v1.ResourceQuotaSpec{
							Hard: v1.ResourceList{
								v1.ResourceCPU:    *resource.NewQuantity(3, resource.DecimalSI),
								v1.ResourceMemory: *resource.NewQuantity(4, resource.BinarySI),
							},
						},
					},
				},
			},
		}

		rqListInNS := &v1.ResourceQuotaList{
			Items: []v1.ResourceQuota{
				*tests.NewQuotaBuilder().
					WithName("rq1").
					WithNamespace(ns).
					WithResource(v1.ResourceCPU, *resource.NewQuantity(5, resource.DecimalSI)).
					WithResource(v1.ResourceMemory, *resource.NewQuantity(6, resource.BinarySI)).
					Build(),
				*tests.NewQuotaBuilder().
					WithName("rq2").
					WithNamespace(ns).
					WithResource(v1.ResourceCPU, *resource.NewQuantity(5, resource.DecimalSI)).
					WithResource(v1.ResourceMemory, *resource.NewQuantity(6, resource.BinarySI)).
					Build(),
				*tests.NewQuotaBuilder().
					WithName("rq3").
					WithNamespace(ns).
					WithResource(v1.ResourceCPU, *resource.NewQuantity(5, resource.DecimalSI)).
					WithResource(v1.ResourceMemory, *resource.NewQuantity(6, resource.BinarySI)).
					Build(),
			},
		}

		ctrl := &VmmrqController{
			resourceQuotaInformer: getFakeRQInformer(rqListInNS, ns),
		}

		rqList, err := ctrl.getAllBlockingRQsInNS(vmmrq, ns)
		Expect(err).ToNot(HaveOccurred())
		Expect(rqList).To(HaveLen(3))
		Expect(rqList).To(ContainElements(
			v1alpha1.ResourceQuotaNameAndSpec{
				Name: "rq1",
				Spec: v1.ResourceQuotaSpec{
					Hard: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewQuantity(1, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(2, resource.BinarySI),
					},
				},
			},
			v1alpha1.ResourceQuotaNameAndSpec{
				Name: "rq2",
				Spec: v1.ResourceQuotaSpec{
					Hard: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewQuantity(3, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(4, resource.BinarySI),
					},
				},
			},
			v1alpha1.ResourceQuotaNameAndSpec{
				Name: "rq3",
				Spec: v1.ResourceQuotaSpec{
					Hard: v1.ResourceList{
						v1.ResourceCPU:    *resource.NewQuantity(5, resource.DecimalSI),
						v1.ResourceMemory: *resource.NewQuantity(6, resource.BinarySI),
					},
				},
			},
		))
	})
	Context("Test getCurrBlockingRQInNS func", func() {
		It("Make sure rq1 is still blocking even after it's been modified with more resources and that new rq3 is considered and that non blocking rq2 is not included", func() {
			ctrl := &VmmrqController{
				podEvaluator: core.NewPodEvaluator(nil, clock.RealClock{}),
				recorder:     record.NewFakeRecorder(100),
			}

			vmmrq := &v1alpha1.VirtualMachineMigrationResourceQuota{
				Status: v1alpha1.VirtualMachineMigrationResourceQuotaStatus{
					OriginalBlockingResourceQuotas: []v1alpha1.ResourceQuotaNameAndSpec{
						{
							Name: "rq1",
							Spec: v1.ResourceQuotaSpec{
								Hard: v1.ResourceList{
									v1.ResourceRequestsCPU:    *resource.NewQuantity(1, resource.DecimalSI),
									v1.ResourceRequestsMemory: *resource.NewQuantity(2, resource.BinarySI),
								},
							},
						},
						{
							Name: "rq2",
							Spec: v1.ResourceQuotaSpec{
								Hard: v1.ResourceList{
									v1.ResourceRequestsCPU:    *resource.NewQuantity(3, resource.DecimalSI),
									v1.ResourceRequestsMemory: *resource.NewQuantity(4, resource.BinarySI),
								},
							},
						},
					},
				},
			}
			m := &v12.VirtualMachineInstanceMigration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-migration",
					Namespace: ns,
				},
			}
			podToCreate := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: ns,
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "test-container",
							Image: "nginx",
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceCPU:    *resource.NewQuantity(2, resource.DecimalSI),
									v1.ResourceMemory: *resource.NewQuantity(3, resource.BinarySI),
								},
							},
						},
					},
				},
			}

			resourceListToAdd := v1.ResourceList{
				v1.ResourceRequestsCPU:    *resource.NewQuantity(1, resource.DecimalSI),
				v1.ResourceRequestsMemory: *resource.NewQuantity(2, resource.BinarySI),
			}

			rqListInNS := &v1.ResourceQuotaList{
				Items: []v1.ResourceQuota{
					*tests.NewQuotaBuilder().
						WithName("rq1").
						WithNamespace(ns).
						WithResource(v1.ResourceRequestsCPU, *resource.NewQuantity(2, resource.DecimalSI)).
						WithResource(v1.ResourceRequestsMemory, *resource.NewQuantity(4, resource.BinarySI)).
						WithSyncStatusHard().
						WithZeroUsage().
						Build(),
					*tests.NewQuotaBuilder().
						WithName("rq2").
						WithNamespace(ns).
						WithResource(v1.ResourceRequestsCPU, *resource.NewQuantity(4, resource.DecimalSI)).
						WithResource(v1.ResourceRequestsMemory, *resource.NewQuantity(6, resource.BinarySI)).
						WithSyncStatusHard().
						WithZeroUsage().
						Build(),
					*tests.NewQuotaBuilder().
						WithName("rq3").
						WithNamespace(ns).
						WithResource(v1.ResourceRequestsCPU, *resource.NewQuantity(1, resource.DecimalSI)).
						WithResource(v1.ResourceRequestsMemory, *resource.NewQuantity(2, resource.BinarySI)).
						WithSyncStatusHard().
						WithZeroUsage().
						Build(),
				},
			}

			ctrl.resourceQuotaInformer = getFakeRQInformer(rqListInNS, ns)

			currRQListItems, err := ctrl.getCurrBlockingRQInNS(vmmrq, m, podToCreate, resourceListToAdd)
			Expect(err).ToNot(HaveOccurred())
			Expect(currRQListItems).To(HaveLen(2))
			Expect(currRQListItems).To(ContainElements("rq1", "rq3"))
		})

		It("Make sure that we don't include rqs that cannot be unblocked because of lake of resources", func() {
			ctrl := &VmmrqController{
				podEvaluator: core.NewPodEvaluator(nil, clock.RealClock{}),
				recorder:     record.NewFakeRecorder(100),
			}

			vmmrq := &v1alpha1.VirtualMachineMigrationResourceQuota{
				Status: v1alpha1.VirtualMachineMigrationResourceQuotaStatus{
					OriginalBlockingResourceQuotas: []v1alpha1.ResourceQuotaNameAndSpec{
						{},
					},
				},
			}
			m := &v12.VirtualMachineInstanceMigration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-migration",
					Namespace: ns,
				},
			}
			podToCreate := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: ns,
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "test-container",
							Image: "nginx",
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceCPU:    *resource.NewQuantity(4, resource.DecimalSI),
									v1.ResourceMemory: *resource.NewQuantity(4, resource.BinarySI),
								},
							},
						},
					},
				},
			}

			resourceListToAdd := v1.ResourceList{
				v1.ResourceRequestsCPU:    *resource.NewQuantity(1, resource.DecimalSI),
				v1.ResourceRequestsMemory: *resource.NewQuantity(1, resource.BinarySI),
			}

			rqListInNS := &v1.ResourceQuotaList{
				Items: []v1.ResourceQuota{
					*tests.NewQuotaBuilder().
						WithName("rq1").
						WithNamespace(ns).
						WithResource(v1.ResourceRequestsCPU, *resource.NewQuantity(1, resource.DecimalSI)).
						WithResource(v1.ResourceRequestsMemory, *resource.NewQuantity(1, resource.BinarySI)).
						WithSyncStatusHard().
						WithZeroUsage().
						Build(),
				},
			}

			ctrl.resourceQuotaInformer = getFakeRQInformer(rqListInNS, ns)

			currRQListItems, err := ctrl.getCurrBlockingRQInNS(vmmrq, m, podToCreate, resourceListToAdd)
			Expect(err).ToNot(HaveOccurred())
			Expect(currRQListItems).To(BeEmpty())
		})
	})
})
