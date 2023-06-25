package mtq_lock_server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/client-go/tools/cache"
	"kubevirt.io/client-go/log"
	"kubevirt.io/managed-tenant-quota/pkg/mtq-lock-server/validation"
	"kubevirt.io/managed-tenant-quota/pkg/util"
	"net/http"
	"os"
)

type TargetVirtLauncherValidator struct {
	migrationInformer cache.SharedIndexInformer
	kubevirtInformer  cache.SharedIndexInformer
	mtqNS             string
}

func NewTargetLauncherValidator(mtqNS string) *TargetVirtLauncherValidator {
	return &TargetVirtLauncherValidator{
		migrationInformer: nil,
		kubevirtInformer:  nil,
		mtqNS:             mtqNS,
	}
}

func (tvlv *TargetVirtLauncherValidator) setInformers() {
	virtCli, err := util.GetVirtCli()
	if err != nil {
		log.Log.Error(err.Error())
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := ctx.Done()
	tvlv.kubevirtInformer = util.KubeVirtInformer(virtCli)
	tvlv.migrationInformer = util.GetMigrationInformer(virtCli)

	go tvlv.kubevirtInformer.Run(stop)
	go tvlv.migrationInformer.Run(stop)
	if !cache.WaitForCacheSync(stop, tvlv.kubevirtInformer.HasSynced, tvlv.migrationInformer.HasSynced) {
		log.Log.Error("couldn't sync vit informers")
		os.Exit(1)
	}

	log.Log.Infof("Virt Informers are set")
}

func (tvlv *TargetVirtLauncherValidator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if tvlv.kubevirtInformer == nil || tvlv.migrationInformer == nil {
		tvlv.setInformers()
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

	out, err := validator.Validate(tvlv.migrationInformer, util.GetKVNS(tvlv.kubevirtInformer), tvlv.mtqNS)
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
