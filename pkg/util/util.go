package util

import (
	"context"
	"crypto/tls"
	"fmt"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/certificate"
	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/log"
	"os"
	"runtime"
	"strings"
)

const (
	readyFile        = "/tmp/ready"
	noSrvCertMessage = "No server certificate, server is not yet ready to receive traffic"
	// Default port that api listens on.
	DefaultPort = 8443
	// Default address api listens on.
	DefaultHost       = "0.0.0.0"
	DefaultMtqNs      = "mtq"
	DefaultKubevirtNs = "kubevirt"
)

var (
	cipherSuites         = tls.CipherSuites()
	insecureCipherSuites = tls.InsecureCipherSuites()
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

// TLSVersion converts from human-readable TLS version (for example "1.1")
// to the values accepted by tls.Config (for example 0x301).
func TLSVersion(version v1.TLSProtocolVersion) uint16 {
	switch version {
	case v1.VersionTLS10:
		return tls.VersionTLS10
	case v1.VersionTLS11:
		return tls.VersionTLS11
	case v1.VersionTLS12:
		return tls.VersionTLS12
	case v1.VersionTLS13:
		return tls.VersionTLS13
	default:
		return tls.VersionTLS12
	}
}

func CipherSuiteNameMap() map[string]uint16 {
	var idByName = map[string]uint16{}
	for _, cipherSuite := range cipherSuites {
		idByName[cipherSuite.Name] = cipherSuite.ID
	}
	for _, cipherSuite := range insecureCipherSuites {
		idByName[cipherSuite.Name] = cipherSuite.ID
	}
	return idByName
}

func CipherSuiteIds(names []string) []uint16 {
	var idByName = CipherSuiteNameMap()
	var ids []uint16
	for _, name := range names {
		if id, ok := idByName[name]; ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func SetupTLS(certManager certificate.Manager) *tls.Config {
	tlsConfig := &tls.Config{
		GetCertificate: func(info *tls.ClientHelloInfo) (certificate *tls.Certificate, err error) {
			cert := certManager.Current()
			if cert == nil {
				return nil, fmt.Errorf(noSrvCertMessage)
			}
			return cert, nil
		},
		GetConfigForClient: func(hi *tls.ClientHelloInfo) (*tls.Config, error) {
			crt := certManager.Current()
			if crt == nil {
				log.Log.Error(noSrvCertMessage)
				return nil, fmt.Errorf(noSrvCertMessage)
			}
			tlsConfig := &v1.TLSConfiguration{ //maybe we will want to add config in MTQ CR in the future
				MinTLSVersion: v1.VersionTLS12,
				Ciphers:       nil,
			}
			ciphers := CipherSuiteIds(tlsConfig.Ciphers)
			minTLSVersion := TLSVersion(tlsConfig.MinTLSVersion)
			config := &tls.Config{
				CipherSuites: ciphers,
				MinVersion:   minTLSVersion,
				Certificates: []tls.Certificate{*crt},
				ClientAuth:   tls.VerifyClientCertIfGiven,
			}

			config.BuildNameToCertificate()
			return config, nil
		},
	}
	tlsConfig.BuildNameToCertificate()
	return tlsConfig
}

func GetKVNS() *string {
	virtCli, err := GetVirtCli()
	if err != nil {
		log.Log.Error(err.Error())
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := ctx.Done()
	kubevirtInformer := KubeVirtInformer(virtCli)
	go kubevirtInformer.Run(stop)
	if err != nil {
		log.Log.Error(err.Error())
		os.Exit(1)
	}
	if !cache.WaitForCacheSync(stop, kubevirtInformer.HasSynced) {
		log.Log.Error("couldn't fetch kv install namespace")
		os.Exit(1)
	}
	objs := kubevirtInformer.GetIndexer().List()
	if len(objs) != 1 {
		log.Log.Error("Single KV object should exist in the cluster.")
		os.Exit(1)
	}
	kv := (objs[0]).(*v1.KubeVirt)

	return &kv.Namespace
}
