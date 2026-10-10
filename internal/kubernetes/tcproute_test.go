package kubernetes_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

func TestBuildTCPRouteObject_ParentRefUsesSectionName(t *testing.T) {
	obj := kubernetes.BuildTCPRouteObject(kubernetes.TCPRouteConfig{
		Name: "r", Namespace: "ns", GatewayName: "str-s", SectionName: "l4-tcp-5432",
		Backends: []kubernetes.L4Backend{{Service: "pg", Namespace: "ns", Port: 5432, Weight: 100}},
		Labels:   map[string]string{"a": "b"},
	}, "gateway.networking.k8s.io/v1alpha2")
	assert.Equal(t, "gateway.networking.k8s.io/v1alpha2", obj.APIVersion)
	assert.Equal(t, "TCPRoute", obj.Kind)
	assert.Equal(t, "r", obj.Name)
	assert.Equal(t, "ns", obj.Namespace)
	assert.Equal(t, map[string]string{"a": "b"}, obj.Labels)
	require.Len(t, obj.Spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1alpha2.ObjectName("str-s"), obj.Spec.ParentRefs[0].Name)
	require.NotNil(t, obj.Spec.ParentRefs[0].SectionName)
	assert.Equal(t, gatewayv1alpha2.SectionName("l4-tcp-5432"), *obj.Spec.ParentRefs[0].SectionName)
	require.Len(t, obj.Spec.Rules, 1)
	require.Len(t, obj.Spec.Rules[0].BackendRefs, 1)
	ref := obj.Spec.Rules[0].BackendRefs[0]
	assert.Equal(t, int32(100), *ref.Weight)
	assert.Equal(t, gatewayv1alpha2.ObjectName("pg"), ref.Name)
	assert.Equal(t, gatewayv1alpha2.PortNumber(5432), *ref.Port)
	assert.Equal(t, gatewayv1alpha2.Namespace("ns"), *ref.Namespace)
	// K8s Service only: group/kind must stay at the core-Service default.
	assert.Nil(t, ref.Group)
	assert.Nil(t, ref.Kind)
}

func TestBuildUDPRouteObject_ParentRefUsesSectionName(t *testing.T) {
	obj := kubernetes.BuildUDPRouteObject(kubernetes.UDPRouteConfig{
		Name: "r", Namespace: "ns", GatewayName: "str-s", SectionName: "l4-udp-53",
		Backends: []kubernetes.L4Backend{
			{Service: "dns-a", Namespace: "ns", Port: 53, Weight: 90},
			{Service: "dns-b", Namespace: "ns", Port: 53, Weight: 10},
		},
	}, "gateway.networking.k8s.io/v1alpha2")
	assert.Equal(t, "gateway.networking.k8s.io/v1alpha2", obj.APIVersion)
	assert.Equal(t, "UDPRoute", obj.Kind)
	require.Len(t, obj.Spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1alpha2.SectionName("l4-udp-53"), *obj.Spec.ParentRefs[0].SectionName)
	require.Len(t, obj.Spec.Rules, 1)
	require.Len(t, obj.Spec.Rules[0].BackendRefs, 2)
	assert.Equal(t, int32(90), *obj.Spec.Rules[0].BackendRefs[0].Weight)
	assert.Equal(t, int32(10), *obj.Spec.Rules[0].BackendRefs[1].Weight)
	assert.Nil(t, obj.Spec.Rules[0].BackendRefs[0].Group)
	assert.Nil(t, obj.Spec.Rules[0].BackendRefs[0].Kind)
}

func TestL4RouteGVRs(t *testing.T) {
	assert.Equal(t, "gateway.networking.k8s.io", kubernetes.TCPRouteGVR.Group)
	assert.Equal(t, "v1alpha2", kubernetes.TCPRouteGVR.Version)
	assert.Equal(t, "tcproutes", kubernetes.TCPRouteGVR.Resource)
	assert.Equal(t, "gateway.networking.k8s.io", kubernetes.UDPRouteGVR.Group)
	assert.Equal(t, "v1alpha2", kubernetes.UDPRouteGVR.Version)
	assert.Equal(t, "udproutes", kubernetes.UDPRouteGVR.Resource)
}
