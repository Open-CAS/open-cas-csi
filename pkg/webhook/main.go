/*
Copyright 2024 The Kubernetes Authors.
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package webhook

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const (
	Label         = "csi.open-cas.com/protected"
	RejectMessage = "objects with label '" + Label + "' are managed by Open-CAS CSI driver"
)

var (
	tlscert, tlskey, port string
)

func init() {
	flag.StringVar(&tlscert, "tlscert", "/etc/certs/tls.crt", "Path to the TLS certificate for HTTPS")
	flag.StringVar(&tlskey, "tlskey", "/etc/certs/tls.key", "Path to the TLS private key for HTTPS")
	flag.StringVar(&port, "port", "8443", "The port on which to listen")
}

func Main() int {
	flag.Parse()

	klog.Infof("Starting with args: port=%s, tlscert=%s, tlskey=%s", port, tlscert, tlskey)

	http.HandleFunc("/validate", Validate)
	if err := http.ListenAndServeTLS(":"+port, tlscert, tlskey, nil); err != nil {
		return 1
	}

	return 0
}

type Object struct {
	Kind     string   `json:"kind"`
	Metadata Metadata `json:"metadata"`
}

type Metadata struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels"`
}

func Validate(w http.ResponseWriter, req *http.Request) {
	ar := admissionv1.AdmissionReview{}
	if err := json.NewDecoder(req.Body).Decode(&ar); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if ar.Request == nil {
		http.Error(w, "empty request body", http.StatusBadRequest)
		return
	}
	klog.Infof("Request (%q) received", ar.Request.UID)
	klog.V(8).Infof("REQUEST: %+v", ar.Request)

	raw := ar.Request.OldObject.Raw

	klog.V(8).Infof("RAW DATA: %v", string(raw[:]))

	object := Object{}
	if err := json.Unmarshal(raw, &object); err != nil {
		klog.Error(err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if object.Metadata.Name == "" {
		klog.Info(object)
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	klog.V(8).Infof("%+v", object)

	ar.Response = &admissionv1.AdmissionResponse{
		UID:     ar.Request.UID,
		Allowed: true,
	}

	if _, ok := object.Metadata.Labels[Label]; ok {
		ar.Response.Allowed = false
		ar.Response.Result = &metav1.Status{
			Message: RejectMessage,
		}
	}

	status := "approved"
	if !ar.Response.Allowed {
		status = "rejected"
	}
	namespacedName, _ := strings.CutPrefix(fmt.Sprintf("%s/%s", object.Metadata.Namespace, object.Metadata.Name), "/")
	klog.Infof("Request (%q) for %s %s %s", ar.Request.UID, object.Kind, namespacedName, status)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(&ar)
}
