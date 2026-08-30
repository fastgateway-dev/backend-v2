// Package gvr provides GroupVersionResource constants for Kubernetes resources
// used by FastGateway. This eliminates repetitive inline GVR definitions.
package gvr

import "k8s.io/apimachinery/pkg/runtime/schema"

// Core Kubernetes resources
var (
	// Namespace represents the core Namespace resource
	Namespace = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	// Secret represents the core Secret resource
	Secret = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	// Service represents the core Service resource
	Service = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "services",
	}

	// ConfigMap represents the core ConfigMap resource
	ConfigMap = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}

	// Pod represents the core Pod resource
	Pod = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "pods",
	}
)

// Apps resources
var (
	// Deployment represents the apps/v1 Deployment resource
	Deployment = schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "deployments",
	}
)

// Gateway API resources (gateway.networking.k8s.io)
var (
	// Gateway represents the Gateway resource
	Gateway = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gateways",
	}

	// GatewayClass represents the GatewayClass resource
	GatewayClass = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gatewayclasses",
	}

	// HTTPRoute represents the HTTPRoute resource
	HTTPRoute = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}

	// ReferenceGrant represents the ReferenceGrant resource
	ReferenceGrant = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1beta1",
		Resource: "referencegrants",
	}
)

// Envoy Gateway resources (gateway.envoyproxy.io)
var (
	// SecurityPolicy represents the Envoy Gateway SecurityPolicy resource
	SecurityPolicy = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "securitypolicies",
	}

	// BackendTrafficPolicy represents the Envoy Gateway BackendTrafficPolicy resource
	BackendTrafficPolicy = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "backendtrafficpolicies",
	}

	// ClientTrafficPolicy represents the Envoy Gateway ClientTrafficPolicy resource
	ClientTrafficPolicy = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "clienttrafficpolicies",
	}

	// Backend represents the Envoy Gateway Backend resource
	Backend = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "backends",
	}

	// EnvoyProxy represents the Envoy Gateway EnvoyProxy resource
	EnvoyProxy = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "envoyproxies",
	}

	// HTTPRouteFilter represents the Envoy Gateway HTTPRouteFilter resource
	HTTPRouteFilter = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "httproutefilters",
	}
)
