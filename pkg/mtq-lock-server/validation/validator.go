// Package admission handles kubernetes admissions,
// it takes admission requests and returns admission reviews;
// for example, to mutate or validate pods
package validation

import (
	"encoding/json"
	"fmt"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	virtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-operator/resources/utils"
	"net/http"
)

type Validator struct {
	Request *admissionv1.AdmissionRequest
}

const (
	InvalidPodCreationErrorMessage           = "Migration process is currently being handled by the Managed Quota controller, and as a result, creations of pods are not allowed in this namespace, please try again."
	ReasonFoForbiddenVMMRQCreationOrDeletion = "Migration process is currently being handled by the Managed Quota controller, and as a result, modifications,creation or deletion of virtualMachineMigrationResourceQuotas are not permitted in this namespace, please try again."
	ReasonFoForbiddenRQUpdate                = "Migration process is currently being handled by the Managed Quota controller, and as a result, modifications to resourceQuotas are not permitted in this namespace, please try again."
	reasonForAcceptedRQUpdate                = "valid ResourceQuota Update"
	reasonForAcceptedVMMRQUpdate             = "valid VirtualMachineMigrationResourceQuota Update"
	VirtControllerServiceAccountName         = "kubevirt-controller"
	MtqContollerServiceAccountName           = utils.ControllerPodName
)

func (v Validator) Validate(kubevirtNS string, mtqNS string) (*admissionv1.AdmissionReview, error) {
	switch v.Request.Kind.Kind {
	case "VirtualMachineMigrationResourceQuota":
		return v.validateRQCtlModification(mtqNS, ReasonFoForbiddenVMMRQCreationOrDeletion, reasonForAcceptedVMMRQUpdate)
	case "ResourceQuota":
		return v.validateRQCtlModification(mtqNS, ReasonFoForbiddenRQUpdate, reasonForAcceptedRQUpdate)
	case "Pod":
		return v.validateTargetVirtLauncherPod(kubevirtNS)
	}
	return nil, fmt.Errorf("MTQ webhook doesn't recongnize request: %+v", v.Request)
}
func (v Validator) validateRQCtlModification(mtqNS string, reasonFoForbidden string, reasonForAccepted string) (*admissionv1.AdmissionReview, error) {
	if isMTQControllerServiceAccount(v.Request.UserInfo.Username, mtqNS) {
		return reviewResponse(v.Request.UID, true, http.StatusAccepted, reasonForAccepted), nil
	}
	return reviewResponse(v.Request.UID, false, http.StatusForbidden, reasonFoForbidden), nil
}

func (v Validator) validateTargetVirtLauncherPod(kubevirtNS string) (*admissionv1.AdmissionReview, error) {
	if !isVirtControllerServiceAccount(v.Request.UserInfo.Username, kubevirtNS) {
		return reviewResponse(v.Request.UID, false, http.StatusForbidden, InvalidPodCreationErrorMessage), nil
	}
	pod, err := v.getPod()
	if err != nil {
		return nil, err
	}
	_, belongToMigration := pod.Labels[virtv1.MigrationJobLabel]
	if !belongToMigration {
		return reviewResponse(v.Request.UID, false, http.StatusForbidden, InvalidPodCreationErrorMessage), nil
	}

	return reviewResponse(v.Request.UID, true, http.StatusAccepted, "valid target virt-launcher"), nil
}

func (v Validator) getPod() (*corev1.Pod, error) {
	p := corev1.Pod{}
	if err := json.Unmarshal(v.Request.Object.Raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func reviewResponse(uid types.UID, allowed bool, httpCode int32,
	reason string) *admissionv1.AdmissionReview {
	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &admissionv1.AdmissionResponse{
			UID:     uid,
			Allowed: allowed,
			Result: &metav1.Status{
				Code:    httpCode,
				Message: reason,
			},
		},
	}
}

func isVirtControllerServiceAccount(serviceAccount string, kubevirtNS string) bool {
	prefix := fmt.Sprintf("system:serviceaccount:%s", kubevirtNS)
	return serviceAccount == fmt.Sprintf("%s:%s", prefix, VirtControllerServiceAccountName)
}

func isMTQControllerServiceAccount(serviceAccount string, mtqNS string) bool {
	prefix := fmt.Sprintf("system:serviceaccount:%s", mtqNS)
	return serviceAccount == fmt.Sprintf("%s:%s", prefix, MtqContollerServiceAccountName)
}
