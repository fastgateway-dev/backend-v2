package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

func TestValidateCertificateKind(t *testing.T) {
	tests := []struct {
		name        string
		usage       models.ManagedCertUsage
		keyMode     models.ManagedCertKeyMode
		issuerType  models.IssuerType
		expectedErr error
	}{
		// Valid combinations
		{
			name:        "server + managed + acme",
			usage:       models.ManagedCertUsageServer,
			keyMode:     models.ManagedCertKeyModeManaged,
			issuerType:  models.IssuerTypeACME,
			expectedErr: nil,
		},
		{
			name:        "server + managed + self_signed_ca",
			usage:       models.ManagedCertUsageServer,
			keyMode:     models.ManagedCertKeyModeManaged,
			issuerType:  models.IssuerTypeSelfSignedCA,
			expectedErr: nil,
		},
		{
			name:        "client + managed + self_signed_ca",
			usage:       models.ManagedCertUsageClient,
			keyMode:     models.ManagedCertKeyModeManaged,
			issuerType:  models.IssuerTypeSelfSignedCA,
			expectedErr: nil,
		},
		{
			name:        "client + csr + self_signed_ca",
			usage:       models.ManagedCertUsageClient,
			keyMode:     models.ManagedCertKeyModeCSR,
			issuerType:  models.IssuerTypeSelfSignedCA,
			expectedErr: nil,
		},
		// Invalid combinations
		{
			name:        "client + managed + acme",
			usage:       models.ManagedCertUsageClient,
			keyMode:     models.ManagedCertKeyModeManaged,
			issuerType:  models.IssuerTypeACME,
			expectedErr: services.ErrClientRequiresPrivateCA,
		},
		{
			name:        "server + csr + acme",
			usage:       models.ManagedCertUsageServer,
			keyMode:     models.ManagedCertKeyModeCSR,
			issuerType:  models.IssuerTypeACME,
			expectedErr: services.ErrCSRRequiresClient,
		},
		{
			name:        "server + csr + self_signed_ca",
			usage:       models.ManagedCertUsageServer,
			keyMode:     models.ManagedCertKeyModeCSR,
			issuerType:  models.IssuerTypeSelfSignedCA,
			expectedErr: services.ErrCSRRequiresClient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := services.ValidateCertificateKind(tt.usage, tt.keyMode, tt.issuerType)
			if tt.expectedErr == nil {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			} else {
				if !errors.Is(err, tt.expectedErr) {
					t.Errorf("expected error %v, got %v", tt.expectedErr, err)
				}
			}
		})
	}
}
