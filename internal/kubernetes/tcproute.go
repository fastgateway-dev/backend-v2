package kubernetes

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// L4Backend is a weighted in-cluster Kubernetes Service backend for an L4
// (TCP/UDP) route. Only core Service backendRefs are supported: Envoy
// Gateway's Backend CRD is not referenceable from TCPRoute/UDPRoute.
type L4Backend struct {
	Service   string
	Namespace string
	Port      int
	Weight    int
}

// TCPRouteConfig holds configuration for building a v1alpha2 TCPRoute.
type TCPRouteConfig struct {
	Name        string
	Namespace   string
	GatewayName string
	// SectionName is the Gateway listener the route attaches to (e.g. "l4-tcp-5432").
	SectionName string
	Backends    []L4Backend
	Labels      map[string]string
}

// buildL4BackendRefs converts L4 backends into core-Service backendRefs
// (group/kind left unset so they default to core Service).
func buildL4BackendRefs(backends []L4Backend) []gatewayv1alpha2.BackendRef {
	refs := make([]gatewayv1alpha2.BackendRef, 0, len(backends))
	for _, b := range backends {
		port := gatewayv1alpha2.PortNumber(b.Port)
		ref := gatewayv1alpha2.BackendRef{
			BackendObjectReference: gatewayv1alpha2.BackendObjectReference{
				Name: gatewayv1alpha2.ObjectName(b.Service),
				Port: &port,
			},
		}
		if b.Namespace != "" {
			ns := gatewayv1alpha2.Namespace(b.Namespace)
			ref.Namespace = &ns
		}
		if b.Weight > 0 {
			weight := int32(b.Weight)
			ref.Weight = &weight
		}
		refs = append(refs, ref)
	}
	return refs
}

// buildL4ParentRef builds the single ParentReference attaching a route to a
// specific Gateway listener via sectionName.
func buildL4ParentRef(gatewayName, namespace, sectionName string) gatewayv1alpha2.ParentReference {
	ns := gatewayv1alpha2.Namespace(namespace)
	section := gatewayv1alpha2.SectionName(sectionName)
	return gatewayv1alpha2.ParentReference{
		Name:        gatewayv1alpha2.ObjectName(gatewayName),
		Namespace:   &ns,
		SectionName: &section,
	}
}

// BuildTCPRouteObject builds a typed TCPRoute from config at the given
// apiVersion (e.g. "gateway.networking.k8s.io/v1"). The v1alpha2 and v1 spec
// schemas are identical (straight graduation), so the v1alpha2 Go type
// serializes correctly under either apiVersion.
func BuildTCPRouteObject(cfg TCPRouteConfig, apiVersion string) *gatewayv1alpha2.TCPRoute {
	return &gatewayv1alpha2.TCPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apiVersion,
			Kind:       "TCPRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    cfg.Labels,
		},
		Spec: gatewayv1alpha2.TCPRouteSpec{
			CommonRouteSpec: gatewayv1alpha2.CommonRouteSpec{
				ParentRefs: []gatewayv1alpha2.ParentReference{
					buildL4ParentRef(cfg.GatewayName, cfg.Namespace, cfg.SectionName),
				},
			},
			Rules: []gatewayv1alpha2.TCPRouteRule{
				{BackendRefs: buildL4BackendRefs(cfg.Backends)},
			},
		},
	}
}
