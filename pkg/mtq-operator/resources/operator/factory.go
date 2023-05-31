package operator

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	utils "kubevirt.io/controller-lifecycle-operator-sdk/pkg/sdk/resources"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/namespaced"
)

// FactoryArgs contains the required parameters to generate all cluster-scoped resources
type FactoryArgs struct {
	NamespacedArgs namespaced.FactoryArgs
	Image          string
}

type factoryFunc func(*FactoryArgs) []client.Object

func aggregateFactoryFunc(funcs ...factoryFunc) factoryFunc {
	return func(args *FactoryArgs) []client.Object {
		var result []client.Object
		for _, f := range funcs {
			result = append(result, f(args)...)
		}
		return result
	}
}

var operatorFactoryFunctions = map[string]factoryFunc{
	"operator-cluster-rbac": createClusterRBAC,
	"operator-rbac":         createNamespacedRBAC,
	"operator-deployment":   createDeployment,
	"operator-crd":          createCRD,
	"everything":            aggregateFactoryFunc(createCRD, createClusterRBAC, createNamespacedRBAC, createDeployment),
}

// ClusterServiceVersionData - Data arguments used to create MTQ's CSV manifest
type ClusterServiceVersionData struct {
	CsvVersion         string
	ReplacesCsvVersion string
	Namespace          string
	ImagePullPolicy    string
	ImagePullSecrets   []corev1.LocalObjectReference
	IconBase64         string
	Verbosity          string

	OperatorVersion string

	ControllerImage    string
	WebhookServerImage string
	OperatorImage      string
}

// CreateOperatorResourceGroup creates all cluster resources from a specific group/component
func CreateOperatorResourceGroup(group string, args *FactoryArgs) ([]client.Object, error) {
	f, ok := operatorFactoryFunctions[group]
	if !ok {
		return nil, fmt.Errorf("group %s does not exist", group)
	}
	resources := f(args)
	for _, r := range resources {
		utils.ValidateGVKs([]runtime.Object{r})
	}
	return resources, nil
}
