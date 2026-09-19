package cluster

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/google/uuid"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// =============================================================================
// Managed Certificate TLS Secret Distribution
// =============================================================================

// CreateOrUpdateTLSSecret creates or updates a Kubernetes Secret of type
// kubernetes.io/tls in a project's cluster, carrying the given certificate
// and private key. Unlike CreateOrUpdateSecret (which writes an Opaque
// mTLS-CA bundle), this is what the managed-certificate distribution
// controller uses to land an issued leaf cert where Envoy Gateway (or any
// other TLS-terminating consumer) expects to find it.
func (s *Client) CreateOrUpdateTLSSecret(ctx context.Context, projectID uuid.UUID, namespace, name string, crt, key []byte) error {
	client, err := s.getClient(projectID)
	if err != nil {
		return err
	}

	gvr := kubernetes.CoreSecretGVR
	secret := kubernetes.TLSSecretObject(name, namespace, crt, key)

	// Try to create; if exists, update
	_, err = client.Resource(gvr).Namespace(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			// Retries on conflict: a re-issued or rotated certificate rewrites
			// this same Secret, so concurrent distribution runs can race.
			ri := client.Resource(gvr).Namespace(namespace)
			if err := updateUnstructuredWithRetry(ctx, ri, name, secret); err != nil {
				return fmt.Errorf("failed to update tls secret: %w", err)
			}
		} else {
			return fmt.Errorf("failed to create tls secret: %w", err)
		}
	}

	return nil
}

// CertFingerprint returns the hex-encoded SHA-256 digest of a certificate's
// raw bytes. Used to detect when a managed certificate's issued material has
// changed (and therefore needs to be redistributed) without comparing the
// full cert/key payload.
func CertFingerprint(crt []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(crt))
}
