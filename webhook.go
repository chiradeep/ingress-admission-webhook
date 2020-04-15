package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/golang/glog"
	"k8s.io/api/admission/v1beta1"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
	networkingv1beta1 "k8s.io/api/networking/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/kubernetes/pkg/apis/core/v1"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()
)

var (
	ignoredNamespaces = []string{
		metav1.NamespaceSystem,
		metav1.NamespacePublic,
	}
)

const (
	admissionWebhookAnnotationValidateKey = "admission-webhook-example.citrix.com/validate"
	admissionWebhookAnnotationMutateKey   = "admission-webhook-example.citrix.com/mutate"
	admissionWebhookAnnotationStatusKey   = "admission-webhook-example.citrix.com/status"
)

type WebhookServer struct {
	server             *http.Server
	defaultAnnotations []map[string]interface{}
}

// Webhook Server parameters
type WhSvrParameters struct {
	port          int    // webhook server port
	certFile      string // path to the x509 certificate for https
	keyFile       string // path to the x509 private key matching `CertFile`
	annotationCfg string // path to annotation configuration file
}

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func init() {
	_ = corev1.AddToScheme(runtimeScheme)
	_ = admissionregistrationv1beta1.AddToScheme(runtimeScheme)
	// defaulting with webhooks:
	// https://github.com/kubernetes/kubernetes/issues/57982
	_ = v1.AddToScheme(runtimeScheme)
}

func admissionRequired(ignoredList []string, admissionAnnotationKey string, metadata *metav1.ObjectMeta) bool {
	// skip special kubernetes system namespaces
	for _, namespace := range ignoredList {
		if metadata.Namespace == namespace {
			glog.Infof("Skip validation for %v for it's in special namespace:%v", metadata.Name, metadata.Namespace)
			return false
		}
	}
	return true
}

func mutationRequired(ignoredList []string, defaultAnnotations []map[string]interface{}, metadata *metav1.ObjectMeta) bool {
	required := admissionRequired(ignoredList, admissionWebhookAnnotationMutateKey, metadata)
	annotations := metadata.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	name := metadata.GetName()
	ingressFound := false
	for _, dflt := range defaultAnnotations {
		ingressName, ok := dflt["ingressName"]
		if !ok {
			continue
		}
		glog.Infof("Checking default for %v/%v", ingressName.(string), metadata.Name)
		if strings.Compare(strings.ToLower(ingressName.(string)), strings.ToLower(name)) == 0 {
			ingressFound = true
			break
		}
	}
	required = required && ingressFound
	glog.Infof("Mutation policy for %v/%v: required:%v", metadata.Namespace, metadata.Name, required)

	status := annotations[admissionWebhookAnnotationStatusKey]

	if strings.ToLower(status) == "mutated" {
		required = false
	}

	glog.Infof("Mutation policy for %v/%v: required:%v", metadata.Namespace, metadata.Name, required)
	return required
}

func validationRequired(ignoredList []string, metadata *metav1.ObjectMeta) bool {
	required := admissionRequired(ignoredList, admissionWebhookAnnotationValidateKey, metadata)
	glog.Infof("Validation policy for %v/%v: required:%v", metadata.Namespace, metadata.Name, required)
	return required
}

func updateAnnotation(annotations map[string]string, defaultAnnotations map[string]interface{}) (patch []patchOperation) {

	for ann, val := range defaultAnnotations {
		annotations[ann] = val.(string)
	}
	patch = append(patch, patchOperation{
		Op:    "add",
		Path:  "/metadata/annotations",
		Value: annotations,
	})

	return patch
}

func createPatch(ingressName string, availableAnnotations map[string]string, allDefaultAnnotations []map[string]interface{}) ([]byte, error) {
	var patch []patchOperation

	defaultAnnotationsForIngressName := map[string]interface{}{}
	for _, dflt := range allDefaultAnnotations {
		name, ok := dflt["ingressName"]
		if !ok {
			continue
		}
		//ingressName := name.(string)
		if strings.Compare(strings.ToLower(dflt["ingressName"].(string)), strings.ToLower(ingressName)) == 0 {
			defaultAnnotationsForIngressName = dflt["defaultAnnotations"].(map[string]interface{})
			break
		}
	}
	patch = append(patch, updateAnnotation(availableAnnotations, defaultAnnotationsForIngressName)...)
	return json.Marshal(patch)
}

// main mutation process
func (whsvr *WebhookServer) mutate(ar *v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
	req := ar.Request
	var (
		objectMeta                      *metav1.ObjectMeta
		resourceNamespace, resourceName string
	)

	glog.Infof("Mutating AdmissionReview for Kind=%v, Namespace=%v Name=%v (%v) UID=%v patchOperation=%v UserInfo=%v",
		req.Kind, req.Namespace, req.Name, resourceName, req.UID, req.Operation, req.UserInfo)

	switch req.Kind.Kind {
	case "Ingress":
		var ingress networkingv1beta1.Ingress
		if err := json.Unmarshal(req.Object.Raw, &ingress); err != nil {
			glog.Errorf("Could not unmarshal raw object: %v", err)
			return &v1beta1.AdmissionResponse{
				Result: &metav1.Status{
					Message: err.Error(),
				},
			}
		}
		resourceName, resourceNamespace, objectMeta = ingress.Name, ingress.Namespace, &ingress.ObjectMeta

	}

	if !mutationRequired(ignoredNamespaces, whsvr.defaultAnnotations, objectMeta) {
		glog.Infof("Skipping validation for %s/%s due to policy check", resourceNamespace, resourceName)
		return &v1beta1.AdmissionResponse{
			Allowed: true,
		}
	}
	patchBytes, err := createPatch(resourceName, objectMeta.GetAnnotations(), whsvr.defaultAnnotations)
	if err != nil {
		return &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	glog.Infof("AdmissionResponse: patch=%v\n", string(patchBytes))
	return &v1beta1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *v1beta1.PatchType {
			pt := v1beta1.PatchTypeJSONPatch
			return &pt
		}(),
	}
}

// Serve method for webhook server
func (whsvr *WebhookServer) serve(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	if len(body) == 0 {
		glog.Error("empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		glog.Errorf("Content-Type=%s, expect application/json", contentType)
		http.Error(w, "invalid Content-Type, expect `application/json`", http.StatusUnsupportedMediaType)
		return
	}

	var admissionResponse *v1beta1.AdmissionResponse
	ar := v1beta1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &ar); err != nil {
		glog.Errorf("Can't decode body: %v", err)
		admissionResponse = &v1beta1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	} else {
		fmt.Println(r.URL.Path)
		if r.URL.Path == "/mutate" {
			admissionResponse = whsvr.mutate(&ar)
		} else if r.URL.Path == "/validate" {
			//admissionResponse = whsvr.validate(&ar)
			glog.Errorf("Not set up to do validation")
		}
	}

	admissionReview := v1beta1.AdmissionReview{}
	if admissionResponse != nil {
		admissionReview.Response = admissionResponse
		if ar.Request != nil {
			admissionReview.Response.UID = ar.Request.UID
		}
	}

	resp, err := json.Marshal(admissionReview)
	if err != nil {
		glog.Errorf("Can't encode response: %v", err)
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
	}
	glog.Infof("Ready to write reponse ...")
	if _, err := w.Write(resp); err != nil {
		glog.Errorf("Can't write response: %v", err)
		http.Error(w, fmt.Sprintf("could not write response: %v", err), http.StatusInternalServerError)
	}
}
