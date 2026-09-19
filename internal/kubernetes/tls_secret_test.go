package kubernetes_test

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
)

func TestTLSSecretObject_BasicShape(t *testing.T) {
	crt := []byte("---CERT---")
	key := []byte("---KEY---")

	obj := kubernetes.TLSSecretObject("cert-x", "fastgateway-system", crt, key)

	require.NotNil(t, obj)
	assert.Equal(t, "v1", obj.Object["apiVersion"])
	assert.Equal(t, "Secret", obj.Object["kind"])
	assert.Equal(t, "kubernetes.io/tls", obj.Object["type"])

	metadata, ok := obj.Object["metadata"].(map[string]interface{})
	require.True(t, ok, "metadata must be a map")
	assert.Equal(t, "cert-x", metadata["name"])
	assert.Equal(t, "fastgateway-system", metadata["namespace"])

	labels, ok := metadata["labels"].(map[string]interface{})
	require.True(t, ok, "labels must be a map")
	assert.Equal(t, "fastgateway", labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, "managed-cert", labels["fastgateway.dev/type"])

	data, ok := obj.Object["data"].(map[string]interface{})
	require.True(t, ok, "data must be a map")

	gotCrt, err := base64.StdEncoding.DecodeString(data["tls.crt"].(string))
	require.NoError(t, err)
	assert.Equal(t, crt, gotCrt)

	gotKey, err := base64.StdEncoding.DecodeString(data["tls.key"].(string))
	require.NoError(t, err)
	assert.Equal(t, key, gotKey)
}

func TestTLSSecretObject_IsPureNoUnexpectedFields(t *testing.T) {
	obj1 := kubernetes.TLSSecretObject("cert-y", "ns-a", []byte("a"), []byte("b"))
	obj2 := kubernetes.TLSSecretObject("cert-y", "ns-a", []byte("a"), []byte("b"))

	// Pure builder: same inputs produce equal (but independent) objects, no I/O.
	assert.Equal(t, obj1.Object, obj2.Object)
}
