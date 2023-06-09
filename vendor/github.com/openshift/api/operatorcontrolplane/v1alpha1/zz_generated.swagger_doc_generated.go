package v1alpha1

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
var map_LogEntry = map[string]string{
	"":        "LogEntry records events",
	"time":    "Start time of check action.",
	"success": "Success indicates if the log entry indicates a success or failure.",
	"reason":  "Reason for status in a machine readable format.",
	"message": "Message explaining status in a human readable format.",
	"latency": "Latency records how long the action mentioned in the entry took.",
}

func (LogEntry) SwaggerDoc() map[string]string {
	return map_LogEntry
}

var map_OutageEntry = map[string]string{
	"":          "OutageEntry records time period of an outage",
	"start":     "Start of outage detected",
	"end":       "End of outage detected",
	"startLogs": "StartLogs contains log entries related to the start of this outage. Should contain the original failure, any entries where the failure mode changed.",
	"endLogs":   "EndLogs contains log entries related to the end of this outage. Should contain the success entry that resolved the outage and possibly a few of the failure log entries that preceded it.",
	"message":   "Message summarizes outage details in a human readable format.",
}

func (OutageEntry) SwaggerDoc() map[string]string {
	return map_OutageEntry
}

var map_PodNetworkConnectivityCheck = map[string]string{
	"":         "PodNetworkConnectivityCheck\n\nCompatibility level 4: No compatibility is provided, the API can change at any point for any reason. These capabilities should not be used by applications needing long term support.",
	"metadata": "metadata is the standard object's metadata. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata",
	"spec":     "Spec defines the source and target of the connectivity check",
	"status":   "Status contains the observed status of the connectivity check",
}

func (PodNetworkConnectivityCheck) SwaggerDoc() map[string]string {
	return map_PodNetworkConnectivityCheck
}

var map_PodNetworkConnectivityCheckCondition = map[string]string{
	"":                   "PodNetworkConnectivityCheckCondition represents the overall status of the pod network connectivity.",
	"type":               "Type of the condition",
	"status":             "Status of the condition",
	"reason":             "Reason for the condition's last status transition in a machine readable format.",
	"message":            "Message indicating details about last transition in a human readable format.",
	"lastTransitionTime": "Last time the condition transitioned from one status to another.",
}

func (PodNetworkConnectivityCheckCondition) SwaggerDoc() map[string]string {
	return map_PodNetworkConnectivityCheckCondition
}

var map_PodNetworkConnectivityCheckList = map[string]string{
	"":         "PodNetworkConnectivityCheckList is a collection of PodNetworkConnectivityCheck\n\nCompatibility level 4: No compatibility is provided, the API can change at any point for any reason. These capabilities should not be used by applications needing long term support.",
	"metadata": "metadata is the standard list's metadata. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata",
	"items":    "Items contains the items",
}

func (PodNetworkConnectivityCheckList) SwaggerDoc() map[string]string {
	return map_PodNetworkConnectivityCheckList
}

var map_PodNetworkConnectivityCheckSpec = map[string]string{
	"sourcePod":      "SourcePod names the pod from which the condition will be checked",
	"targetEndpoint": "EndpointAddress to check. A TCP address of the form host:port. Note that if host is a DNS name, then the check would fail if the DNS name cannot be resolved. Specify an IP address for host to bypass DNS name lookup.",
	"tlsClientCert":  "TLSClientCert, if specified, references a kubernetes.io/tls type secret with 'tls.crt' and 'tls.key' entries containing an optional TLS client certificate and key to be used when checking endpoints that require a client certificate in order to gracefully preform the scan without causing excessive logging in the endpoint process. The secret must exist in the same namespace as this resource.",
}

func (PodNetworkConnectivityCheckSpec) SwaggerDoc() map[string]string {
	return map_PodNetworkConnectivityCheckSpec
}

var map_PodNetworkConnectivityCheckStatus = map[string]string{
	"successes":  "Successes contains logs successful check actions",
	"failures":   "Failures contains logs of unsuccessful check actions",
	"outages":    "Outages contains logs of time periods of outages",
	"conditions": "Conditions summarize the status of the check",
}

func (PodNetworkConnectivityCheckStatus) SwaggerDoc() map[string]string {
	return map_PodNetworkConnectivityCheckStatus
}

// AUTO-GENERATED FUNCTIONS END HERE
