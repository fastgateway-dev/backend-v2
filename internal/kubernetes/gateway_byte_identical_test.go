package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func gatewayListeners(t *testing.T, cfg *GatewayConfig) []map[string]interface{} {
	t.Helper()
	obj := BuildGatewayObject(cfg)
	require.NotNil(t, obj)
	ls, found, err := unstructured.NestedSlice(obj.Object, "spec", "listeners")
	require.NoError(t, err)
	require.True(t, found)
	out := make([]map[string]interface{}, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.(map[string]interface{}))
	}
	return out
}

// TestGateway_BothListeners_ByteIdentical pins the byte-identical invariant: a
// "both" domain still renders [http, https] with the same TLS block as the
// pre-listener-model builder.
func TestGateway_BothListeners_ByteIdentical(t *testing.T) {
	ls := gatewayListeners(t, &GatewayConfig{
		Name: "gw", Namespace: "ns", GatewayClassName: "gc", Hostname: "api.example.com",
		HostnameListeners: []HostnameListener{
			{Name: "http", Protocol: "HTTP", Port: 80},
			{Name: "https", Protocol: "HTTPS", Port: 443, TLSMode: "Terminate"},
		},
		TLSSecretName: "api-tls",
	})
	require.Len(t, ls, 2)

	assert.Equal(t, map[string]interface{}{
		"name":     "http",
		"port":     int64(80),
		"protocol": "HTTP",
		"hostname": "api.example.com",
	}, ls[0])

	assert.Equal(t, map[string]interface{}{
		"name":     "https",
		"port":     int64(443),
		"protocol": "HTTPS",
		"hostname": "api.example.com",
		"tls": map[string]interface{}{
			"mode": "Terminate",
			"certificateRefs": []interface{}{
				map[string]interface{}{"kind": "Secret", "name": "api-tls"},
			},
		},
	}, ls[1])
}

func TestGateway_HostnameListeners_Variants(t *testing.T) {
	t.Run("https only passthrough with cross-namespace secret", func(t *testing.T) {
		ls := gatewayListeners(t, &GatewayConfig{
			Name: "gw", Namespace: "ns", GatewayClassName: "gc", Hostname: "h.example.com",
			HostnameListeners:  []HostnameListener{{Name: "https", Protocol: "HTTPS", Port: 8443, TLSMode: "Passthrough"}},
			TLSSecretName:      "s",
			TLSSecretNamespace: "other",
		})
		require.Len(t, ls, 1)
		tls := ls[0]["tls"].(map[string]interface{})
		assert.Equal(t, "Passthrough", tls["mode"])
		ref := tls["certificateRefs"].([]interface{})[0].(map[string]interface{})
		assert.Equal(t, "other", ref["namespace"])
	})

	t.Run("https without secret has no tls block", func(t *testing.T) {
		ls := gatewayListeners(t, &GatewayConfig{
			Name: "gw", Namespace: "ns", GatewayClassName: "gc", Hostname: "h.example.com",
			HostnameListeners: []HostnameListener{{Name: "https", Protocol: "HTTPS", Port: 443, TLSMode: "Terminate"}},
		})
		require.Len(t, ls, 1)
		assert.NotContains(t, ls[0], "tls")
	})

	t.Run("same-namespace secret omits namespace", func(t *testing.T) {
		ls := gatewayListeners(t, &GatewayConfig{
			Name: "gw", Namespace: "ns", GatewayClassName: "gc", Hostname: "h.example.com",
			HostnameListeners:  []HostnameListener{{Name: "https", Protocol: "HTTPS", Port: 443, TLSMode: "Terminate"}},
			TLSSecretName:      "s",
			TLSSecretNamespace: "ns",
		})
		ref := ls[0]["tls"].(map[string]interface{})["certificateRefs"].([]interface{})[0].(map[string]interface{})
		assert.NotContains(t, ref, "namespace")
	})
}
