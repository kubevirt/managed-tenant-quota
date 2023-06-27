package tests

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// QuotaBuilder is a builder for creating a ResourceQuota.
type QuotaBuilder struct {
	resourceQuota *v1.ResourceQuota
}

// NewQuotaBuilder creates a new instance of QuotaBuilder.
func NewQuotaBuilder() *QuotaBuilder {
	return &QuotaBuilder{
		resourceQuota: &v1.ResourceQuota{},
	}
}

// WithNamespace sets the namespace for the ResourceQuota.
func (qb *QuotaBuilder) WithNamespace(namespace string) *QuotaBuilder {
	qb.resourceQuota.ObjectMeta.Namespace = namespace
	return qb
}

// WithName sets the name for the ResourceQuota.
func (qb *QuotaBuilder) WithName(name string) *QuotaBuilder {
	qb.resourceQuota.ObjectMeta.Name = name
	return qb
}

// WithRequestsMemory sets  requests/limits for the ResourceQuota.
func (qb *QuotaBuilder) WithResource(resourceName v1.ResourceName, requestMemory resource.Quantity) *QuotaBuilder {
	if qb.resourceQuota.Spec.Hard == nil {
		qb.resourceQuota.Spec.Hard = make(v1.ResourceList)
	}
	qb.resourceQuota.Spec.Hard[resourceName] = requestMemory
	return qb
}

// Build creates and returns the ResourceQuota.
func (qb *QuotaBuilder) Build() *v1.ResourceQuota {
	return qb.resourceQuota
}
