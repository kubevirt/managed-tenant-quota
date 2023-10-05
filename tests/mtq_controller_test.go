package tests

import (
	"context"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
	"kubevirt.io/kubevirt/pkg/controller"
	"kubevirt.io/kubevirt/tests"
	"kubevirt.io/kubevirt/tests/libvmi"
	"kubevirt.io/kubevirt/tests/testsuite"
	mtq_controller "kubevirt.io/managed-tenant-quota/pkg/mtq-controller/vmmrq-controller"
	"kubevirt.io/managed-tenant-quota/tests/events"
	"kubevirt.io/managed-tenant-quota/tests/framework"
	"strconv"
	"strings"
	"sync"
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
			rq := NewQuotaBuilder().
				WithNamespace(f.Namespace.GetName()).
				WithName("test-quota").
				WithResource(overcommitmentResource, podResources[overcommitmentResource]).
				Build()

			vmmrq := NewVmmrqBuilder().
				WithNamespace(f.Namespace.GetName()).
				WithName("test-vmmrq").
				WithResource(overcommitmentResource, podResources[overcommitmentResource]).
				Build()
			err = createRQWithHardLimitOrRequestAndWaitForRegistration(f.VirtClient, rq)
			Expect(err).ToNot(HaveOccurred())

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
			rq := NewQuotaBuilder().
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
			err = createRQWithHardLimitOrRequestAndWaitForRegistration(f.VirtClient, rq)
			Expect(err).ToNot(HaveOccurred())

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

		It("Check that max parallel migration is being considered", func() {
			nodes, err := f.VirtClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())

			vmiNames := []string{"vmi-1", "vmi-2", "vmi-3", "vmi-4"}
			var vmiList []*kv1.VirtualMachineInstance
			var vmimList []*kv1.VirtualMachineInstanceMigration
			for _, vmiName := range vmiNames {
				vmi := libvmi.NewAlpine(
					libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
					libvmi.WithNetwork(kv1.DefaultPodNetwork()),
					libvmi.WithNamespace(f.Namespace.GetName()),
					libvmi.WithResourceMemory("256Mi"),
					WithNodePreferredAffinityFor(&nodes.Items[0]),
				)
				vmi.SetName(vmiName)

				// Assuming "tests.RunVMIAndExpectLaunch" function launches the VMI and performs expectations
				vmi = tests.RunVMIAndExpectLaunch(vmi, 30)

				// Add the VMI to the VMI list
				vmiList = append(vmiList, vmi)
			}

			vmiPod := tests.GetRunningPodByVirtualMachineInstance(vmiList[0], f.Namespace.GetName())
			podResources, err := getCurrLauncherUsage(vmiPod)
			originalQuantity := podResources[v1.ResourceRequestsMemory]
			multipliedValue := originalQuantity.Value() * int64(len(vmiNames))
			multipliedQuantity := *resource.NewQuantity(int64(multipliedValue), originalQuantity.Format)

			Expect(err).To(Not(HaveOccurred()))
			rq := NewQuotaBuilder().
				WithNamespace(f.Namespace.GetName()).
				WithName("test-quota").
				WithResource(v1.ResourceRequestsMemory, multipliedQuantity).
				Build()

			vmmrq := NewVmmrqBuilder().
				WithNamespace(f.Namespace.GetName()).
				WithName("test-vmmrq").
				WithResource(v1.ResourceRequestsMemory, multipliedQuantity).
				Build()
			err = createRQWithHardLimitOrRequestAndWaitForRegistration(f.VirtClient, rq)
			Expect(err).ToNot(HaveOccurred())

			By("Starting the Migration")
			for _, vmi := range vmiList {
				migration := tests.NewRandomMigration(vmi.Name, vmi.Namespace)
				migration = tests.RunMigration(f.VirtClient, migration)
				Eventually(func() error {
					if migrationHasRejectedByResourceQuotaCond(f.VirtClient, migration) {
						return nil
					}
					return fmt.Errorf("migration is in the phase: %s", migration.Status.Phase)
				}, 60*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("migration should be blocked after %d s", 20*time.Second))
				vmimList = append(vmimList, migration)
			}

			_, err = f.MtqClient.MtqV1alpha1().VirtualMachineMigrationResourceQuotas(vmmrq.Namespace).Create(context.TODO(), vmmrq, metav1.CreateOptions{})

			Consistently(func() error {
				valid, err := validLocking(f.Namespace.GetName(), f.VirtClient)
				if err != nil {
					return err
				}
				if !valid {
					return fmt.Errorf("locking is not valid")
				}
				return nil
			}, 40*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), "locking lasted more than 10s")

			Expect(err).To(Not(HaveOccurred()))
			for _, migration := range vmimList {
				Eventually(func() error {
					if !migrationHasRejectedByResourceQuotaCond(f.VirtClient, migration) {
						return nil
					}
					return fmt.Errorf("migration is still blocked in the phase: %s", migration.Status.Phase)
				}, 200*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), fmt.Sprintf("migration be unlocked after %d s", 20*time.Second))
			}

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
				err = createRQWithHardLimitOrRequestAndWaitForRegistration(f.VirtClient, rq)
				Expect(err).ToNot(HaveOccurred())
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
			rq := NewQuotaBuilder().
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
			err = createRQWithHardLimitOrRequestAndWaitForRegistration(f.VirtClient, rq)
			Expect(err).ToNot(HaveOccurred())

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
			}

			vmiPod := tests.GetRunningPodByVirtualMachineInstance(vmiMap[ns1.GetName()], vmiMap[ns1.GetName()].Namespace)
			podResources, err := getCurrLauncherUsage(vmiPod) //all pods require same resources
			Expect(err).To(Not(HaveOccurred()))
			resources := []v1.ResourceName{v1.ResourceEphemeralStorage, v1.ResourceRequestsMemory, v1.ResourceRequestsCPU}
			for i, resource := range resources {
				rq := NewQuotaBuilder().
					WithName("test-quota"+strconv.Itoa(i)).
					WithResource(resource, podResources[resource]).
					Build()
				for _, ns := range namespaces {
					rq.Namespace = ns
					err = createRQWithHardLimitOrRequestAndWaitForRegistration(f.VirtClient, rq)
					Expect(err).ToNot(HaveOccurred())
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
				WithResource(v1.ResourceEphemeralStorage, podResources[v1.ResourceEphemeralStorage]).
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

	It("migrations with system abuse attempt", func() {
		vmi := libvmi.NewAlpine(
			libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
			libvmi.WithNetwork(kv1.DefaultPodNetwork()),
			libvmi.WithNamespace(f.Namespace.GetName()),
			libvmi.WithLimitCPU("2"),
		)
		vmi = tests.RunVMIAndExpectLaunch(vmi, 30)

		vmiPod := tests.GetRunningPodByVirtualMachineInstance(vmi, vmi.Namespace)
		podResources, err := getCurrLauncherUsage(vmiPod)
		Expect(err).To(Not(HaveOccurred()))
		rq := NewQuotaBuilder().
			WithNamespace(f.Namespace.GetName()).
			WithName("test-quota").
			WithResource(v1.ResourceRequestsMemory, podResources[v1.ResourceRequestsMemory]).
			Build()
		err = createRQWithHardLimitOrRequestAndWaitForRegistration(f.VirtClient, rq)
		Expect(err).ToNot(HaveOccurred())

		// Create a context and a cancel function
		ctx, cancel := context.WithCancel(context.Background())
		// Use a WaitGroup to synchronize goroutines
		var wg sync.WaitGroup
		wg.Add(1)

		go func(ctx context.Context) {
			defer wg.Done()
			// Start the abusing process
			abuseSystem(f.VirtClient, ctx, f.Namespace.GetName())
		}(ctx)
		By("Starting the Migrations")
		vmmrq := NewVmmrqBuilder().
			WithNamespace(f.Namespace.GetName()).
			WithName("test-vmmrq").
			WithResource(v1.ResourceRequestsMemory, podResources[v1.ResourceRequestsMemory]). //enough memory for single migration
			Build()
		_, err = f.MtqClient.MtqV1alpha1().VirtualMachineMigrationResourceQuotas(vmmrq.Namespace).Create(context.TODO(), vmmrq, metav1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		By("Making sure pod doesn't get additional resources in 5 increasements")
		for range []int{1, 2, 3, 4, 5} {
			migration := tests.NewRandomMigration(vmi.Name, vmi.Namespace)
			migration = tests.RunMigrationAndExpectCompletion(f.VirtClient, migration, 240)
		}
		// Call the cancel function to stop the abusing process
		cancel()

		// Wait for the goroutine to exit
		wg.Wait()

	})

	It("should work with LimitRange", func() {
		limitRange := &v1.LimitRange{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "limit.range.test",
				Namespace: f.Namespace.GetName(),
			},
			Spec: v1.LimitRangeSpec{
				Limits: []v1.LimitRangeItem{
					{
						Default: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("200m"),
							v1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Type: v1.LimitTypeContainer,
					},
				},
			},
		}
		_, err := f.VirtClient.CoreV1().LimitRanges(limitRange.Namespace).Create(context.TODO(), limitRange, metav1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))
		vmi := libvmi.NewAlpine(
			libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
			libvmi.WithNetwork(kv1.DefaultPodNetwork()),
			libvmi.WithNamespace(f.Namespace.GetName()),
			libvmi.WithResourceCPU("100m"),
			libvmi.WithResourceMemory("512Mi"),
		)
		vmi = tests.RunVMIAndExpectLaunch(vmi, 30)

		vmiPod := tests.GetRunningPodByVirtualMachineInstance(vmi, vmi.Namespace)
		podResources, err := getCurrLauncherUsage(vmiPod)
		Expect(err).To(Not(HaveOccurred()))
		rq := NewQuotaBuilder().
			WithNamespace(f.Namespace.GetName()).
			WithName("test-quota").
			WithResource(v1.ResourceRequestsMemory, podResources[v1.ResourceRequestsMemory]).
			WithResource(v1.ResourceRequestsCPU, podResources[v1.ResourceRequestsCPU]).
			WithResource(v1.ResourceLimitsMemory, resource.MustParse("4Gi")).
			WithResource(v1.ResourceLimitsCPU, resource.MustParse("1")).
			Build()
		err = createRQWithHardLimitOrRequestAndWaitForRegistration(f.VirtClient, rq)
		Expect(err).ToNot(HaveOccurred())

		By("Starting the Migrations")
		vmmrq := NewVmmrqBuilder().
			WithNamespace(f.Namespace.GetName()).
			WithName("test-vmmrq").
			WithResource(v1.ResourceRequestsMemory, podResources[v1.ResourceRequestsMemory]).
			WithResource(v1.ResourceRequestsCPU, podResources[v1.ResourceRequestsCPU]).
			WithResource(v1.ResourceLimitsMemory, resource.MustParse("4Gi")).
			WithResource(v1.ResourceLimitsCPU, resource.MustParse("1")).
			Build()
		_, err = f.MtqClient.MtqV1alpha1().VirtualMachineMigrationResourceQuotas(vmmrq.Namespace).Create(context.TODO(), vmmrq, metav1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		migration := tests.NewRandomMigration(vmi.Name, vmi.Namespace)
		migration = tests.RunMigrationAndExpectCompletion(f.VirtClient, migration, 240)

	})

})

