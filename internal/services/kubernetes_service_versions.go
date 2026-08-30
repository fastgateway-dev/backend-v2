package services

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(-[\w.]+)?$`)

// ParseImageTagVersion extracts a semver string from a container image reference.
// Returns "" for non-semver tags ("latest", "dev", digest references, missing tag).
func ParseImageTagVersion(image string) string {
	if image == "" {
		return ""
	}
	if strings.Contains(image, "@") {
		return ""
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon < 0 || lastColon < lastSlash {
		return ""
	}
	tag := image[lastColon+1:]
	tag = strings.TrimPrefix(tag, "v")
	if !semverPattern.MatchString(tag) {
		return ""
	}
	return tag
}

// ParseBundleVersion validates and normalizes a Gateway API CRD bundle-version
// annotation value. Returns "" when the value is not semver shape.
func ParseBundleVersion(annotation string) string {
	v := strings.TrimPrefix(annotation, "v")
	if !semverPattern.MatchString(v) {
		return ""
	}
	return v
}

// RawVersions is the unprocessed result of probing a cluster for EG and Gateway API versions.
// EGError and GWError are each populated independently when that probe fails to produce a
// parseable version. Errors aggregates both for callers that just want the union.
type RawVersions struct {
	EGVersion string
	EGImage   string
	EGSource  string
	EGError   string
	GWVersion string
	GWSource  string
	GWError   string
	Errors    []string
}

const detectionTimeout = 10 * time.Second
const envoyGatewayNamespace = "envoy-gateway-system"
const envoyGatewayPrimaryDeployment = "envoy-gateway"

var envoyGatewayImagePrefixes = []string{"/envoyproxy/gateway", "/envoy-gateway"}

// NewKubernetesServiceWithClient builds a KubernetesService that returns the given
// dynamic.Interface from getClientFor regardless of projectID. Intended for tests.
func NewKubernetesServiceWithClient(client dynamic.Interface) *KubernetesService {
	return &KubernetesService{testClient: client}
}

// DetectVersions probes the project's cluster for the installed Envoy Gateway operator
// version and Gateway API version. It returns a populated RawVersions even on partial
// failures — error is only non-nil for unexpected programming errors. Per-probe failures
// land in Errors and leave the corresponding *Version field empty.
func (s *KubernetesService) DetectVersions(ctx context.Context, projectID uuid.UUID) (*RawVersions, error) {
	ctx, cancel := context.WithTimeout(ctx, detectionTimeout)
	defer cancel()

	client, err := s.getClientFor(projectID)
	if err != nil {
		msg := fmt.Sprintf("kubeconfig error: %v", err)
		return &RawVersions{EGError: msg, GWError: msg, Errors: []string{msg}}, nil
	}

	out := &RawVersions{}
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		image, source, errStr := detectEGImage(ctx, client)
		out.EGImage = image
		out.EGSource = source
		out.EGVersion = ParseImageTagVersion(image)
		switch {
		case errStr != "":
			out.EGError = errStr
		case image != "" && out.EGVersion == "":
			out.EGError = fmt.Sprintf("image tag in %q is not a semantic version", image)
		}
	}()
	go func() {
		defer wg.Done()
		raw, errStr := detectGatewayAPIVersion(ctx, client)
		out.GWVersion = ParseBundleVersion(raw)
		if out.GWVersion != "" {
			out.GWSource = "crd/gateways.gateway.networking.k8s.io"
		}
		switch {
		case errStr != "":
			out.GWError = errStr
		case raw != "" && out.GWVersion == "":
			out.GWError = fmt.Sprintf("bundle-version annotation %q is not a semantic version", raw)
		}
	}()
	wg.Wait()

	// Aggregate per-side errors into the legacy Errors slice for callers that want the union.
	if out.EGError != "" {
		out.Errors = append(out.Errors, out.EGError)
	}
	if out.GWError != "" {
		out.Errors = append(out.Errors, out.GWError)
	}
	return out, nil
}

func (s *KubernetesService) getClientFor(projectID uuid.UUID) (dynamic.Interface, error) {
	if s.testClient != nil {
		return s.testClient, nil
	}
	return s.getClient(projectID)
}

func deploymentsGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
}

func crdsGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
}

func detectEGImage(ctx context.Context, client dynamic.Interface) (image, source, errStr string) {
	gvr := deploymentsGVR()
	d, err := client.Resource(gvr).Namespace(envoyGatewayNamespace).Get(ctx, envoyGatewayPrimaryDeployment, metav1.GetOptions{})
	if err == nil {
		if img := firstContainerImage(d); img != "" {
			return img, "deployment/" + envoyGatewayPrimaryDeployment, ""
		}
	}
	list, err := client.Resource(gvr).Namespace(envoyGatewayNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", "", fmt.Sprintf("list deployments in %s: %v", envoyGatewayNamespace, err)
	}
	for _, item := range list.Items {
		img := firstContainerImage(&item)
		if matchesEGImage(img) {
			return img, "deployment/" + item.GetName(), ""
		}
	}
	return "", "", fmt.Sprintf("no envoy-gateway deployment found in namespace %q", envoyGatewayNamespace)
}

func detectGatewayAPIVersion(ctx context.Context, client dynamic.Interface) (raw, errStr string) {
	gvr := crdsGVR()
	for _, name := range []string{"gateways.gateway.networking.k8s.io", "httproutes.gateway.networking.k8s.io"} {
		crd, err := client.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			continue
		}
		annotations, _, _ := unstructured.NestedStringMap(crd.Object, "metadata", "annotations")
		if v, ok := annotations["gateway.networking.k8s.io/bundle-version"]; ok && v != "" {
			return v, ""
		}
		return "", fmt.Sprintf("%s has no bundle-version annotation", name)
	}
	return "", "no Gateway API CRDs found"
}

func firstContainerImage(d *unstructured.Unstructured) string {
	containers, _, _ := unstructured.NestedSlice(d.Object, "spec", "template", "spec", "containers")
	if len(containers) == 0 {
		return ""
	}
	c, ok := containers[0].(map[string]interface{})
	if !ok {
		return ""
	}
	img, _ := c["image"].(string)
	return img
}

func matchesEGImage(image string) bool {
	for _, p := range envoyGatewayImagePrefixes {
		if strings.Contains(image, p+":") || strings.Contains(image, p+"@") {
			return true
		}
	}
	return false
}
