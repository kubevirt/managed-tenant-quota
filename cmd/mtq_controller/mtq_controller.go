package main

import (
	"context"
	"kubevirt.io/client-go/log"
	mtq_controller "kubevirt.io/managed-tenant-quota/pkg/mtq-controller"
)

func main() {
	ctx, _ := context.WithCancel(context.Background())
	stop := ctx.Done()
	if err := mtq_controller.Run(3, stop); err != nil {
		log.Log.Warningf("error running the clone controller: %v", err)
	}
}