func migrationHasRejectedByResourceQuotaCond(virtClient kubecli.KubevirtClient, migration *kv1.VirtualMachineInstanceMigration) bool {
	conditionManager := controller.NewVirtualMachineInstanceMigrationConditionManager()
	migrationObj, err := virtClient.VirtualMachineInstanceMigration(migration.Namespace).Get(migration.Name, &metav1.GetOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return conditionManager.HasCondition(migrationObj, kv1.VirtualMachineInstanceMigrationRejectedByResourceQuota)
}

func abuseSystem(virtClient kubecli.KubevirtClient, ctx context.Context, testNs string) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "fake-pod-",
			Namespace:    testNs,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "nginx",
					Image: "your-image", // Specify the container image to use
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceMemory: resource.MustParse("50Mi"),
						},
					},
					SecurityContext: &v1.SecurityContext{
						AllowPrivilegeEscalation: resourcemerge.BoolPtr(false),
						RunAsNonRoot:             resourcemerge.BoolPtr(true),
						Capabilities: &v1.Capabilities{
							Drop: []v1.Capability{"ALL"},
						},
						SeccompProfile: &v1.SeccompProfile{
							Type: v1.SeccompProfileTypeRuntimeDefault,
						},
					},
				},
			},
		},
	}

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Abusing process canceled")
			return
		default:
			// Create a pod requiring 50Mi resources
			_, _ = virtClient.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})

		}
	}
}

