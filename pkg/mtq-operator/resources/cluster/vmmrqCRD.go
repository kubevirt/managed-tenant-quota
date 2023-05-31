package cluster

import (
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources"
	"strings"

	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

// createVirtualMachineMigrationResourceQuotaCRD creates the VMMRQ schema
func createVirtualMachineMigrationResourceQuotaCRD() *extv1.CustomResourceDefinition {
	crd := extv1.CustomResourceDefinition{}
	_ = k8syaml.NewYAMLToJSONDecoder(strings.NewReader(resources.MTQCRDs["virtualmachinemigrationresourcequota"])).Decode(&crd)
	return &crd
}
