package validation_webhook_lock

import (
	"context"
	"encoding/base64"
	"fmt"
	v13 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubevirt.io/client-go/kubecli"
	"reflect"
)

func LockNamespace(ns string, cli kubecli.KubevirtClient) error {
	lockingValidationWebHook := v13.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "lock." + ns + ".com",
			Labels: map[string]string{
				"mtq-webhook": "mtq-webhook",
			},
		},
		Webhooks: []v13.ValidatingWebhook{getLockingValidatingWebhook(ns)},
	}
	_, err := cli.AdmissionregistrationV1().ValidatingWebhookConfigurations().Create(context.Background(), &lockingValidationWebHook, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

func UnlockNamespace(ns string, cli kubecli.KubevirtClient) error {
	err := cli.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(context.Background(), "lock."+ns+".com", metav1.DeleteOptions{})
	if err != nil {
		return err
	}
	return nil
}

func NamespaceLocked(ns string, cli kubecli.KubevirtClient) (bool, error) {
	webhookName := "lock." + ns + ".com"
	ValidatingWH, err := cli.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(context.Background(), webhookName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, err
	} else if !isOurValidatingWebhookConfiguration(ValidatingWH, ns) {
		return false, fmt.Errorf("ValidatingWebhookConfiguration with name " + webhookName + " and diffrent logic already exist")
	}
	return true, nil
}

func isOurValidatingWebhookConfiguration(existingVWHC *v13.ValidatingWebhookConfiguration, ns string) bool {
	expectedVWH := getLockingValidatingWebhook(ns)
	if len(existingVWHC.Webhooks) != 1 {
		return false
	}

	existingVWH := existingVWHC.Webhooks[0]

	expectedClientConfigSvc := expectedVWH.ClientConfig.Service
	existingClientConfigSvc := existingVWH.ClientConfig.Service
	expectedRules := expectedVWH.Rules
	existingRules := existingVWH.Rules

	if existingVWH.Name != expectedVWH.Name ||
		!reflect.DeepEqual(existingVWH.AdmissionReviewVersions, expectedVWH.AdmissionReviewVersions) ||
		!reflect.DeepEqual(existingVWH.NamespaceSelector, expectedVWH.NamespaceSelector) ||
		*existingVWH.FailurePolicy != *expectedVWH.FailurePolicy ||
		*existingVWH.SideEffects != *expectedVWH.SideEffects ||
		*existingVWH.MatchPolicy != *expectedVWH.MatchPolicy {

		return false
	}
	if !reflect.DeepEqual(expectedClientConfigSvc, existingClientConfigSvc) {
		return false
	}

	if !reflect.DeepEqual(expectedRules, existingRules) {
		return false
	}

	return true
}

func getLockingValidatingWebhook(ns string) v13.ValidatingWebhook {
	fail := v13.Fail
	sideEffects := v13.SideEffectClassNone
	equivalent := v13.Equivalent
	namespacedScope := v13.NamespacedScope
	path := "/validate-pods"
	port := int32(443)
	//TODO: add this dynamically once the operator is ready
	caBundle, _ := base64.StdEncoding.DecodeString("LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURkRENDQWx5Z0F3SUJBZ0lVVnA3Q0FQZ2VLcmU5OUNoeVh3emk4SVN6VFA0d0RRWUpLb1pJaHZjTkFRRUwKQlFBd1VqRUxNQWtHQTFVRUJoTUNRVlV4RURBT0JnTlZCQWdUQjBWNFlXMXdiR1V4RWpBUUJnTlZCQWNUQ1UxbApiR0p2ZFhKdVpURVFNQTRHQTFVRUNoTUhSWGhoYlhCc1pURUxNQWtHQTFVRUN4TUNRMEV3SGhjTk1qTXdORE13Ck1EY3hOREF3V2hjTk1qZ3dOREk0TURjeE5EQXdXakJTTVFzd0NRWURWUVFHRXdKQlZURVFNQTRHQTFVRUNCTUgKUlhoaGJYQnNaVEVTTUJBR0ExVUVCeE1KVFdWc1ltOTFjbTVsTVJBd0RnWURWUVFLRXdkRmVHRnRjR3hsTVFzdwpDUVlEVlFRTEV3SkRRVENDQVNJd0RRWUpLb1pJaHZjTkFRRUJCUUFEZ2dFUEFEQ0NBUW9DZ2dFQkFLVU5VQnBQCmtaT0l1QkE3MFV0Mk1VUG0yYXF1dExtNzZhcFg2Ykl2aURkYVJqVXhLU3c3TVV2RmVsUVNzL010YkVNSk5jN2kKN1FtOTVXdzdwV3l2eXZUS1N1eDJtV09tS3ZwMlRma0o4R2ZhVDhEMENPUU03cGhubHNZNHp5L0JBYTUrNU9heApKV3N4K0E4V2JWTS9rTnJhenBlVXFOTEQ2aU9QOFBNb2ZsTnJidXdTbCtPMFMrMUVOUE1wQ2hPTmM5OGdSQy9TCndaZkVUTW9XMy9aVUxoMjRjSkY0MVFNanU2YklFWCt4QlRjYTlad2lxc2dTSGthZi9XeklhMDA0UnRLTkU5cWsKNUtYMThhTXMwNXhPQTZsSHVXeUxaRFdXbTI2aFc0Z3ZrUmFrZVZIdVl3Q2IwZ0FDVUcrVkEyd1c1bWhUTm96eAphTm1xSm1IMTViaUdvODBDQXdFQUFhTkNNRUF3RGdZRFZSMFBBUUgvQkFRREFnRUdNQThHQTFVZEV3RUIvd1FGCk1BTUJBZjh3SFFZRFZSME9CQllFRkIyTXF2TXd3Y2E1ZWpHQlN6OG1aT29GcEJ2dk1BMEdDU3FHU0liM0RRRUIKQ3dVQUE0SUJBUUJqYlhLQXQwNGlQUC9NWUZmWFoxeHIwRDJnNnRRWlE1Lzk1SXhPbFVHVEdycDZCc3dFVG9UWQpneHhZU0VEVnZMVTU2RzZwZENBUklMVCtUVnB2YVVKNlV0UlJFbHF1ZFRqK3lwREhQRzdSaW9ONE1xYVlxWisrCkFjdDhwNGhKY3dCLzU1Yll6TU0vUlNBUDNyUDhSTm13Si9VUXNkczdBZElkaTZzRE9ZR0ZUN3FMbXlYdUQ0TU8KVDA0dEozazJVcW5wb2Z0QmM0ZlpsZ1JPTkZDRW9jWFlEanFlODdiU1F4K3k1eGlrb1FBZUt2VU1SMHBSZ0RUTwpMcHY3QWEvazQyZTI5MmlTM2xFZ004N1ROZ1d4T3dYK3FTZEhPQmxscnU1R09Qa0dTaHRsS2Fha0NRaW54cUNzCkE1ZjUyOFdLenpwbjNWM0J5eUhJRElXa0dZck1aNEtlCi0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K")
	return v13.ValidatingWebhook{
		Name:                    "lock." + ns + ".com",
		AdmissionReviewVersions: []string{"v1", "v1beta1"},
		FailurePolicy:           &fail,
		SideEffects:             &sideEffects,
		MatchPolicy:             &equivalent,
		NamespaceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{v1.LabelMetadataName: ns},
		},
		Rules: []v13.RuleWithOperations{
			{
				Operations: []v13.OperationType{
					v13.Update,
				},
				Rule: v13.Rule{
					APIGroups:   []string{"*"},
					APIVersions: []string{"*"},
					Scope:       &namespacedScope,
					Resources:   []string{"resourcequotas"},
				},
			},
			{
				Operations: []v13.OperationType{
					v13.Delete, v13.Create,
				},
				Rule: v13.Rule{
					APIGroups:   []string{"*"},
					APIVersions: []string{"*"},
					Scope:       &namespacedScope,
					Resources:   []string{"virtualmachinemigrationresourcequotas"},
				},
			},
			{
				Operations: []v13.OperationType{
					v13.Create,
				},
				Rule: v13.Rule{
					APIGroups:   []string{"*"},
					APIVersions: []string{"*"},
					Scope:       &namespacedScope,
					Resources:   []string{"pods"},
				},
			},
		},
		ClientConfig: v13.WebhookClientConfig{
			Service: &v13.ServiceReference{
				Namespace: "default",
				Name:      "example-webhook",
				Path:      &path,
				Port:      &port,
			},
			CABundle: caBundle,
		},
	}
}