func createRQWithHardLimitOrRequestAndWaitForRegistration(virtClient kubecli.KubevirtClient, rq *v1.ResourceQuota) error {
	_, err := virtClient.CoreV1().ResourceQuotas(rq.Namespace).Create(context.TODO(), rq, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	err = waitForRQWithHardLimitOrRequestRegistration(virtClient, rq, time.Second*100)
	if err != nil {
		return err
	}
	return nil
}

func waitForRQWithHardLimitOrRequestRegistration(virtClient kubecli.KubevirtClient, rq *v1.ResourceQuota, timeout time.Duration) error {
	var containerResourceName v1.ResourceName
	var limitingQuantity resource.Quantity
	var err error
	resourceRequirements := v1.ResourceRequirements{}
	resourceRequirements.Limits = v1.ResourceList{}
	resourceRequirements.Requests = v1.ResourceList{}
	resourceRequirements.Requests[v1.ResourceCPU] = resource.MustParse("0m")
	resourceRequirements.Limits[v1.ResourceCPU] = resource.MustParse("0m")
	resourceRequirements.Requests[v1.ResourceMemory] = resource.MustParse("0Mi")
	resourceRequirements.Limits[v1.ResourceMemory] = resource.MustParse("0Mi")
	resourceRequirements.Requests[v1.ResourceEphemeralStorage] = resource.MustParse("0")
	resourceRequirements.Limits[v1.ResourceEphemeralStorage] = resource.MustParse("0")

	for rn, q := range rq.Spec.Hard {
		containerResourceName, err = limitRequestToValidContainerResourceName(rn)
		if err != nil {
			return err
		}
		limitingQuantity = q
		limitingQuantity.Add(limitingQuantity)
		if strings.HasPrefix(string(rn), "limits.") {
			resourceRequirements.Limits[containerResourceName] = limitingQuantity
		} else {
			resourceRequirements.Limits[containerResourceName] = limitingQuantity
			resourceRequirements.Requests[containerResourceName] = limitingQuantity
		}
	}

	// Create a fake pod that exceeds one of the resourceQuota limitations
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "fake-pod-",
			Namespace:    rq.Namespace,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:      "fake-container",
					Image:     "nginx",
					Resources: resourceRequirements,
					SecurityContext: &v1.SecurityContext{
						AllowPrivilegeEscalation: resourcemerge.BoolPtr(false),
						RunAsNonRoot:             resourcemerge.BoolPtr(true),
						Capabilities: &v1.Capabilities{
							Drop: []v1.Capability{"ALL"},
						},
						SeccompProfile: &v1.SeccompProfile{
							Type: v1.SeccompProfileTypeRuntimeDefault,
						},
					},
				},
			},
		},
	}

	// Wait for the resourceQuota registration to take effect
	timeoutCh := time.After(timeout)
	tick := time.Tick(1 * time.Second)
	for {
		select {
		case <-timeoutCh:
			return fmt.Errorf("timed out waiting for resourceQuota registration")
		case <-tick:
			// Try creating the fake pod and check if it exceeds any of the resourceQuota limitations
			_, err := virtClient.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
			if err != nil && strings.Contains(err.Error(), "exceeded quota") && strings.Contains(err.Error(), rq.Name) {
				return nil
			} else if err != nil {
				fmt.Println(err.Error())
			} else {
				println(" no error?")
			}

		}
	}

}

