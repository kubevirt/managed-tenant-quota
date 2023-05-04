package mtq_webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/client-go/tools/cache"
	"kubevirt.io/client-go/log"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-webhook/validation"
	"net/http"
)

type TargetVirtLauncherValidator struct {
	migrationInformer cache.SharedIndexInformer
	kubevirtNS        string
	mtqNS             string
}

func NewTargetLauncherValidator(migrationInformer cache.SharedIndexInformer, kubevirtNS string, mtqNS string) *TargetVirtLauncherValidator {
	return &TargetVirtLauncherValidator{
		migrationInformer: migrationInformer,
		kubevirtNS:        kubevirtNS,
		mtqNS:             mtqNS,
	}
}

func (tvlv *TargetVirtLauncherValidator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	in, err := parseRequest(*r)
	if err != nil {
		log.Log.Error(err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	validator := validation.Validator{
		Request: in.Request,
	}

	//todo: replace to validator.Validate(tvlv.migrationInformer, tvlv.kubevirtNS, tvlv.mtqNS) once the operator is done
	out, err := validator.Validate(tvlv.migrationInformer, tvlv.kubevirtNS, tvlv.kubevirtNS)
	if err != nil {
		e := fmt.Sprintf("could not generate admission response: %v", err)
		log.Log.Error(err.Error())
		http.Error(w, e, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	jout, err := json.Marshal(out)
	if err != nil {
		e := fmt.Sprintf("could not parse admission response: %v", err)
		log.Log.Error(err.Error())
		http.Error(w, e, http.StatusInternalServerError)
		return
	}

	_, err = fmt.Fprintf(w, "%s", jout)
	if err != nil {
		log.Log.Error(err.Error())
	}

}

// parseRequest extracts an AdmissionReview from an http.Request if possible
func parseRequest(r http.Request) (*admissionv1.AdmissionReview, error) {
	if r.Header.Get("Content-Type") != "application/json" {
		return nil, fmt.Errorf("Content-Type: %q should be %q",
			r.Header.Get("Content-Type"), "application/json")
	}

	bodybuf := new(bytes.Buffer)
	_, err := bodybuf.ReadFrom(r.Body)
	if err != nil {
		return nil, err
	}
	body := bodybuf.Bytes()

	if len(body) == 0 {
		return nil, fmt.Errorf("admission request body is empty")
	}

	var a admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("could not parse admission review request: %v", err)
	}

	if a.Request == nil {
		return nil, fmt.Errorf("admission review can't be used: Request field is nil")
	}

	return &a, nil
}
