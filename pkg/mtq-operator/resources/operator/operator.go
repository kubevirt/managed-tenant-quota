package operator

import (
	"encoding/json"
	"github.com/coreos/go-semver/semver"
	csvv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
				"clusterrolebindings",
				"clusterroles",
			},
			Verbs: []string{
				"create",
				"get",
				"list",
				"watch",
				"delete",
				"update",
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
				"create",
				"get",
				"list",
				"watch",
				"delete",
				"update",
			},
		},
		{
			APIGroups: []string{
				"mtq.kubevirt.io",
			},
			Resources: []string{
				"mtqs",
				"mtqs/finalizers",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"delete",
				"update",
			},
		},
		{
			APIGroups: []string{
				"mtq.kubevirt.io",
			},
			Resources: []string{
				"mtqs/status",
			},
			Verbs: []string{
				"get",
				"update",
				"patch",
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
				"create",
				"get",
				"list",
				"watch",
				"delete",
				"update",
			},
		},
		{
			APIGroups: []string{
				"",
			},
			Resources: []string{
				"pods",
				"services",
				"endpoints",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
				"create",
				"update",
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
				"create",
				"get",
				"list",
				"watch",
				"delete",
				"update",
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
				"get",
				"list",
				"watch",
				"delete",
				"update",
				"create",
				"patch",
			},
		},
		{
			APIGroups: []string{
				"rbac.authorization.k8s.io",
			},
			Resources: []string{
				"rolebindings",
				"roles",
			},
			Verbs: []string{
				"create",
				"get",
				"list",
				"watch",
				"delete",
				"update",
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
			args.NamespacedArgs.MTQLockServerImage,
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
			Name:  "MTQ_LOCK_SERVER_IMAGE",
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

type csvPermissions struct {
	ServiceAccountName string              `json:"serviceAccountName"`
	Rules              []rbacv1.PolicyRule `json:"rules"`
}
type csvDeployments struct {
	Name string                `json:"name"`
	Spec appsv1.DeploymentSpec `json:"spec,omitempty"`
}

type csvStrategySpec struct {
	Permissions        []csvPermissions `json:"permissions"`
	ClusterPermissions []csvPermissions `json:"clusterPermissions"`
	Deployments        []csvDeployments `json:"deployments"`
}

func createClusterServiceVersion(data *ClusterServiceVersionData) (*csvv1.ClusterServiceVersion, error) {

	description := `
MTQ is a kubernetes extension that provides Multi Tenancy support to kubevirt

_The MTQ Operator does not support updates yet._
`

	deployment := createOperatorDeployment(
		data.OperatorVersion,
		data.Namespace,
		"true",
		data.OperatorImage,
		data.ControllerImage,
		data.WebhookServerImage,
		data.Verbosity,
		data.ImagePullPolicy,
		data.ImagePullSecrets)

	deployment.Spec.Template.Spec.PriorityClassName = utils2.MTQPriorityClass

	strategySpec := csvStrategySpec{
		Permissions: []csvPermissions{
			{
				ServiceAccountName: serviceAccountName,
				Rules:              getNamespacedPolicyRules(),
			},
		},
		ClusterPermissions: []csvPermissions{
			{
				ServiceAccountName: serviceAccountName,
				Rules:              getClusterPolicyRules(),
			},
		},
		Deployments: []csvDeployments{
			{
				Name: "mtq-operator",
				Spec: deployment.Spec,
			},
		},
	}

	strategySpecJSONBytes, err := json.Marshal(strategySpec)
	if err != nil {
		return nil, err
	}

	return &csvv1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterServiceVersion",
			APIVersion: "operators.coreos.com/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mtqoperator." + data.CsvVersion,
			Namespace: data.Namespace,
			Annotations: map[string]string{

				"capabilities": "Full Lifecycle",
				"categories":   "Storage,Virtualization",
				"alm-examples": `
      [
        {
          "apiVersion":"mtq.kubevirt.io/v1alpha1",
          "kind":"MTQ",
          "metadata": {
            "name":"mtq",
            "namespace":"mtq"
          },
          "spec": {
            "imagePullPolicy":"IfNotPresent"
          }
        }
      ]`,
				"description": "Creates and maintains MTQ deployments",
			},
		},

		Spec: csvv1.ClusterServiceVersionSpec{
			DisplayName: "MTQ",
			Description: description,
			Keywords:    []string{"MTQ", "Virtualization", "MultiTenancy"},
			Version:     *semver.New(data.CsvVersion),
			Maturity:    "alpha",
			Replaces:    data.ReplacesCsvVersion,
			Maintainers: []csvv1.Maintainer{{
				Name:  "KubeVirt project",
				Email: "kubevirt-dev@googlegroups.com",
			}},
			Provider: csvv1.AppLink{
				Name: "KubeVirt/MTQ project",
			},
			Links: []csvv1.AppLink{
				{
					Name: "MTQ",
					URL:  "https://github.com/kubevirt/managed-tenant-quota/blob/main/README.md",
				},
				{
					Name: "Source Code",
					URL:  "https://github.com/kubevirt/managed-tenant-quota",
				},
			},
			Icon: []csvv1.Icon{{
				Data:      data.IconBase64,
				MediaType: "image/png",
			}},
			Labels: map[string]string{
				"alm-owner-mtq": "mtq-operator",
				"operated-by":   "mtq-operator",
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"alm-owner-mtq": "mtq-operator",
					"operated-by":   "mtq-operator",
				},
			},
			InstallModes: []csvv1.InstallMode{
				{
					Type:      csvv1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      csvv1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      csvv1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      csvv1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: csvv1.NamedInstallStrategy{
				StrategyName:    "deployment",
				StrategySpecRaw: json.RawMessage(strategySpecJSONBytes),
			},
			CustomResourceDefinitions: csvv1.CustomResourceDefinitions{

				Owned: []csvv1.CRDDescription{
					{
						Name:        "mtqs.mtq.kubevirt.io",
						Version:     "v1alpha1",
						Kind:        "MTQ",
						DisplayName: "MTQ deployment",
						Description: "Represents a MTQ deployment",
						Resources: []csvv1.APIResourceReference{
							{
								Kind:    "ConfigMap",
								Name:    "mtq-operator-leader-election-helper",
								Version: "v1",
							},
						},
						SpecDescriptors: []csvv1.SpecDescriptor{

							{
								Description:  "The ImageRegistry to use for the MTQ components.",
								DisplayName:  "ImageRegistry",
								Path:         "imageRegistry",
								XDescriptors: []string{"urn:alm:descriptor:text"},
							},
							{
								Description:  "The ImageTag to use for the MTQ components.",
								DisplayName:  "ImageTag",
								Path:         "imageTag",
								XDescriptors: []string{"urn:alm:descriptor:text"},
							},
							{
								Description:  "The ImagePullPolicy to use for the MTQ components.",
								DisplayName:  "ImagePullPolicy",
								Path:         "imagePullPolicy",
								XDescriptors: []string{"urn:alm:descriptor:io.kubernetes:imagePullPolicy"},
							},
						},
						StatusDescriptors: []csvv1.StatusDescriptor{
							{
								Description:  "The deployment phase.",
								DisplayName:  "Phase",
								Path:         "phase",
								XDescriptors: []string{"urn:alm:descriptor:io.kubernetes.phase"},
							},
							{
								Description:  "Explanation for the current status of the MTQ deployment.",
								DisplayName:  "Conditions",
								Path:         "conditions",
								XDescriptors: []string{"urn:alm:descriptor:io.kubernetes.conditions"},
							},
							{
								Description:  "The observed version of the MTQ deployment.",
								DisplayName:  "Observed MTQ Version",
								Path:         "observedVersion",
								XDescriptors: []string{"urn:alm:descriptor:text"},
							},
							{
								Description:  "The targeted version of the MTQ deployment.",
								DisplayName:  "Target MTQ Version",
								Path:         "targetVersion",
								XDescriptors: []string{"urn:alm:descriptor:text"},
							},
							{
								Description:  "The version of the MTQ Operator",
								DisplayName:  "MTQ Operator Version",
								Path:         "operatorVersion",
								XDescriptors: []string{"urn:alm:descriptor:text"},
							},
						},
					},
				},
			},
		},
	}, nil
}
