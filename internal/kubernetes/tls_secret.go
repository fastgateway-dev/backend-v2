package kubernetes

import (
	"encoding/base64"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TLSSecretObject builds a Kubernetes Secret of type kubernetes.io/tls
// carrying a leaf certificate and its private key. It is a pure builder: no
// I/O, no cluster access — callers (cluster.Client.CreateOrUpdateTLSSecret)
// apply the returned object.
//
// This mirrors the object shape cluster.Client.CreateOrUpdateSecret builds
// for mTLS CA secrets, but with the TLS-specific type and keys that a
// managed certificate's Secret needs: type: Opaque with an arbitrary data
// map is wrong for a cert/key pair that Envoy Gateway (or any TLS consumer)
// expects to find under the standard tls.crt / tls.key keys.
func TLSSecretObject(name, namespace string, crt, key []byte) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "fastgateway",
					"fastgateway.dev/type":         "managed-cert",
				},
			},
			"type": "kubernetes.io/tls",
			"data": map[string]interface{}{
				"tls.crt": base64.StdEncoding.EncodeToString(crt),
				"tls.key": base64.StdEncoding.EncodeToString(key),
			},
		},
	}
}
