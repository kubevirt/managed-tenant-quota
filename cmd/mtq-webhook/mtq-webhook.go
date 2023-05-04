package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"k8s.io/client-go/tools/cache"
	"kubevirt.io/client-go/log"
	mtq_webhook "kubevirt.io/managed-tenant-quota/pkg/mtq-webhook"
	"kubevirt.io/managed-tenant-quota/pkg/util"
	"net/http"
	"os"
)

func main() {
	virtCli, err := util.GetVirtCli()
	if err != nil {
		log.Log.Error(err.Error())
		os.Exit(1)
	}
	migrationInformer, err := util.GetMigrationInformer(virtCli)
	if err != nil {
		log.Log.Error(err.Error())
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := ctx.Done()
	go migrationInformer.Run(stop)

	if !cache.WaitForCacheSync(stop, migrationInformer.HasSynced) {
		os.Exit(1)
	}
	kubevirtNS := os.Getenv("KUBEVIRT_INSTALL_NAMESPACE")

	nsBytes, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		panic(err)
	}
	mtqNS := string(nsBytes)

	// handle our core application
	http.Handle("/validate-pods", mtq_webhook.NewTargetLauncherValidator(migrationInformer, kubevirtNS, mtqNS))
	http.HandleFunc("/health", ServeHealth)

	// start the server
	// listens to clear text http on port 8080 unless TLS env var is set to "true"
	if os.Getenv("TLS") == "true" {
		cert := "/etc/admission-webhook/tls/tls.crt"
		key := "/etc/admission-webhook/tls/tls.key"
		log.Log.Infof("Listening on port 443...")
		err := http.ListenAndServeTLS(":443", cert, key, nil)
		if err != nil {
			log.Log.Error(err.Error())
			os.Exit(1)
		}

	} else {
		log.Log.Infof("Listening on port 8080...")
		err := http.ListenAndServe(":8080", nil)
		if err != nil {
			log.Log.Error(err.Error())
			os.Exit(1)

		}
	}
}

// ServeHealth returns 200 when things are good
func ServeHealth(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "OK")
}
