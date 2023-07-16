package mtq_lock_server

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/tools/cache"
	"kubevirt.io/kubevirt/pkg/certificates/bootstrap"
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
})
