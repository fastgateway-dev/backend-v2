package certdist

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// ControlPlaneSourceReader adapts *cluster.ControlPlaneClient into the
// SourceReader the Distributor depends on: it reads the Secret that
// cert-manager (or whichever issuer) writes into the control-plane cluster,
// at kubernetes.CoreSecretGVR/name in the control plane's own namespace,
// once a leaf certificate finishes issuing.
type ControlPlaneSourceReader struct {
	ControlPlane *cluster.ControlPlaneClient
}

var _ SourceReader = (*ControlPlaneSourceReader)(nil)

// ReadLeafSecret reads and base64-decodes the tls.crt/tls.key data of the
// named Secret. found is false, with a nil error, only when the Secret
// itself doesn't exist yet (k8serrors.IsNotFound) -- an expected state while
// a certificate is still issuing, not an error. Any other failure to read or
// decode the Secret (including a Secret that exists but is missing
// tls.crt/tls.key, which should never happen for a cert-manager-issued TLS
// Secret) is reported as an error instead, since that is not a "just wait
// and retry" condition.
func (r *ControlPlaneSourceReader) ReadLeafSecret(ctx context.Context, name string) (crt, key []byte, found bool, err error) {
	obj, err := r.ControlPlane.Get(ctx, kubernetes.CoreSecretGVR, name, true)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil, false, nil
		}
		return nil, nil, false, fmt.Errorf("certdist: reading source secret %q: %w", name, err)
	}

	// Mirrors the extraction pattern in cluster/secret.go's GetSecretData:
	// Secret.data is a map of base64-encoded strings.
	data, ok, err := unstructured.NestedStringMap(obj.Object, "data")
	if err != nil || !ok {
		return nil, nil, false, fmt.Errorf("certdist: source secret %q has no data", name)
	}

	crtEnc, ok := data["tls.crt"]
	if !ok {
		return nil, nil, false, fmt.Errorf("certdist: source secret %q missing tls.crt", name)
	}
	keyEnc, ok := data["tls.key"]
	if !ok {
		return nil, nil, false, fmt.Errorf("certdist: source secret %q missing tls.key", name)
	}

	crt, err = base64.StdEncoding.DecodeString(crtEnc)
	if err != nil {
		return nil, nil, false, fmt.Errorf("certdist: decoding tls.crt from source secret %q: %w", name, err)
	}
	key, err = base64.StdEncoding.DecodeString(keyEnc)
	if err != nil {
		return nil, nil, false, fmt.Errorf("certdist: decoding tls.key from source secret %q: %w", name, err)
	}

	return crt, key, true, nil
}

// ManagedCertRepo is the narrow slice of
// repository.ManagedCertificateRepositoryInterface that ManagedCertUpdater
// needs: load a certificate row, mutate it, save it back. Kept as its own
// interface (rather than depending on the repository package's wider one)
// so this adapter's tests don't need every other repository method faked.
type ManagedCertRepo interface {
	GetByID(id uuid.UUID) (*models.ManagedCertificate, error)
	Update(c *models.ManagedCertificate) error
}

// ManagedCertUpdater adapts a ManagedCertRepo into the CertUpdater the
// Distributor depends on: recording the fingerprint/notAfter observed on a
// ManagedCertificate's issued leaf material once a distribution push
// succeeds. Status is deliberately left untouched here -- that field
// reflects cert-manager's live Ready condition as reported by
// ManagedCertificateService.Status, not distribution outcome.
type ManagedCertUpdater struct {
	Repo ManagedCertRepo
}

var _ CertUpdater = (*ManagedCertUpdater)(nil)

func (u *ManagedCertUpdater) SetIssuedMeta(certID uuid.UUID, fingerprint string, notAfter *time.Time) error {
	cert, err := u.Repo.GetByID(certID)
	if err != nil {
		return fmt.Errorf("certdist: loading certificate %s: %w", certID, err)
	}

	cert.Fingerprint = fingerprint
	cert.NotAfter = notAfter

	if err := u.Repo.Update(cert); err != nil {
		return fmt.Errorf("certdist: updating certificate %s: %w", certID, err)
	}
	return nil
}
