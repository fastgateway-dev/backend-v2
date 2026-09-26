package kubernetes_test

import (
	"encoding/base64"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// sampleCSRPEM is a fixed sample CSR (CN=client-1.fastgateway, EC
// prime256v1 key) generated once with openssl. It must stay fixed so the
// base64-encoded spec.request in the golden file is stable across runs.
const sampleCSRPEM = `-----BEGIN CERTIFICATE REQUEST-----
MIHbMIGBAgEAMB8xHTAbBgNVBAMMFGNsaWVudC0xLmZhc3RnYXRld2F5MFkwEwYH
KoZIzj0CAQYIKoZIzj0DAQcDQgAEq/kJLFJpC46THJo399VFiDQ3my+/HEplaeMw
lkr2XNvP9xYpDUHnxTxm5wo8vgTbpQiwYRfTNJWKulGqNu1lkKAAMAoGCCqGSM49
BAMCA0kAMEYCIQDt1sR62FKflBqbux2WSOWk0Nt/RNuyF4zcg0NA99JbsgIhAI6/
/rFwaBOepjROniakTj8JaZ7eAQ9xHgccllfjt7Uj
-----END CERTIFICATE REQUEST-----
`

var updateGolden = flag.Bool("update-golden", false, "rewrite golden files")

func assertGolden(t *testing.T, name string, obj any) {
	t.Helper()
	got, err := yaml.Marshal(obj)
	require.NoError(t, err)
	path := filepath.Join("testdata", "golden", "certmanager", name+".yaml")
	if *updateGolden {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoErrorf(t, err, "missing golden %s; regenerate with -update-golden", path)
	assert.Equal(t, string(want), string(got))
}

func TestCACertificate_Golden(t *testing.T) {
	obj := kubernetes.CACertificate(kubernetes.CACertConfig{
		Name: "ca-abc", Namespace: "fastgateway-system", CommonName: "FastGateway Root",
		SecretName: "ca-abc", SelfSignedIssuerName: "fgw-selfsigned",
		KeyAlgorithm: "RSA", KeySize: 4096, DurationDays: 3650,
	})
	assert.Equal(t, "cert-manager.io/v1", obj.Object["apiVersion"])
	assert.Equal(t, "Certificate", obj.Object["kind"])
	spec := obj.Object["spec"].(map[string]interface{})
	issuerRef := spec["issuerRef"].(map[string]interface{})
	assert.Equal(t, "Issuer", issuerRef["kind"])
	assertGolden(t, "ca-certificate", obj)
}

func TestLeafCertificate_Golden(t *testing.T) {
	obj := kubernetes.LeafCertificate(kubernetes.LeafCertConfig{
		Name: "cert-abc", Namespace: "fastgateway-system", SecretName: "cert-abc",
		IssuerName: "iss-1", DNSNames: []string{"api.example.com"},
		KeyAlgorithm: "RSA", KeySize: 2048, DurationDays: 90,
	})
	assert.Equal(t, "Certificate", obj.Object["kind"])
	spec := obj.Object["spec"].(map[string]interface{})
	assert.NotContains(t, spec, "isCA")
	assert.Contains(t, spec["usages"], "server auth")
	issuerRef := spec["issuerRef"].(map[string]interface{})
	assert.Equal(t, "Issuer", issuerRef["kind"])
	assertGolden(t, "leaf-certificate", obj)
}

func TestLeafCertificate_ClientAuth_Golden(t *testing.T) {
	obj := kubernetes.LeafCertificate(kubernetes.LeafCertConfig{
		Name: "cert-client-abc", Namespace: "fastgateway-system", SecretName: "cert-client-abc",
		IssuerName:   "iss-1",
		Usage:        models.ManagedCertUsageClient,
		URISANs:      []string{"spiffe://fastgateway/proj/client-1"},
		KeyAlgorithm: "RSA", KeySize: 2048, DurationDays: 90,
	})
	assert.Equal(t, "cert-manager.io/v1", obj.Object["apiVersion"])
	assert.Equal(t, "Certificate", obj.Object["kind"])
	spec := obj.Object["spec"].(map[string]interface{})
	assert.Contains(t, spec["usages"], "client auth")
	assert.Equal(t, []interface{}{"spiffe://fastgateway/proj/client-1"}, spec["uris"])
	issuerRef := spec["issuerRef"].(map[string]interface{})
	assert.Equal(t, "Issuer", issuerRef["kind"])
	assertGolden(t, "leaf-certificate-client-auth", obj)
}

func TestCertificateRequest_Golden(t *testing.T) {
	csr := []byte(sampleCSRPEM)
	obj := kubernetes.CertificateRequestObject(kubernetes.CertificateRequestConfig{
		Name: "csr-abc", Namespace: "fastgateway-system",
		IssuerName:   "iss-1",
		Request:      csr,
		DurationDays: 90,
	})
	assert.Equal(t, "cert-manager.io/v1", obj.Object["apiVersion"])
	assert.Equal(t, "CertificateRequest", obj.Object["kind"])
	spec := obj.Object["spec"].(map[string]interface{})
	assert.Contains(t, spec["usages"], "client auth")
	assert.Equal(t, base64.StdEncoding.EncodeToString(csr), spec["request"])
	issuerRef := spec["issuerRef"].(map[string]interface{})
	assert.Equal(t, "Issuer", issuerRef["kind"])
	assertGolden(t, "certificate-request", obj)
}

func TestACMEIssuer_Golden(t *testing.T) {
	obj := kubernetes.ACMEIssuer(kubernetes.ACMEIssuerConfig{
		Name: "iss-acme", Namespace: "fastgateway-system", Server: "https://acme-v02.api.letsencrypt.org/directory",
		Email: "ops@example.com", AccountSecretName: "iss-acme-account",
		ProviderType: "cloudflare", SolverSecretName: "iss-acme-cf",
	})
	assert.Equal(t, "Issuer", obj.Object["kind"])
	assert.Equal(t, "fastgateway-system", obj.GetNamespace())
	assertGolden(t, "acme-issuer", obj)
}

func TestSelfSignedIssuer_NamespacedIssuer(t *testing.T) {
	obj := kubernetes.SelfSignedIssuer("fgw-selfsigned", "fastgateway-system")
	assert.Equal(t, "Issuer", obj.GetKind())
	assert.Equal(t, "fastgateway-system", obj.GetNamespace())
}

func TestCAIssuer_NamespacedIssuer(t *testing.T) {
	obj := kubernetes.CAIssuer("iss-1", "fastgateway-system", "ca-1")
	assert.Equal(t, "Issuer", obj.GetKind())
	assert.Equal(t, "fastgateway-system", obj.GetNamespace())
	ca, _, _ := unstructured.NestedString(obj.Object, "spec", "ca", "secretName")
	assert.Equal(t, "ca-1", ca)
}
