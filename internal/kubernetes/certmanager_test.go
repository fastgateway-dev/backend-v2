package kubernetes_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

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
	assertGolden(t, "ca-certificate", obj)
}

func TestACMEClusterIssuer_Golden(t *testing.T) {
	obj := kubernetes.ACMEClusterIssuer(kubernetes.ACMEIssuerConfig{
		Name: "iss-acme", Server: "https://acme-v02.api.letsencrypt.org/directory",
		Email: "ops@example.com", AccountSecretName: "iss-acme-account",
		ProviderType: "cloudflare", SolverSecretName: "iss-acme-cf",
	})
	assert.Equal(t, "ClusterIssuer", obj.Object["kind"])
	assertGolden(t, "acme-clusterissuer", obj)
}
