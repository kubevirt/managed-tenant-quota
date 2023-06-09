package v1beta1

// This file contains a collection of methods that can be used from go-restful to
// generate Swagger API documentation for its models. Please read this PR for more
// information on the implementation: https://github.com/emicklei/go-restful/pull/215
//
// TODOs are ignored from the parser (e.g. TODO(andronat):... || TODO:...) if and only if
// they are on one line! For multiple line or blocks that you want to ignore use ---.
// Any context after a --- is ignored.
//
// Those methods can be generated by using hack/update-swagger-docs.sh

// AUTO-GENERATED FUNCTIONS START HERE
var map_ConnectionConfig = map[string]string{
	"url":             "Chart repository URL",
	"ca":              "ca is an optional reference to a config map by name containing the PEM-encoded CA bundle. It is used as a trust anchor to validate the TLS certificate presented by the remote server. The key \"ca-bundle.crt\" is used to locate the data. If empty, the default system roots are used. The namespace for this config map is openshift-config.",
	"tlsClientConfig": "tlsClientConfig is an optional reference to a secret by name that contains the PEM-encoded TLS client certificate and private key to present when connecting to the server. The key \"tls.crt\" is used to locate the client certificate. The key \"tls.key\" is used to locate the private key. The namespace for this secret is openshift-config.",
}

func (ConnectionConfig) SwaggerDoc() map[string]string {
	return map_ConnectionConfig
}

var map_HelmChartRepository = map[string]string{
	"":         "HelmChartRepository holds cluster-wide configuration for proxied Helm chart repository\n\nCompatibility level 2: Stable within a major release for a minimum of 9 months or 3 minor releases (whichever is longer).",
	"metadata": "metadata is the standard object's metadata. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata",
	"spec":     "spec holds user settable values for configuration",
	"status":   "Observed status of the repository within the cluster..",
}

func (HelmChartRepository) SwaggerDoc() map[string]string {
	return map_HelmChartRepository
}

var map_HelmChartRepositoryList = map[string]string{
	"":         "Compatibility level 2: Stable within a major release for a minimum of 9 months or 3 minor releases (whichever is longer).",
	"metadata": "metadata is the standard list's metadata. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata",
}

func (HelmChartRepositoryList) SwaggerDoc() map[string]string {
	return map_HelmChartRepositoryList
}

var map_HelmChartRepositorySpec = map[string]string{
	"":                 "Helm chart repository exposed within the cluster",
	"disabled":         "If set to true, disable the repo usage in the cluster/namespace",
	"name":             "Optional associated human readable repository name, it can be used by UI for displaying purposes",
	"description":      "Optional human readable repository description, it can be used by UI for displaying purposes",
	"connectionConfig": "Required configuration for connecting to the chart repo",
}

func (HelmChartRepositorySpec) SwaggerDoc() map[string]string {
	return map_HelmChartRepositorySpec
}

var map_HelmChartRepositoryStatus = map[string]string{
	"conditions": "conditions is a list of conditions and their statuses",
}

func (HelmChartRepositoryStatus) SwaggerDoc() map[string]string {
	return map_HelmChartRepositoryStatus
}

var map_ConnectionConfigNamespaceScoped = map[string]string{
	"url":             "Chart repository URL",
	"ca":              "ca is an optional reference to a config map by name containing the PEM-encoded CA bundle. It is used as a trust anchor to validate the TLS certificate presented by the remote server. The key \"ca-bundle.crt\" is used to locate the data. If empty, the default system roots are used. The namespace for this configmap must be same as the namespace where the project helm chart repository is getting instantiated.",
	"tlsClientConfig": "tlsClientConfig is an optional reference to a secret by name that contains the PEM-encoded TLS client certificate and private key to present when connecting to the server. The key \"tls.crt\" is used to locate the client certificate. The key \"tls.key\" is used to locate the private key. The namespace for this secret must be same as the namespace where the project helm chart repository is getting instantiated.",
	"basicAuthConfig": "basicAuthConfig is an optional reference to a secret by name that contains the basic authentication credentials to present when connecting to the server. The key \"username\" is used locate the username. The key \"password\" is used to locate the password. The namespace for this secret must be same as the namespace where the project helm chart repository is getting instantiated.",
}

func (ConnectionConfigNamespaceScoped) SwaggerDoc() map[string]string {
	return map_ConnectionConfigNamespaceScoped
}

var map_ProjectHelmChartRepository = map[string]string{
	"":         "ProjectHelmChartRepository holds namespace-wide configuration for proxied Helm chart repository\n\nCompatibility level 2: Stable within a major release for a minimum of 9 months or 3 minor releases (whichever is longer).",
	"metadata": "metadata is the standard object's metadata. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata",
	"spec":     "spec holds user settable values for configuration",
	"status":   "Observed status of the repository within the namespace..",
}

func (ProjectHelmChartRepository) SwaggerDoc() map[string]string {
	return map_ProjectHelmChartRepository
}

var map_ProjectHelmChartRepositoryList = map[string]string{
	"":         "Compatibility level 2: Stable within a major release for a minimum of 9 months or 3 minor releases (whichever is longer).",
	"metadata": "metadata is the standard list's metadata. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata",
}

func (ProjectHelmChartRepositoryList) SwaggerDoc() map[string]string {
	return map_ProjectHelmChartRepositoryList
}

var map_ProjectHelmChartRepositorySpec = map[string]string{
	"":                 "Project Helm chart repository exposed within a namespace",
	"disabled":         "If set to true, disable the repo usage in the namespace",
	"name":             "Optional associated human readable repository name, it can be used by UI for displaying purposes",
	"description":      "Optional human readable repository description, it can be used by UI for displaying purposes",
	"connectionConfig": "Required configuration for connecting to the chart repo",
}

func (ProjectHelmChartRepositorySpec) SwaggerDoc() map[string]string {
	return map_ProjectHelmChartRepositorySpec
}

// AUTO-GENERATED FUNCTIONS END HERE
