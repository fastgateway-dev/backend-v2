// Package gvr provides GroupVersionResource constants for Kubernetes resources
// used by FastGateway. This eliminates repetitive inline GVR definitions.
package kubernetes

import "k8s.io/apimachinery/pkg/runtime/schema"

// Core Kubernetes resources
var (
	// NamespaceGVR represents the core Namespace resource
	NamespaceGVR = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "namespaces",
	}

	// SecretGVR represents the core Secret resource
	SecretGVR = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	// ServiceGVR represents the core Service resource
	ServiceGVR = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "services",
	}

	// ConfigMapGVR represents the core ConfigMap resource
	ConfigMapGVR = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}

	// PodGVR represents the core Pod resource
	PodGVR = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "pods",
	}
)

// Apps resources
var (
	// DeploymentGVR represents the apps/v1 Deployment resource
	DeploymentGVR = schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "deployments",
	}
)

// Gateway API resources (gateway.networking.k8s.io)
var (
	// GatewayGVR represents the Gateway resource
	GatewayGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gateways",
	}

	// GatewayClassGVR represents the GatewayClass resource
	GatewayClassGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "gatewayclasses",
	}

	// HTTPRouteGVR represents the HTTPRoute resource
	HTTPRouteGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1",
		Resource: "httproutes",
	}

	// ReferenceGrantGVR represents the ReferenceGrant resource
	ReferenceGrantGVR = schema.GroupVersionResource{
		Group:    "gateway.networking.k8s.io",
		Version:  "v1beta1",
		Resource: "referencegrants",
	}
)

// Envoy Gateway resources (gateway.envoyproxy.io)
var (
	// SecurityPolicyGVR represents the Envoy Gateway SecurityPolicy resource
	SecurityPolicyGVR = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "securitypolicies",
	}

	// BackendTrafficPolicyGVR represents the Envoy Gateway BackendTrafficPolicy resource
	BackendTrafficPolicyGVR = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "backendtrafficpolicies",
	}

	// ClientTrafficPolicyGVR represents the Envoy Gateway ClientTrafficPolicy resource
	ClientTrafficPolicyGVR = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "clienttrafficpolicies",
	}

	// BackendGVR represents the Envoy Gateway Backend resource
	BackendGVR = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "backends",
	}

	// EnvoyProxyGVR represents the Envoy Gateway EnvoyProxy resource
	EnvoyProxyGVR = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "envoyproxies",
	}

	// HTTPRouteFilterGVR represents the Envoy Gateway HTTPRouteFilter resource
	HTTPRouteFilterGVR = schema.GroupVersionResource{
		Group:    "gateway.envoyproxy.io",
		Version:  "v1alpha1",
		Resource: "httproutefilters",
	}
)
