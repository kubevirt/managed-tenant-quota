package validating_webhook_lock

import (
	"context"
	"fmt"
	v13 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubevirt.io/client-go/kubecli"
	"reflect"
)

const (
	MTQLockServerServiceName = "mtq-lock"
)

func LockNamespace(nsToLock string, mtqNS string, cli kubecli.KubevirtClient, caBundle []byte) error {
	lockingValidationWebHook := v13.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "lock." + nsToLock + ".com",
			Labels: map[string]string{
				"mtq-lock-server": "mtq-lock-server",
			},
		},
		Webhooks: []v13.ValidatingWebhook{getLockingValidatingWebhook(mtqNS, nsToLock, caBundle)},
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

func NamespaceLocked(nsToVerify string, mtqNs string, cli kubecli.KubevirtClient) (bool, error) {
	webhookName := "lock." + nsToVerify + ".com"
	ValidatingWH, err := cli.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(context.Background(), webhookName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, err
	} else if !isOurValidatingWebhookConfiguration(nsToVerify, mtqNs, ValidatingWH) {
		return false, fmt.Errorf("ValidatingWebhookConfiguration with name " + webhookName + " and diffrent logic already exist")
	}
	return true, nil
}

func isOurValidatingWebhookConfiguration(mtqNs string, lockedNs string, existingVWHC *v13.ValidatingWebhookConfiguration) bool {
	expectedVWH := getLockingValidatingWebhook(mtqNs, lockedNs, []byte{})
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

func getLockingValidatingWebhook(mtqNs string, nsToLock string, caBundle []byte) v13.ValidatingWebhook {
	fail := v13.Fail
	sideEffects := v13.SideEffectClassNone
	equivalent := v13.Equivalent
	namespacedScope := v13.NamespacedScope
	path := "/validate-pods"
	port := int32(443)
	return v13.ValidatingWebhook{
		Name:                    "lock." + nsToLock + ".com",
		AdmissionReviewVersions: []string{"v1", "v1beta1"},
		FailurePolicy:           &fail,
		SideEffects:             &sideEffects,
		MatchPolicy:             &equivalent,
		NamespaceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{v1.LabelMetadataName: nsToLock},
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
				Namespace: mtqNs,
				Name:      MTQLockServerServiceName,
				Path:      &path,
				Port:      &port,
			},
			CABundle: caBundle,
		},
	}
}