func limitRequestToValidContainerResourceName(resourceName v1.ResourceName) (v1.ResourceName, error) {
	switch resourceName {
	case v1.ResourceLimitsMemory:
		return v1.ResourceMemory, nil
	case v1.ResourceRequestsMemory:
		return v1.ResourceMemory, nil
	case v1.ResourceMemory:
		return v1.ResourceMemory, nil
	case v1.ResourceLimitsCPU:
		return v1.ResourceCPU, nil
	case v1.ResourceRequestsCPU:
		return v1.ResourceCPU, nil
	case v1.ResourceCPU:
		return v1.ResourceCPU, nil
	case v1.ResourceLimitsEphemeralStorage:
		return v1.ResourceEphemeralStorage, nil
	case v1.ResourceRequestsEphemeralStorage:
		return v1.ResourceEphemeralStorage, nil
	case v1.ResourceEphemeralStorage:
		return v1.ResourceEphemeralStorage, nil
	default:
		return "", fmt.Errorf("limitRequestToValidContainerResourceName support only resource that fits to requests or limits Conversion")
	}
}

func WithNodePreferredAffinityFor(node *v1.Node) libvmi.Option {
	return func(vmi *kv1.VirtualMachineInstance) {
		nodeSelectorTerm := v1.PreferredSchedulingTerm{
			Preference: v1.NodeSelectorTerm{
				MatchExpressions: []v1.NodeSelectorRequirement{
					{
						Key: "kubernetes.io/hostname", Operator: v1.NodeSelectorOpIn, Values: []string{node.Name},
					},
				},
			},
			Weight: int32(100),
		}

		if vmi.Spec.Affinity == nil {
			vmi.Spec.Affinity = &v1.Affinity{}
		}

		if vmi.Spec.Affinity.NodeAffinity == nil {
			vmi.Spec.Affinity.NodeAffinity = &v1.NodeAffinity{}
		}

		if vmi.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution == nil {
			vmi.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = []v1.PreferredSchedulingTerm{}
		}

		vmi.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution =
			append(vmi.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution, nodeSelectorTerm)
	}
}

func validLocking(namespace string, virtClient kubecli.KubevirtClient) (bool, error) {

	// Define a list options to filter ValidatingWebhookConfigurations in the specified namespace.
	listOptions := metav1.ListOptions{
		FieldSelector: "metadata.namespace=" + namespace,
	}
	// List ValidatingWebhookConfigurations in the namespace.
	webhookList, err := virtClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.Background(), listOptions)
	if err != nil {
		return false, err
	}

	// Check if there is only one ValidatingWebhookConfiguration.
	if len(webhookList.Items) > 1 {
		return false, fmt.Errorf("there should be only one locking WHC in the testing ns")
	} else if len(webhookList.Items) == 0 {
		return true, nil
	}

	// Get the creation timestamp of the ValidatingWebhookConfiguration.
	creationTime := webhookList.Items[0].GetCreationTimestamp().Time

	// Calculate the time difference in seconds.
	duration := time.Since(creationTime).Seconds()

	// Check if the ValidatingWebhookConfiguration was created less than 10 seconds ago.
	if duration <= 10 {
		return true, nil
	}

	return false, nil
}
