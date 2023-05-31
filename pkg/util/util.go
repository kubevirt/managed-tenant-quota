package util

import (
	"fmt"
	"kubevirt.io/client-go/log"
	"os"
	"runtime"
	"strings"
)

const (
	readyFile = "/tmp/ready"
)

func PrintVersion() {
	log.Log.Infof(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Log.Infof(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
}
func getNamespace(path string) string {
	if data, err := os.ReadFile(path); err == nil {
		if ns := strings.TrimSpace(string(data)); len(ns) > 0 {
			return ns
		}
	}
	return "mtq"
}

// GetNamespace returns the namespace the pod is executing in
func GetNamespace() string {
	return getNamespace("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
}

func CreateReadyFile() error {
	f, err := os.Create(readyFile)
	if err != nil {
		return err
	}
	defer f.Close()
	return nil
}

func DeleteReadyFile() {
	os.Remove(readyFile)
}
