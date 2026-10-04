package kubernetes_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/stretchr/testify/assert"
)

func TestDNSEndpoint_A_Golden(t *testing.T) {
	ttl := 300
	obj := kubernetes.DNSEndpoint(kubernetes.DNSEndpointConfig{
		Name: "dns-11111111", Hostname: "app.example.com", RecordType: "A",
		Targets: []string{"203.0.113.10"}, TTL: &ttl,
	})
	assert.Equal(t, "DNSEndpoint", obj.Object["kind"])
	assert.Equal(t, "fastgateway-system", obj.GetNamespace())
	assertGolden(t, "externaldns", "dnsendpoint-a", obj)
}

func TestDNSEndpoint_CNAME_ProxiedGolden(t *testing.T) {
	obj := kubernetes.DNSEndpoint(kubernetes.DNSEndpointConfig{
		Name: "dns-22222222", Hostname: "app.example.com", RecordType: "CNAME",
		Targets: []string{"lb.example.net"}, CloudflareProxied: true,
	})
	assertGolden(t, "externaldns", "dnsendpoint-cname-proxied", obj)
}

func TestExternalDNSSecret_Golden(t *testing.T) {
	obj := kubernetes.ExternalDNSSecret(kubernetes.ExternalDNSSecretName, map[string][]byte{"apiToken": []byte("tok")})
	assert.Equal(t, "Secret", obj.Object["kind"])
	assertGolden(t, "externaldns", "externaldns-secret", obj)
}

func TestDNSEndpointGVR_Defined(t *testing.T) {
	assert.Equal(t, "dnsendpoints", kubernetes.DNSEndpointGVR.Resource)
	assert.Equal(t, "externaldns.k8s.io", kubernetes.DNSEndpointGVR.Group)
	assert.Equal(t, "v1alpha1", kubernetes.DNSEndpointGVR.Version)
}

func TestSecretGVR_Reused(t *testing.T) {
	assert.Equal(t, "secrets", kubernetes.SecretGVR.Resource)
	assert.Equal(t, "", kubernetes.SecretGVR.Group)
	assert.Equal(t, "v1", kubernetes.SecretGVR.Version)
}
