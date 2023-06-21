package main

import (
	"context"
	"github.com/pkg/errors"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"kubevirt.io/client-go/log"
	"kubevirt.io/kubevirt/pkg/certificates/bootstrap"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-lock-server"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/namespaced"
	"kubevirt.io/managed-tenant-quota/pkg/util"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

const (
	// Default port that api listens on.
	defaultPort = 8443
	// Default address api listens on.
	defaultHost = "0.0.0.0"
)

func main() {
	defer klog.Flush()
	mtqNS := util.GetNamespace()

	virtCli, err := util.GetVirtCli()
	if err != nil {
		log.Log.Error(err.Error())
		os.Exit(1)
	}
	ctx := signals.SetupSignalHandler()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := ctx.Done()
	if err := util.CreateReadyFile(); err != nil {
		klog.Fatalf("Error creating ready file: %+v", err)
	}

	secretInformer := util.GetSecretInformer(virtCli, mtqNS)
	go secretInformer.Run(stop)
	if !cache.WaitForCacheSync(stop, secretInformer.HasSynced) {
		os.Exit(1)
	}

	secretCertManager := bootstrap.NewFallbackCertificateManager(
		bootstrap.NewSecretCertificateManager(
			namespaced.SecretResourceName,
			mtqNS,
			secretInformer.GetStore(),
		),
	)

	secretCertManager.Start()
	defer secretCertManager.Stop()

	mtqLockServer, err := mtq_lock_server.MTQLockServer(mtqNS,
		defaultHost,
		defaultPort,
		secretCertManager,
	)
	if err != nil {
		klog.Fatalf("UploadProxy failed to initialize: %v\n", errors.WithStack(err))
	}

	err = mtqLockServer.Start()
	if err != nil {
		klog.Fatalf("TLS server failed: %v\n", errors.WithStack(err))
	}

}
