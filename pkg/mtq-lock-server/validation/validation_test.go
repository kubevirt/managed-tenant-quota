package validation

import (
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	virtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/managed-tenant-quota/pkg/util"
	"net/http"
)

var _ = Describe("Test validation of mtq lock server", func() {
	It("Valid migration target pod should be accepted", func() {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					virtv1.MigrationJobLabel: "fakeMigration",
				},
			},
		}

		podBytes, err := json.Marshal(pod)
		Expect(err).ToNot(HaveOccurred())

		v := Validator{
			Request: &admissionv1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Kind: "Pod",
				},
				Object: runtime.RawExtension{
					Raw:    podBytes,
					Object: pod,
				},

				UserInfo: authenticationv1.UserInfo{
					Username: fmt.Sprintf("system:serviceaccount:%v:kubevirt-controller", util.DefaultKubevirtNs),
				},
			},
		}
		admissionReview, err := v.Validate(util.DefaultKubevirtNs, util.DefaultMtqNs)
		Expect(err).ToNot(HaveOccurred())
		Expect(admissionReview.Response.Allowed).To(BeTrue())
		Expect(admissionReview.Response.Result.Code).To(Equal(int32(http.StatusAccepted)))
		Expect(admissionReview.Response.Result.Message).To(Equal(ReasonForAcceptedPodCreation))
	})

	It("Pod that is not target launcher should not be accepted", func() {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{},
		}

		podBytes, err := json.Marshal(pod)
		Expect(err).ToNot(HaveOccurred())

		v := Validator{
			Request: &admissionv1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Kind: "Pod",
				},
				Object: runtime.RawExtension{
					Raw:    podBytes,
					Object: pod,
				},

				UserInfo: authenticationv1.UserInfo{
					Username: fmt.Sprintf("system:serviceaccount:%v:kubevirt-controller", util.DefaultKubevirtNs),
				},
			},
		}
		admissionReview, err := v.Validate(util.DefaultKubevirtNs, util.DefaultMtqNs)
		Expect(err).ToNot(HaveOccurred())
		Expect(admissionReview.Response.Allowed).To(BeFalse())
		Expect(admissionReview.Response.Result.Code).To(Equal(int32(http.StatusForbidden)))
		Expect(admissionReview.Response.Result.Message).To(Equal(InvalidPodCreationErrorMessage))
	})

	It("Target migration is not valid if is not created by the migration controller", func() {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					virtv1.MigrationJobLabel: "fakeMigration",
				},
			},
		}

		podBytes, err := json.Marshal(pod)
		Expect(err).ToNot(HaveOccurred())

		v := Validator{
			Request: &admissionv1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Kind: "Pod",
				},
				Object: runtime.RawExtension{
					Raw:    podBytes,
					Object: pod,
				},

				UserInfo: authenticationv1.UserInfo{
					Username: fmt.Sprintf("system:serviceaccount:%v:faker-controller", "fakens"),
				},
			},
		}
		admissionReview, err := v.Validate(util.DefaultKubevirtNs, util.DefaultMtqNs)
		Expect(err).ToNot(HaveOccurred())
		Expect(admissionReview.Response.Allowed).To(BeFalse())
		Expect(admissionReview.Response.Result.Code).To(Equal(int32(http.StatusForbidden)))
		Expect(admissionReview.Response.Result.Message).To(Equal(InvalidPodCreationErrorMessage))
	})

	It("Validation should not allow non mtq-controller rq modifications requests", func() {
		v := Validator{
			Request: &admissionv1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Kind: "ResourceQuota",
				},
				UserInfo: authenticationv1.UserInfo{
					Username: fmt.Sprintf("system:serviceaccount:%v:faker-controller", "fakens"),
				},
			},
		}

		admissionReview, err := v.Validate(util.DefaultKubevirtNs, util.DefaultMtqNs)
		Expect(err).ToNot(HaveOccurred())
		Expect(admissionReview.Response.Allowed).To(BeFalse())
		Expect(admissionReview.Response.Result.Code).To(Equal(int32(http.StatusForbidden)))
		Expect(admissionReview.Response.Result.Message).To(Equal(ReasonFoForbiddenRQUpdate))
	})

	It("Validation should not allow non mtq-controller vmmrq modifications requests", func() {
		v := Validator{
			Request: &admissionv1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Kind: "VirtualMachineMigrationResourceQuota",
				},
				UserInfo: authenticationv1.UserInfo{
					Username: fmt.Sprintf("system:serviceaccount:%v:faker-controller", "fakens"),
				},
			},
		}

		admissionReview, err := v.Validate(util.DefaultKubevirtNs, util.DefaultMtqNs)
		Expect(err).ToNot(HaveOccurred())
		Expect(admissionReview.Response.Allowed).To(BeFalse())
		Expect(admissionReview.Response.Result.Code).To(Equal(int32(http.StatusForbidden)))
		Expect(admissionReview.Response.Result.Message).To(Equal(ReasonFoForbiddenVMMRQCreationOrDeletion))
	})

	It("Validation should allow mtq-controller rq modifications requests", func() {
		v := Validator{
			Request: &admissionv1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Kind: "ResourceQuota",
				},
				UserInfo: authenticationv1.UserInfo{
					Username: fmt.Sprintf("system:serviceaccount:%v:%v", util.DefaultMtqNs, MtqContollerServiceAccountName),
				},
			},
		}

		admissionReview, err := v.Validate(util.DefaultKubevirtNs, util.DefaultMtqNs)
		Expect(err).ToNot(HaveOccurred())
		Expect(admissionReview.Response.Allowed).To(BeTrue())
		Expect(admissionReview.Response.Result.Code).To(Equal(int32(http.StatusAccepted)))
		Expect(admissionReview.Response.Result.Message).To(Equal(ReasonForAcceptedRQUpdate))
	})

	It("Validation should allow mtq-controller vmmrq modifications requests", func() {
		v := Validator{
			Request: &admissionv1.AdmissionRequest{
				Kind: metav1.GroupVersionKind{
					Kind: "VirtualMachineMigrationResourceQuota",
				},
				UserInfo: authenticationv1.UserInfo{
					Username: fmt.Sprintf("system:serviceaccount:%v:%v", util.DefaultMtqNs, MtqContollerServiceAccountName),
				},
			},
		}

		admissionReview, err := v.Validate(util.DefaultKubevirtNs, util.DefaultMtqNs)
		Expect(err).ToNot(HaveOccurred())
		Expect(admissionReview.Response.Allowed).To(BeTrue())
		Expect(admissionReview.Response.Result.Code).To(Equal(int32(http.StatusAccepted)))
		Expect(admissionReview.Response.Result.Message).To(Equal(ReasonForAcceptedVMMRQUpdate))
	})
})
