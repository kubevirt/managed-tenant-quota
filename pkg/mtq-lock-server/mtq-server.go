package mtq_lock_server

import (
	"fmt"
	"github.com/rs/cors"
	"io"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/certificate"
	"k8s.io/klog/v2"
	"kubevirt.io/managed-tenant-quota/pkg/util"
	"net/http"
)

const (
	healthzPath = "/healthz"
	servePath   = "/validate-pods"
)

// Server is the public interface to the upload proxy
type Server interface {
	Start() error
}

type MTQServer struct {
	mtqNS             string
	kubevirtNS        string
	bindAddress       string
	bindPort          uint
	migrationInformer cache.SharedIndexInformer
	secretCertManager certificate.Manager
	handler           http.Handler
}

// MTQLockServer returns an initialized uploadProxyApp
func MTQLockServer(mtqNS string,
	kubevirtNS string,
	bindAddress string,
	bindPort uint,
	migrationInformer cache.SharedIndexInformer,
	secretCertManager certificate.Manager) (Server, error) {
	app := &MTQServer{
		mtqNS:             mtqNS,
		kubevirtNS:        kubevirtNS,
		migrationInformer: migrationInformer,
		secretCertManager: secretCertManager,
		bindAddress:       bindAddress,
		bindPort:          bindPort,
	}

	app.initHandler()

	return app, nil
}

func (app *MTQServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	app.handler.ServeHTTP(w, r)
}

func (app *MTQServer) initHandler() {
	mux := http.NewServeMux()
	mux.HandleFunc(healthzPath, app.handleHealthzRequest)
	mux.Handle(servePath, NewTargetLauncherValidator(app.migrationInformer, app.kubevirtNS, app.mtqNS))

	app.handler = cors.AllowAll().Handler(mux)

}

func (app *MTQServer) handleHealthzRequest(w http.ResponseWriter, r *http.Request) {
	_, err := io.WriteString(w, "OK")
	if err != nil {
		klog.Errorf("handleHealthzRequest: failed to send response; %v", err)
	}
}

func (app *MTQServer) Start() error {
	return app.startTLS()
}

func (app *MTQServer) startTLS() error {
	var serveFunc func() error
	bindAddr := fmt.Sprintf("%s:%d", app.bindAddress, app.bindPort)
	tlsConfig := util.SetupTLS(app.secretCertManager)
	server := &http.Server{
		Addr:      bindAddr,
		Handler:   app.handler,
		TLSConfig: tlsConfig,
	}

	serveFunc = func() error {
		return server.ListenAndServeTLS("", "")
	}

	errChan := make(chan error)

	go func() {
		errChan <- serveFunc()
	}()
	// wait for server to exit
	return <-errChan
}
