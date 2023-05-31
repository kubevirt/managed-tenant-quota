package operator

import (
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources"
	utils2 "kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/utils"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/cluster"
)

const (
	serviceAccountName = "mtq-operator"
	roleName           = "mtq-operator"
	clusterRoleName    = roleName + "-cluster"
)

func getClusterPolicyRules() []rbacv1.PolicyRule {
	rules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{
				"rbac.authorization.k8s.io",
			},
			Resources: []string{
				"rolebindings",
				"roles",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				"rbac.authorization.k8s.io",
			},
			Resources: []string{
				"clusterrolebindings",
				"clusterroles",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				"security.openshift.io",
			},
			Resources: []string{
				"securitycontextconstraints",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"update",
				"create",
			},
		},
		{
			APIGroups: []string{
				"",
			},
			Resources: []string{
				"pods",
				"services",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"delete",
			},
		},
		{
			APIGroups: []string{
				"apiextensions.k8s.io",
			},
			Resources: []string{
				"customresourcedefinitions",
				"customresourcedefinitions/status",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				"mtq.kubevirt.io",
			},
			Resources: []string{
				"*",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				"scheduling.k8s.io",
			},
			Resources: []string{
				"priorityclasses",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{
				"kubevirt.io",
			},
			Resources: []string{
				"kubevirts",
			},
			Verbs: []string{
				"list",
				"watch",
			},
		},
	}
	rules = append(rules, cluster.GetClusterRolePolicyRules()...)
	return rules
}

func createClusterRole() *rbacv1.ClusterRole {
	return utils2.ResourceBuilder.CreateOperatorClusterRole(clusterRoleName, getClusterPolicyRules())
}

func createClusterRoleBinding(namespace string) *rbacv1.ClusterRoleBinding {
	return utils2.ResourceBuilder.CreateOperatorClusterRoleBinding(serviceAccountName, clusterRoleName, serviceAccountName, namespace)
}

func createClusterRBAC(args *FactoryArgs) []client.Object {
	return []client.Object{
		createClusterRole(),
		createClusterRoleBinding(args.NamespacedArgs.Namespace),
	}
}

func getNamespacedPolicyRules() []rbacv1.PolicyRule {
	rules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{
				"",
			},
			Resources: []string{
				"serviceaccounts",
				"configmaps",
				"events",
				"secrets",
				"services",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				"apps",
			},
			Resources: []string{
				"deployments",
				"deployments/finalizers",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				"monitoring.coreos.com",
			},
			Resources: []string{
				"servicemonitors",
				"prometheusrules",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"create",
				"delete",
				"update",
				"patch",
			},
		},
		{
			APIGroups: []string{
				"coordination.k8s.io",
			},
			Resources: []string{
				"leases",
			},
			Verbs: []string{
				"*",
			},
		},
	}
	return rules
}

func createServiceAccount(namespace string) *corev1.ServiceAccount {
	return utils2.ResourceBuilder.CreateOperatorServiceAccount(serviceAccountName, namespace)
}

func createNamespacedRole(namespace string) *rbacv1.Role {
	role := utils2.ResourceBuilder.CreateRole(roleName, getNamespacedPolicyRules())
	role.Namespace = namespace
	return role
}

func createNamespacedRoleBinding(namespace string) *rbacv1.RoleBinding {
	roleBinding := utils2.ResourceBuilder.CreateRoleBinding(serviceAccountName, roleName, serviceAccountName, namespace)
	roleBinding.Namespace = namespace
	return roleBinding
}

func createNamespacedRBAC(args *FactoryArgs) []client.Object {
	return []client.Object{
		createServiceAccount(args.NamespacedArgs.Namespace),
		createNamespacedRole(args.NamespacedArgs.Namespace),
		createNamespacedRoleBinding(args.NamespacedArgs.Namespace),
	}
}

func createDeployment(args *FactoryArgs) []client.Object {
	return []client.Object{
		createOperatorDeployment(args.NamespacedArgs.OperatorVersion,
			args.NamespacedArgs.Namespace,
			args.NamespacedArgs.DeployClusterResources,
			args.Image,
			args.NamespacedArgs.ControllerImage,
			args.NamespacedArgs.MTQLockImage,
			args.NamespacedArgs.Verbosity,
			args.NamespacedArgs.PullPolicy,
			args.NamespacedArgs.ImagePullSecrets),
	}
}

func createCRD(args *FactoryArgs) []client.Object {
	return []client.Object{
		createMTQListCRD(),
	}
}
func createMTQListCRD() *extv1.CustomResourceDefinition {
	crd := extv1.CustomResourceDefinition{}
	_ = k8syaml.NewYAMLToJSONDecoder(strings.NewReader(resources.MTQCRDs["mtq"])).Decode(&crd)
	return &crd
}

func createOperatorEnvVar(operatorVersion, deployClusterResources, controllerImage, webhookServerImage, verbosity, pullPolicy string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name:  "DEPLOY_CLUSTER_RESOURCES",
			Value: deployClusterResources,
		},
		{
			Name:  "OPERATOR_VERSION",
			Value: operatorVersion,
		},
		{
			Name:  "CONTROLLER_IMAGE",
			Value: controllerImage,
		},
		{
			Name:  "MTQ_LOCK_IMAGE",
			Value: webhookServerImage,
		},
		{
			Name:  "VERBOSITY",
			Value: verbosity,
		},
		{
			Name:  "PULL_POLICY",
			Value: pullPolicy,
		},
		{
			Name:  "MONITORING_NAMESPACE",
			Value: "",
		},
	}
}

func createOperatorDeployment(operatorVersion, namespace, deployClusterResources, operatorImage, controllerImage, webhookServerImage, verbosity, pullPolicy string, imagePullSecrets []corev1.LocalObjectReference) *appsv1.Deployment {
	deployment := utils2.CreateOperatorDeployment("mtq-operator", namespace, "name", "mtq-operator", serviceAccountName, imagePullSecrets, int32(1))
	container := utils2.CreateContainer("mtq-operator", operatorImage, verbosity, pullPolicy)
	container.Ports = createPrometheusPorts()
	container.SecurityContext.Capabilities = &corev1.Capabilities{
		Drop: []corev1.Capability{
			"ALL",
		},
	}
	container.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("150Mi"),
		},
	}
	container.Env = createOperatorEnvVar(operatorVersion, deployClusterResources, controllerImage, webhookServerImage, verbosity, pullPolicy)
	deployment.Spec.Template.Spec.Containers = []corev1.Container{container}
	return deployment
}

func createPrometheusPorts() []corev1.ContainerPort {
	return []corev1.ContainerPort{
		{
			Name:          "metrics",
			ContainerPort: 8080,
			Protocol:      "TCP",
		},
	}
}
