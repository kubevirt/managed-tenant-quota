package mtq_lock_server

import (
	"bytes"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	virtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/kubevirt/pkg/certificates/bootstrap"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-lock-server/validation"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/namespaced"
	"kubevirt.io/managed-tenant-quota/pkg/util"
	"net/http"
	"net/http/httptest"
)

var _ = Describe("Test mtq serve functions", func() {
	It("Healthz", func() {
		secretCache := cache.NewStore(cache.DeletionHandlingMetaNamespaceKeyFunc)
		secretCertManager := bootstrap.NewFallbackCertificateManager(
			bootstrap.NewSecretCertificateManager(
				namespaced.SecretResourceName,
				util.DefaultMtqNs,
				secretCache,
			),
		)
		mtqLockServer, err := MTQLockServer(util.DefaultMtqNs,
			util.DefaultHost,
			util.DefaultPort,
			secretCertManager,
		)
		req, err := http.NewRequest("GET", healthzPath, nil)
		Expect(err).ToNot(HaveOccurred())
		rr := httptest.NewRecorder()
		mtqLockServer.ServeHTTP(rr, req)
		status := rr.Code
		Expect(status).To(Equal(http.StatusOK))

	})

	It("servePath", func() {
		setKVNS("kubevirt-test")
		secretCache := cache.NewStore(cache.DeletionHandlingMetaNamespaceKeyFunc)
		secretCertManager := bootstrap.NewFallbackCertificateManager(
			bootstrap.NewSecretCertificateManager(
				namespaced.SecretResourceName,
				util.DefaultMtqNs,
				secretCache,
			),
		)
		mtqLockServer, err := MTQLockServer(util.DefaultMtqNs,
			util.DefaultHost,
			util.DefaultPort,
			secretCertManager,
		)
		// Create a new pod create request
		admissionReview := v1.AdmissionReview{
			TypeMeta: metav1.TypeMeta{
				Kind:       "AdmissionReview",
				APIVersion: "admission.k8s.io/v1",
			},
			Request: &v1.AdmissionRequest{
				UID: types.UID("<unique-identifier>"),
				Kind: metav1.GroupVersionKind{
					Kind: "Pod",
				},
				Resource: metav1.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "pods",
				},
				Name:      "testPod",
				Namespace: "testNS",
				Operation: v1.Create,
				UserInfo: authenticationv1.UserInfo{
					Username: fmt.Sprintf("%s:%s", fmt.Sprintf("system:serviceaccount:%s", "kubevirt-2test"), validation.VirtControllerServiceAccountName),
				},
				Object: runtime.RawExtension{
					Raw: []byte(`{
				"apiVersion": "v1",
				"kind": "Pod",
				"metadata": {
					"name": "testPod",
					"namespace": "testNS",
					"labels": {
						"` + virtv1.MigrationJobLabel + `": "fakeMigration"
					}
				}
			}`),
				},
			},
		}
		// Convert the admission review to JSON
		admissionReviewBody, err := json.Marshal(admissionReview)
		if err != nil {
			// Handle error
		}

		// Create a new HTTP POST request with the admission review body
		req, err := http.NewRequest("POST", servePath, bytes.NewBuffer(admissionReviewBody))
		if err != nil {
			// Handle error
		}
		// Set the Content-Type header to "application/json"
		req.Header.Set("Content-Type", "application/json")

		Expect(err).ToNot(HaveOccurred())
		rr := httptest.NewRecorder()
		mtqLockServer.ServeHTTP(rr, req)
		status := rr.Code
		Expect(status).To(Equal(http.StatusOK))
	})
})

func setKVNS(namespace string) {
	getKVNS = func() *string {
		return &namespace
	}
}
