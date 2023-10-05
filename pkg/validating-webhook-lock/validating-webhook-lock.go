package validating_webhook_lock

import (
	"bytes"
	"context"
	"fmt"
	v13 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubevirt.io/client-go/kubecli"
	"kubevirt.io/client-go/log"
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

func NamespaceLocked(nsToVerify string, mtqNs string, cli kubecli.KubevirtClient, caBundle []byte) (bool, error) {
	webhookName := "lock." + nsToVerify + ".com"
	ValidatingWH, err := cli.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(context.Background(), webhookName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, err
	} else if !isOurValidatingWebhookConfiguration(nsToVerify, mtqNs, ValidatingWH, caBundle) {
		ValidatingWH.Webhooks = []v13.ValidatingWebhook{getLockingValidatingWebhook(mtqNs, nsToVerify, caBundle)}
		_, err := cli.AdmissionregistrationV1().ValidatingWebhookConfigurations().Update(context.Background(), ValidatingWH, metav1.UpdateOptions{})
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func isOurValidatingWebhookConfiguration(mtqNs string, lockedNs string, existingVWHC *v13.ValidatingWebhookConfiguration, caBundle []byte) bool {
	expectedVWH := getLockingValidatingWebhook(mtqNs, lockedNs, caBundle)
	if len(existingVWHC.Webhooks) != 1 {
		return false
	}

	existingVWH := existingVWHC.Webhooks[0]
	expectedClientConfigSvc := expectedVWH.ClientConfig.Service
	existingClientConfigSvc := existingVWH.ClientConfig.Service
	expectedRules := expectedVWH.Rules
	existingRules := existingVWH.Rules

	if !bytes.Equal(existingVWH.ClientConfig.CABundle, expectedVWH.ClientConfig.CABundle) {
		log.Log.Infof("Found WHC doesn't match our CABundles do not match")
		return false
	}
	if existingVWH.Name != expectedVWH.Name {
		log.Log.Infof(fmt.Sprintf("Found WHC doesn't match our existingVWH.Name %v expectedVWH.Name %v ", existingVWH.Name, expectedVWH.Name))
		return false
	}
	if !reflect.DeepEqual(existingVWH.AdmissionReviewVersions, expectedVWH.AdmissionReviewVersions) {
		log.Log.Infof(fmt.Sprintf("Found WHC doesn't match our existingVWH.AdmissionReviewVersions %v expectedVWH.AdmissionReviewVersions %v ", existingVWH.AdmissionReviewVersions, expectedVWH.AdmissionReviewVersions))
		return false
	}
	if !reflect.DeepEqual(existingVWH.NamespaceSelector, expectedVWH.NamespaceSelector) {
		log.Log.Infof(fmt.Sprintf("Found WHC doesn't match our existingVWH.NamespaceSelector %v expectedVWH.AdmissionReviewVersions %v ", existingVWH.NamespaceSelector, expectedVWH.NamespaceSelector))
		return false
	}
	if existingVWH.FailurePolicy == nil || *existingVWH.FailurePolicy != *expectedVWH.FailurePolicy {
		log.Log.Infof(fmt.Sprintf("Found WHC doesn't match our *existingVWH.FailurePolicy  %v *expectedVWH.FailurePolicy %v ", *existingVWH.FailurePolicy, *expectedVWH.FailurePolicy))
		return false
	}
	if existingVWH.SideEffects == nil || *existingVWH.SideEffects != *expectedVWH.SideEffects {
		log.Log.Infof(fmt.Sprintf("Found WHC doesn't match our existingVWH.SideEffects %v expectedVWH.SideEffects %v ", existingVWH.SideEffects, expectedVWH.SideEffects))
		return false
	}
	if existingVWH.MatchPolicy == nil || *existingVWH.MatchPolicy != *expectedVWH.MatchPolicy {
		log.Log.Infof(fmt.Sprintf("Found WHC doesn't match our existingVWH.MatchPolicy %v expectedVWH.MatchPolicy %v ", existingVWH.MatchPolicy, expectedVWH.MatchPolicy))
		return false
	}
	if !reflect.DeepEqual(expectedClientConfigSvc, existingClientConfigSvc) {
		log.Log.Infof(fmt.Sprintf("Found WHC doesn't match our expectedClientConfigSvc %v existingClientConfigSvc %v ", expectedClientConfigSvc, existingClientConfigSvc))
		return false
	}
	if !reflect.DeepEqual(expectedRules, existingRules) {
		log.Log.Infof(fmt.Sprintf("Found WHC doesn't match our expectedRules %v existingRules %v ", expectedRules, existingRules))
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
