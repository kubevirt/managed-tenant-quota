package main

import (
	"context"
	"k8s.io/klog/v2"
	"kubevirt.io/client-go/log"
	mtq_controller "kubevirt.io/managed-tenant-quota/pkg/mtq-controller"
	"kubevirt.io/managed-tenant-quota/pkg/util"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := ctx.Done()
	if err := util.CreateReadyFile(); err != nil {
		klog.Fatalf("Error creating ready file: %+v", err)
	}
	if err := mtq_controller.Run(3, stop); err != nil {
		log.Log.Warningf("error running the clone controller: %v", err)
	}
	util.DeleteReadyFile()

	klog.V(2).Infoln("MTQ controller exited")
}
