package main

import mtq_controller "kubevirt.io/managed-tenant-quota/pkg/mtq-controller"

func main() {
	mtq_controller.Execute()
}
