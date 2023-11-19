package mtq_lock_server

import (
	"bytes"
	"encoding/json"
	"fmt"
	admissionv1 "k8s.io/api/admission/v1"
	"kubevirt.io/client-go/log"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-lock-server/validation"
	"kubevirt.io/managed-tenant-quota/pkg/util"
	"net/http"
)

type TargetVirtLauncherValidator struct {
	mtqNS string
	kvNS  *string
}

func NewTargetLauncherValidator(mtqNS string) *TargetVirtLauncherValidator {
	return &TargetVirtLauncherValidator{
		mtqNS: mtqNS,
		kvNS:  nil,
	}
}

func (tvlv *TargetVirtLauncherValidator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if tvlv.kvNS == nil {
		tvlv.kvNS = getKVNS()
	}
	in, err := parseRequest(*r)
	if err != nil {
		log.Log.Error(err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	validator := validation.Validator{
		Request: in.Request,
	}

	out, err := validator.Validate(*tvlv.kvNS, tvlv.mtqNS)
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

// parseRequest extracts an AdmissionReview from an http.Request if possible.
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

var getKVNS = func() *string {
	return util.GetKVNS()
}
