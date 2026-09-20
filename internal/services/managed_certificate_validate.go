package services

import (
	"errors"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

var (
	ErrClientRequiresPrivateCA  = errors.New("client certificates require a self-signed (private) CA issuer")
	ErrCSRRequiresClient        = errors.New("CSR key mode is only valid for client certificates")
	ErrServerRequiresManagedKey = errors.New("server certificates must use managed key mode")
)

// ValidateCertificateKind enforces the valid combinations of usage, key mode,
// and issuer trust origin. See the managed-client-certificates design spec.
func ValidateCertificateKind(usage models.ManagedCertUsage, keyMode models.ManagedCertKeyMode, issuerType models.IssuerType) error {
	if usage == models.ManagedCertUsageClient && issuerType != models.IssuerTypeSelfSignedCA {
		return ErrClientRequiresPrivateCA
	}
	if keyMode == models.ManagedCertKeyModeCSR && usage != models.ManagedCertUsageClient {
		return ErrCSRRequiresClient
	}
	if usage == models.ManagedCertUsageServer && keyMode != models.ManagedCertKeyModeManaged {
		return ErrServerRequiresManagedKey
	}
	return nil
}
