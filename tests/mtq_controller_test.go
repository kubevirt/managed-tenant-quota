package tests

import (
	"context"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
	"kubevirt.io/kubevirt/pkg/controller"
	"kubevirt.io/kubevirt/tests"
	"kubevirt.io/kubevirt/tests/libvmi"
	"kubevirt.io/kubevirt/tests/testsuite"
	mtq_controller "kubevirt.io/managed-tenant-quota/pkg/mtq-controller"
	"kubevirt.io/managed-tenant-quota/tests/events"
	"kubevirt.io/managed-tenant-quota/tests/framework"
	"strconv"
	"time"
)

var _ = Describe("Blocked migration", func() {
	f := framework.NewFramework("fake-test")

	Context("General blocked migration cases in a single namespace", func() {
		DescribeTable("single blocked migration with all types of resources requests and limits", func(overcommitmentResource v1.ResourceName) {
			opts := []libvmi.Option{
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(kv1.DefaultPodNetwork()),
				libvmi.WithNamespace(f.Namespace.GetName()),
			}

			switch overcommitmentResource {
			case v1.ResourceLimitsMemory:
				opts = append(opts, libvmi.WithLimitMemory("512Mi"))
			case v1.ResourceLimitsCPU:
				opts = append(opts, libvmi.WithLimitCPU("2"))
			}

			vmi := libvmi.NewAlpine(opts...)
			vmi = tests.RunVMIAndExpectLaunch(vmi, 30)
			vmiPod := tests.GetRunningPodByVirtualMachineInstance(vmi, testsuite.GetTestNamespace(vmi))
			podResources, err := getCurrLauncherUsage(vmiPod)
			Expect(err).To(Not(HaveOccurred()))
			resourceQuota := NewQuotaBuilder().
				WithNamespace(f.Namespace.GetName()).
				WithName("test-quota").
				WithResource(overcommitmentResource, podResources[overcommitmentResource]).
				Build()

			vmmrq := NewVmmrqBuilder().
				WithNamespace(f.Namespace.GetName()).
				WithName("test-vmmrq").
				WithResource(overcommitmentResource, podResources[overcommitmentResource]).
				Build()
			_, err = f.K8sClient.CoreV1().ResourceQuotas(resourceQuota.Namespace).Create(context.TODO(), resourceQuota, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			By("Starting the Migration")
			migration := tests.NewRandomMigration(vmi.Name, vmi.Namespace)
			migration = tests.RunMigration(f.VirtClient, migration)
			Eventually(func() error {
				if migrationHasRejectedByResourceQuotaCond(f.VirtClient, migration) {
					return nil
				}
				return fmt.Errorf("migration is in the phase: %s", migration.Status.Phase)
			}, 20*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("migration should be blocked after %d s", 20*time.Second))

			_, err = f.MtqClient.MtqV1alpha1().VirtualMachineMigrationResourceQuotas(vmmrq.Namespace).Create(context.TODO(), vmmrq, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			Eventually(func() error {
				if !migrationHasRejectedByResourceQuotaCond(f.VirtClient, migration) {
					return nil
				}
				return fmt.Errorf("migration is still blocked in the phase: %s", migration.Status.Phase)
			}, 20*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("migration be unlocked after %d s", 20*time.Second))
		},
			Entry("vmi memory overcommitment", v1.ResourceMemory),
			Entry("vmi memory requirement overcommitment", v1.ResourceRequestsMemory),
			Entry("vmi memory limit overcommitment", v1.ResourceLimitsMemory),
			Entry("vmi cpu overcommitment", v1.ResourceCPU),
			Entry("vmi cpu requirement overcommitment", v1.ResourceRequestsCPU),
			Entry("vmi cpu limit overcommitment", v1.ResourceLimitsCPU),
			Entry("vmi ephemeralStorage overcommitment", v1.ResourceEphemeralStorage),
			Entry("vmi ephemeralStorage request overcommitment", v1.ResourceRequestsEphemeralStorage),
		)

		It("single blocked migration with several restrictions ", func() {
			vmi := libvmi.NewAlpine(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(kv1.DefaultPodNetwork()),
				libvmi.WithNamespace(f.Namespace.GetName()),
				libvmi.WithLimitCPU("2"),
			)
			vmi = tests.RunVMIAndExpectLaunch(vmi, 30)
			vmiPod := tests.GetRunningPodByVirtualMachineInstance(vmi, testsuite.GetTestNamespace(vmi))
			podResources, err := getCurrLauncherUsage(vmiPod)
			Expect(err).To(Not(HaveOccurred()))
			resourceQuota := NewQuotaBuilder().
				WithNamespace(f.Namespace.GetName()).
				WithName("test-quota").
				WithResource(v1.ResourceLimitsCPU, podResources[v1.ResourceLimitsCPU]).
				WithResource(v1.ResourceRequestsMemory, podResources[v1.ResourceRequestsMemory]).
				WithResource(v1.ResourceRequestsCPU, podResources[v1.ResourceRequestsCPU]).
				Build()

			vmmrq := NewVmmrqBuilder().
				WithNamespace(f.Namespace.GetName()).
				WithName("test-vmmrq").
				WithResource(v1.ResourceLimitsCPU, podResources[v1.ResourceLimitsCPU]).
				WithResource(v1.ResourceRequestsMemory, podResources[v1.ResourceRequestsMemory]).
				WithResource(v1.ResourceRequestsCPU, podResources[v1.ResourceRequestsCPU]).
				Build()
			_, err = f.K8sClient.CoreV1().ResourceQuotas(resourceQuota.Namespace).Create(context.TODO(), resourceQuota, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			By("Starting the Migration")
			migration := tests.NewRandomMigration(vmi.Name, vmi.Namespace)
			migration = tests.RunMigration(f.VirtClient, migration)
			Eventually(func() error {
				if migrationHasRejectedByResourceQuotaCond(f.VirtClient, migration) {
					return nil
				}
				return fmt.Errorf("migration is in the phase: %s", migration.Status.Phase)
			}, 60*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("migration should be blocked after %d s", 20*time.Second))

			_, err = f.MtqClient.MtqV1alpha1().VirtualMachineMigrationResourceQuotas(vmmrq.Namespace).Create(context.TODO(), vmmrq, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			Eventually(func() error {
				if !migrationHasRejectedByResourceQuotaCond(f.VirtClient, migration) {
					return nil
				}
				return fmt.Errorf("migration is still blocked in the phase: %s", migration.Status.Phase)
			}, 20*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("migration be unlocked after %d s", 20*time.Second))

		})

		It("single blocked migration with several restricting resourceQuotas ", func() {
			vmi := libvmi.NewAlpine(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(kv1.DefaultPodNetwork()),
				libvmi.WithNamespace(f.Namespace.GetName()),
				libvmi.WithLimitCPU("2"),
			)
			vmi = tests.RunVMIAndExpectLaunch(vmi, 30)
			vmiPod := tests.GetRunningPodByVirtualMachineInstance(vmi, testsuite.GetTestNamespace(vmi))
			podResources, err := getCurrLauncherUsage(vmiPod)
			Expect(err).To(Not(HaveOccurred()))
			resources := []v1.ResourceName{v1.ResourceLimitsCPU, v1.ResourceRequestsMemory, v1.ResourceRequestsCPU}
			for i, resource := range resources {
				rq := NewQuotaBuilder().
					WithNamespace(f.Namespace.GetName()).
					WithName("test-quota"+strconv.Itoa(i)).
					WithResource(resource, podResources[resource]).
					Build()
				_, err = f.K8sClient.CoreV1().ResourceQuotas(rq.Namespace).Create(context.TODO(), rq, metav1.CreateOptions{})
				Expect(err).To(Not(HaveOccurred()))
			}

			vmmrq := NewVmmrqBuilder().
				WithNamespace(f.Namespace.GetName()).
				WithName("test-vmmrq").
				WithResource(v1.ResourceLimitsCPU, podResources[v1.ResourceLimitsCPU]).
				WithResource(v1.ResourceRequestsMemory, podResources[v1.ResourceRequestsMemory]).
				WithResource(v1.ResourceRequestsCPU, podResources[v1.ResourceRequestsCPU]).
				Build()

			By("Starting the Migration")
			migration := tests.NewRandomMigration(vmi.Name, vmi.Namespace)
			migration = tests.RunMigration(f.VirtClient, migration)
			Eventually(func() error {
				if migrationHasRejectedByResourceQuotaCond(f.VirtClient, migration) {
					return nil
				}
				return fmt.Errorf("migration is in the phase: %s", migration.Status.Phase)
			}, 60*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("migration should be blocked after %d s", 20*time.Second))

			_, err = f.MtqClient.MtqV1alpha1().VirtualMachineMigrationResourceQuotas(vmmrq.Namespace).Create(context.TODO(), vmmrq, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			Eventually(func() error {
				if !migrationHasRejectedByResourceQuotaCond(f.VirtClient, migration) {
					return nil
				}
				return fmt.Errorf("migration is still blocked in the phase: %s", migration.Status.Phase)
			}, 20*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("migration be unlocked after %d s", 20*time.Second))

			Eventually(func() error {
				for i, resource := range resources {
					rq, err := f.K8sClient.CoreV1().ResourceQuotas(f.Namespace.GetName()).Get(context.TODO(), "test-quota"+strconv.Itoa(i), metav1.GetOptions{})
					if err != nil {
						return err
					}
					quantity := rq.Spec.Hard[resource]
					if quantity.Cmp(podResources[resource]) != 0 {
						return fmt.Errorf("rq should be restored by the mtq-controller after migration is finished")
					}
				}
				return nil
			}, 40*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("Eventually resourceQuotas should be restore to original value after %d s", 40*time.Second))

		})
	})

	Context("mtq-controller events tests", func() {
		BeforeEach(func() {
			events.DeleteEvents(f.Namespace.GetName(), v1.Pod{}.Kind, v1.EventTypeWarning, mtq_controller.FailedToReleaseMigrationReason)
		})
		DescribeTable("Check event creation when a migration cannot be released", func(releaseMigration bool) {
			vmi := libvmi.NewAlpine(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(kv1.DefaultPodNetwork()),
				libvmi.WithNamespace(f.Namespace.GetName()),
			)
			vmi = tests.RunVMIAndExpectLaunch(vmi, 30)
			vmiPod := tests.GetRunningPodByVirtualMachineInstance(vmi, testsuite.GetTestNamespace(vmi))
			podResources, err := getCurrLauncherUsage(vmiPod)
			Expect(err).To(Not(HaveOccurred()))
			resourceQuota := NewQuotaBuilder().
				WithNamespace(f.Namespace.GetName()).
				WithName("test-quota").
				WithResource(v1.ResourceRequestsMemory, podResources[v1.ResourceRequestsMemory]).
				Build()

			lakedQuantity := podResources[v1.ResourceRequestsMemory]
			if !releaseMigration {
				lakedQuantity.Sub(resource.MustParse("1m"))
			}
			vmmrq := NewVmmrqBuilder().
				WithNamespace(f.Namespace.GetName()).
				WithName("test-vmmrq").
				WithResource(v1.ResourceRequestsMemory, lakedQuantity).
				Build()
			_, err = f.K8sClient.CoreV1().ResourceQuotas(resourceQuota.Namespace).Create(context.TODO(), resourceQuota, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))

			By("Starting the Migration")
			migration := tests.NewRandomMigration(vmi.Name, vmi.Namespace)
			migration = tests.RunMigration(f.VirtClient, migration)
			Eventually(func() error {
				if migrationHasRejectedByResourceQuotaCond(f.VirtClient, migration) {
					return nil
				}
				return fmt.Errorf("migration is in the phase: %s", migration.Status.Phase)
			}, 60*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("migration should be blocked after %d s", 20*time.Second))

			_, err = f.MtqClient.MtqV1alpha1().VirtualMachineMigrationResourceQuotas(vmmrq.Namespace).Create(context.TODO(), vmmrq, metav1.CreateOptions{})
			Expect(err).To(Not(HaveOccurred()))
			if !releaseMigration {
				events.ExpectEvent(f.Namespace.GetName(), v1.Pod{}.Kind, v1.EventTypeWarning, mtq_controller.FailedToReleaseMigrationReason)
			} else {
				events.ExpectNoEvent(f.Namespace.GetName(), v1.Pod{}.Kind, v1.EventTypeWarning, mtq_controller.FailedToReleaseMigrationReason)
			}
		},
			Entry("blocked migration, event should be created", false),
			Entry("unblocked migration, event should not be created", true),
		)
	})

	Context("Multiple namespaces e2e test", func() {
		var ns1 *v1.Namespace
		var ns2 *v1.Namespace
		var err error
		BeforeEach(func() {
			ns1, err = f.CreateNamespace("test1", map[string]string{
				framework.NsPrefixLabel: "test1",
			})
			Expect(err).ToNot(HaveOccurred())
			ns2, err = f.CreateNamespace("test2", map[string]string{
				framework.NsPrefixLabel: "test2",
			})
			Expect(err).ToNot(HaveOccurred())
			f.AddNamespaceToDelete(ns1)
			f.AddNamespaceToDelete(ns2)
		})

		It("migrations in different namespaces with multiple blocking RQs", func() {
			vmiMap := make(map[string]*kv1.VirtualMachineInstance)
			migrationMap := make(map[string]*kv1.VirtualMachineInstanceMigration)
			opts := []libvmi.Option{
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(kv1.DefaultPodNetwork()),
				libvmi.WithLimitCPU("2"),
			}
			namespaces := []string{ns1.GetName(), ns2.GetName(), f.Namespace.GetName()}

			for _, ns := range namespaces {
				opts = append(opts, libvmi.WithNamespace(ns))
				vmiMap[ns] = libvmi.NewAlpine(opts...)
				vmiMap[ns] = tests.RunVMIAndExpectLaunch(vmiMap[ns], 30)
				Expect(err).To(Not(HaveOccurred()))
			}

			vmiPod := tests.GetRunningPodByVirtualMachineInstance(vmiMap[ns1.GetName()], vmiMap[ns1.GetName()].Namespace)
			podResources, err := getCurrLauncherUsage(vmiPod) //all pods require same resources
			resources := []v1.ResourceName{v1.ResourceLimitsCPU, v1.ResourceRequestsMemory, v1.ResourceRequestsCPU}
			for i, resource := range resources {
				rq := NewQuotaBuilder().
					WithName("test-quota"+strconv.Itoa(i)).
					WithResource(resource, podResources[resource]).
					Build()
				for _, ns := range namespaces {
					rq.Namespace = ns
					_, err = f.K8sClient.CoreV1().ResourceQuotas(rq.Namespace).Create(context.TODO(), rq, metav1.CreateOptions{})
					Expect(err).To(Not(HaveOccurred()))
				}
			}
			By("Starting the Migrations")
			for _, ns := range namespaces {
				migrationMap[ns] = tests.NewRandomMigration(vmiMap[ns].Name, vmiMap[ns].Namespace)
				migrationMap[ns] = tests.RunMigration(f.VirtClient, migrationMap[ns])
				Eventually(func() error {
					if migrationHasRejectedByResourceQuotaCond(f.VirtClient, migrationMap[ns]) {
						return nil
					}
					return fmt.Errorf("migration %v in ns %v is in the phase: %s", migrationMap[ns].Name, migrationMap[ns].Namespace, migrationMap[ns].Status.Phase)
				}, 60*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("migration should be blocked after %d s", 20*time.Second))
			}

			vmmrq := NewVmmrqBuilder().
				WithName("vmmrq").
				WithResource(v1.ResourceLimitsCPU, podResources[v1.ResourceLimitsCPU]).
				WithResource(v1.ResourceRequestsMemory, podResources[v1.ResourceRequestsMemory]).
				WithResource(v1.ResourceRequestsCPU, podResources[v1.ResourceRequestsCPU]).
				Build()
			for _, ns := range namespaces {
				_, err = f.MtqClient.MtqV1alpha1().VirtualMachineMigrationResourceQuotas(ns).Create(context.TODO(), vmmrq, metav1.CreateOptions{})
				Expect(err).To(Not(HaveOccurred()))
			}

			Eventually(func() error {
				for _, ns := range namespaces {
					if migrationHasRejectedByResourceQuotaCond(f.VirtClient, migrationMap[ns]) {
						return fmt.Errorf("migration %v in ns %v is still blocked in the phase: %s", migrationMap[ns].Name, migrationMap[ns].Namespace, migrationMap[ns].Status.Phase)
					}
				}
				return nil
			}, 40*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("migration be unlocked after %d s", 40*time.Second))

			Eventually(func() error {
				for i, resource := range resources {
					for _, ns := range namespaces {
						rq, err := f.K8sClient.CoreV1().ResourceQuotas(ns).Get(context.TODO(), "test-quota"+strconv.Itoa(i), metav1.GetOptions{})
						if err != nil {
							return err
						}
						quantity := rq.Spec.Hard[resource]
						if quantity.Cmp(podResources[resource]) != 0 {
							return fmt.Errorf("rq should be restored by the mtq-controller after migration is finished")
						}
					}
				}
				return nil
			}, 40*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("Eventually resourceQuotas should be restore to original value after %d s", 40*time.Second))
		})

	})

})

func migrationHasRejectedByResourceQuotaCond(virtClient kubecli.KubevirtClient, migration *kv1.VirtualMachineInstanceMigration) bool {
	conditionManager := controller.NewVirtualMachineInstanceMigrationConditionManager()
	migrationObj, err := virtClient.VirtualMachineInstanceMigration(migration.Namespace).Get(migration.Name, &metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred())
	return conditionManager.HasCondition(migrationObj, kv1.VirtualMachineInstanceMigrationRejectedByResourceQuota)
}
