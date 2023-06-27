package tests

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"kubevirt.io/managed-tenant-quota/staging/src/kubevirt.io/managed-tenant-quota-api/pkg/apis/core/v1alpha1"
)

// QuotaBuilder is a builder for creating a ResourceQuota.
type VmmrqBuilder struct {
	vmmrq *v1alpha1.VirtualMachineMigrationResourceQuota
}

// NewQuotaBuilder creates a new instance of QuotaBuilder.
func NewVmmrqBuilder() *VmmrqBuilder {
	return &VmmrqBuilder{
		vmmrq: &v1alpha1.VirtualMachineMigrationResourceQuota{},
	}
}

// WithNamespace sets the namespace for the ResourceQuota.
func (qb *VmmrqBuilder) WithNamespace(namespace string) *VmmrqBuilder {
	qb.vmmrq.ObjectMeta.Namespace = namespace
	return qb
}

// WithName sets the name for the ResourceQuota.
func (qb *VmmrqBuilder) WithName(name string) *VmmrqBuilder {
	qb.vmmrq.ObjectMeta.Name = name
	return qb
}

// WithRequestsMemory sets  requests/limits for the ResourceQuota.
func (qb *VmmrqBuilder) WithResource(resourceName v1.ResourceName, requestMemory resource.Quantity) *VmmrqBuilder {
	if qb.vmmrq.Spec.AdditionalMigrationResources == nil {
		qb.vmmrq.Spec.AdditionalMigrationResources = make(v1.ResourceList)
	}
	qb.vmmrq.Spec.AdditionalMigrationResources[resourceName] = requestMemory
	return qb
}

// Build creates and returns the ResourceQuota.
func (qb *VmmrqBuilder) Build() *v1alpha1.VirtualMachineMigrationResourceQuota {
	return qb.vmmrq
}
