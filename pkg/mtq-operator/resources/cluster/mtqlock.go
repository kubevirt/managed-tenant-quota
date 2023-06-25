package cluster

import (
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/utils"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	mtqLockResourceName = "mtq-lock"
)

func createStaticMTQLockResources(args *FactoryArgs) []client.Object {
	return []client.Object{
		createAPIServerClusterRole(),
		createAPIServerClusterRoleBinding(args.Namespace),
	}
}

func getMTQLockServerClusterPolicyRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{
				"kubevirt.io",
			},
			Resources: []string{
				"virtualmachineinstancemigrations",
			},
			Verbs: []string{
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
}

func createAPIServerClusterRoleBinding(namespace string) *rbacv1.ClusterRoleBinding {
	return utils.ResourceBuilder.CreateClusterRoleBinding(mtqLockResourceName, mtqLockResourceName, mtqLockResourceName, namespace)
}

func createAPIServerClusterRole() *rbacv1.ClusterRole {
	return utils.ResourceBuilder.CreateClusterRole(mtqLockResourceName, getMTQLockServerClusterPolicyRules())
}
