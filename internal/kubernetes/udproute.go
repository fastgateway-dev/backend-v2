package kubernetes

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// UDPRouteConfig holds configuration for building a v1alpha2 UDPRoute.
type UDPRouteConfig struct {
	Name        string
	Namespace   string
	GatewayName string
	// SectionName is the Gateway listener the route attaches to (e.g. "l4-udp-53").
	SectionName string
	Backends    []L4Backend
	Labels      map[string]string
}

// BuildUDPRouteObject builds a typed UDPRoute from config at the given
// apiVersion (e.g. "gateway.networking.k8s.io/v1"). The v1alpha2 and v1 spec
// schemas are identical (straight graduation), so the v1alpha2 Go type
// serializes correctly under either apiVersion.
func BuildUDPRouteObject(cfg UDPRouteConfig, apiVersion string) *gatewayv1alpha2.UDPRoute {
	return &gatewayv1alpha2.UDPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apiVersion,
			Kind:       "UDPRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    cfg.Labels,
		},
		Spec: gatewayv1alpha2.UDPRouteSpec{
			CommonRouteSpec: gatewayv1alpha2.CommonRouteSpec{
				ParentRefs: []gatewayv1alpha2.ParentReference{
					buildL4ParentRef(cfg.GatewayName, cfg.Namespace, cfg.SectionName),
				},
			},
			Rules: []gatewayv1alpha2.UDPRouteRule{
				{BackendRefs: buildL4BackendRefs(cfg.Backends)},
			},
		},
	}
}
