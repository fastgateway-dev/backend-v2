package services

import (
	"context"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// CertInfraApplier is the narrow control-plane role the certificate-infra
// services use to write cert-manager CRDs and DNS-solver Secrets to the
// backend's own (control) cluster. Satisfied by *cluster.ControlPlaneClient.
type CertInfraApplier interface {
	ApplyClusterScoped(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error
	ApplyNamespaced(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error
	Get(ctx context.Context, gvr schema.GroupVersionResource, name string, namespaced bool) (*unstructured.Unstructured, error)
	Delete(ctx context.Context, gvr schema.GroupVersionResource, name string, namespaced bool) error
	Namespace() string
}

// Compile-time role satisfaction check, in the same style as k8s_roles.go:
// *cluster.ControlPlaneClient is the single concrete implementation of this
// role. internal/services already imports internal/cluster elsewhere
// (see k8s_roles.go), so this assertion lives here rather than in a _test.go
// to avoid any import-cycle risk.
var _ CertInfraApplier = (*cluster.ControlPlaneClient)(nil)

// Compile-time role satisfaction check for TenantSecretDeleter
// (managed_certificate_service.go): *cluster.Client is the single concrete
// implementation, the same client passed elsewhere as certdist's
// TenantWriter.
var _ TenantSecretDeleter = (*cluster.Client)(nil)
