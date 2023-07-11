package tests

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"kubevirt.io/containerized-data-importer/pkg/util/cert/fetcher"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-lock-server/validation"
	validating_webhook_lock "kubevirt.io/managed-tenant-quota/pkg/validating-webhook-lock"
	"kubevirt.io/managed-tenant-quota/tests/framework"
	"time"
)

var _ = Describe("Blocked migration", func() {
	f := framework.NewFramework("fake-test")
	mtqNs := "mtq"
	AfterEach(func() {
		Eventually(func() error {
			return validating_webhook_lock.UnlockNamespace(f.Namespace.GetName(), f.VirtClient)
		}, 20*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), "should be able to unlock namespaced")
	})

	It("Lock namespace for non target launcher pods", func() {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod" + rand.String(3),
				Namespace: f.Namespace.GetName(), // Adjust the namespace as needed
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
		serverBundleFetcher := &fetcher.ConfigMapCertBundleFetcher{
			Name:   "mtq-lock-signer-bundle",
			Client: f.VirtClient.CoreV1().ConfigMaps(mtqNs),
		}
		caBundle, err := serverBundleFetcher.BundleBytes()
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() error {
			return validating_webhook_lock.LockNamespace(f.Namespace.GetName(), mtqNs, f.VirtClient, caBundle)
		}, 20*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), "should be able to lock namespaced")

		Consistently(func() string {
			_, err = f.VirtClient.CoreV1().Pods(pod.Namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
			Expect(err).ShouldNot(BeNil())
			return err.Error()
		}, 5*time.Second, 1*time.Nanosecond).Should(ContainSubstring(validation.InvalidPodCreationErrorMessage), "should be able to lock namespaced")

	})

	It("Lock namespace for rq changes", func() {
		rq := NewQuotaBuilder().
			WithNamespace(f.Namespace.GetName()).
			WithName("test-quota").
			WithResource(v1.ResourceRequestsMemory, resource.MustParse("512Mi")).
			Build()

		_, err := f.VirtClient.CoreV1().ResourceQuotas(rq.Namespace).Create(context.TODO(), rq, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		serverBundleFetcher := &fetcher.ConfigMapCertBundleFetcher{
			Name:   "mtq-lock-signer-bundle",
			Client: f.VirtClient.CoreV1().ConfigMaps(mtqNs),
		}
		caBundle, err := serverBundleFetcher.BundleBytes()
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() error {
			return validating_webhook_lock.LockNamespace(f.Namespace.GetName(), mtqNs, f.VirtClient, caBundle)
		}, 20*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), "should be able to lock namespaced")
		rq.Spec.Hard[v1.ResourceRequestsMemory] = resource.MustParse("612Mi")
		Consistently(func() string {
			_, err = f.VirtClient.CoreV1().ResourceQuotas(rq.Namespace).Update(context.TODO(), rq, metav1.UpdateOptions{})
			Expect(err).ShouldNot(BeNil())
			return err.Error()
		}, 5*time.Second, 1*time.Nanosecond).Should(ContainSubstring(validation.ReasonFoForbiddenRQUpdate), "should be able to lock namespaced")

	})

	It("Lock namespace for vmmrq deletion", func() {
		vmmrq := NewVmmrqBuilder().
			WithNamespace(f.Namespace.GetName()).
			WithName("test-quota").
			WithResource(v1.ResourceRequestsMemory, resource.MustParse("512Mi")).
			Build()

		_, err := f.MtqClient.MtqV1alpha1().VirtualMachineMigrationResourceQuotas(vmmrq.Namespace).Create(context.TODO(), vmmrq, metav1.CreateOptions{})
		Expect(err).To(Not(HaveOccurred()))

		serverBundleFetcher := &fetcher.ConfigMapCertBundleFetcher{
			Name:   "mtq-lock-signer-bundle",
			Client: f.VirtClient.CoreV1().ConfigMaps(mtqNs),
		}
		caBundle, err := serverBundleFetcher.BundleBytes()
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() error {
			return validating_webhook_lock.LockNamespace(f.Namespace.GetName(), mtqNs, f.VirtClient, caBundle)
		}, 20*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), "should be able to lock namespaced")

		Consistently(func() string {
			err = f.MtqClient.MtqV1alpha1().VirtualMachineMigrationResourceQuotas(vmmrq.Namespace).Delete(context.TODO(), vmmrq.Name, metav1.DeleteOptions{})
			Expect(err).ShouldNot(BeNil())
			return err.Error()
		}, 5*time.Second, 1*time.Nanosecond).Should(ContainSubstring(validation.ReasonFoForbiddenVMMRQCreationOrDeletion), "should be able to lock namespaced")

	})

	It("Lock namespace for vmmrq creations", func() {
		vmmrq := NewVmmrqBuilder().
			WithNamespace(f.Namespace.GetName()).
			WithName("test-quota").
			WithResource(v1.ResourceRequestsMemory, resource.MustParse("512Mi")).
			Build()

		serverBundleFetcher := &fetcher.ConfigMapCertBundleFetcher{
			Name:   "mtq-lock-signer-bundle",
			Client: f.VirtClient.CoreV1().ConfigMaps(mtqNs),
		}
		caBundle, err := serverBundleFetcher.BundleBytes()
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() error {
			return validating_webhook_lock.LockNamespace(f.Namespace.GetName(), mtqNs, f.VirtClient, caBundle)
		}, 20*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), "should be able to lock namespaced")

		Consistently(func() string {
			_, err := f.MtqClient.MtqV1alpha1().VirtualMachineMigrationResourceQuotas(vmmrq.Namespace).Create(context.TODO(), vmmrq, metav1.CreateOptions{})
			Expect(err).ShouldNot(BeNil())
			return err.Error()
		}, 5*time.Second, 1*time.Nanosecond).Should(ContainSubstring(validation.ReasonFoForbiddenVMMRQCreationOrDeletion), "should be able to lock namespaced")

	})
})
