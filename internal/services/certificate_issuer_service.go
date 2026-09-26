package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// CertificateIssuerService creates and manages platform-global certificate
// issuers backing FastGateway's managed-certificates feature: a self-signed
// internal CA chain, or an ACME issuer (DNS-01, e.g. Let's Encrypt via
// Cloudflare). Create applies the necessary cert-manager CRDs (and, for ACME,
// a DNS-01 solver Secret) to the control cluster via ControlPlane, then
// records the resolved object names on the row so Delete and later phases can
// find them again.
type CertificateIssuerService struct {
	repo            repository.CertificateIssuerRepositoryInterface
	dnsCreds        *DNSCredentialService
	controlPlane    CertInfraApplier
	config          *config.Config
	managedCertRepo repository.ManagedCertificateRepositoryInterface
}

// CertificateIssuerServiceDeps are CertificateIssuerService's required
// dependencies. NewCertificateIssuerService panics if any of them is nil,
// following the house pattern for services with required constructor
// dependencies.
type CertificateIssuerServiceDeps struct {
	Repo            repository.CertificateIssuerRepositoryInterface
	DNSCreds        *DNSCredentialService
	ControlPlane    CertInfraApplier
	Config          *config.Config
	ManagedCertRepo repository.ManagedCertificateRepositoryInterface
}

func NewCertificateIssuerService(deps CertificateIssuerServiceDeps) *CertificateIssuerService {
	var missing []string
	if deps.Repo == nil {
		missing = append(missing, "Repo")
	}
	if deps.DNSCreds == nil {
		missing = append(missing, "DNSCreds")
	}
	if deps.ControlPlane == nil {
		missing = append(missing, "ControlPlane")
	}
	if deps.Config == nil {
		missing = append(missing, "Config")
	}
	if deps.ManagedCertRepo == nil {
		missing = append(missing, "ManagedCertRepo")
	}
	if len(missing) > 0 {
		panic("services.NewCertificateIssuerService: missing required dependency: " + strings.Join(missing, ", "))
	}
	return &CertificateIssuerService{repo: deps.Repo, dnsCreds: deps.DNSCreds, controlPlane: deps.ControlPlane, config: deps.Config, managedCertRepo: deps.ManagedCertRepo}
}

// CreateIssuerInput is the request body for creating a certificate issuer.
// The self_signed_ca fields (CommonName, KeyAlgorithm, KeySize, DurationDays)
// apply only when Type is self_signed_ca; the ACME fields (Server, Email,
// EABKeyID, EABHMACKey, DNSCredentialID) apply only when Type is acme.
type CreateIssuerInput struct {
	Type            string     `json:"type" binding:"required"`
	Name            string     `json:"name" binding:"required"`
	CommonName      string     `json:"commonName"`
	KeyAlgorithm    string     `json:"keyAlgorithm"`
	KeySize         int        `json:"keySize"`
	DurationDays    int        `json:"durationDays"`
	Server          string     `json:"server"`
	Email           string     `json:"email"`
	EABKeyID        string     `json:"eabKeyId"`
	EABHMACKey      string     `json:"eabHmacKey"`
	DNSCredentialID *uuid.UUID `json:"dnsCredentialId"`
}

const selfSignedIssuerName = "fgw-selfsigned"

// Create builds either a self-signed internal CA chain or an ACME issuer,
// applying the cert-manager CRDs (and, for ACME, the DNS-01 solver Secret) to
// the control cluster via ControlPlane. The row is persisted before the CRDs
// are applied so its ID can be used to derive stable object names; if a CRD
// apply fails partway through, the row is kept with Status=error and
// StatusMessage set to the failure (returned as a normal, non-error result --
// mirroring domain_service's error-status convention -- so the caller can see
// what happened rather than losing the record).
func (s *CertificateIssuerService) Create(input *CreateIssuerInput, createdBy uuid.UUID) (*models.CertificateIssuer, error) {
	iss := &models.CertificateIssuer{Name: input.Name, Type: models.IssuerType(input.Type), CreatedBy: createdBy, Status: models.IssuerStatusPending}
	ctx := context.Background()
	ns := s.controlPlane.Namespace()

	switch models.IssuerType(input.Type) {
	case models.IssuerTypeSelfSignedCA:
		if input.CommonName == "" {
			return nil, errors.New("commonName is required for a self-signed CA")
		}
		if input.KeyAlgorithm == "" {
			input.KeyAlgorithm = "RSA"
		}
		if input.KeySize == 0 {
			input.KeySize = 4096
		}
		if input.DurationDays == 0 {
			input.DurationDays = 3650
		}
		if err := s.repo.Create(iss); err != nil { // persist first for a stable ID-derived name
			return nil, err
		}
		caSecret := "ca-" + iss.ID.String()
		issuerName := "iss-" + iss.ID.String()

		// Persist the resolved object names into Config BEFORE applying any
		// CRDs. If an apply below fails, markError persists the row with
		// Status=error but Config already populated, so Delete can still find
		// and clean up whatever was actually created in-cluster.
		iss.Config = models.IssuerConfig{
			CommonName: input.CommonName, KeyAlgorithm: input.KeyAlgorithm, KeySize: input.KeySize,
			DurationDays: input.DurationDays, CASecretName: caSecret, IssuerName: issuerName,
		}
		if err := s.repo.Update(iss); err != nil {
			return nil, err
		}

		if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerIssuerGVR, kubernetes.SelfSignedIssuer(selfSignedIssuerName, ns)); err != nil {
			return s.markError(iss, err)
		}
		caCert := kubernetes.CACertificate(kubernetes.CACertConfig{
			Name: caSecret, Namespace: ns, CommonName: input.CommonName, SecretName: caSecret,
			SelfSignedIssuerName: selfSignedIssuerName, KeyAlgorithm: input.KeyAlgorithm, KeySize: input.KeySize, DurationDays: input.DurationDays,
		})
		if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerCertificateGVR, caCert); err != nil {
			return s.markError(iss, err)
		}
		if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerIssuerGVR, kubernetes.CAIssuer(issuerName, ns, caSecret)); err != nil {
			return s.markError(iss, err)
		}

	case models.IssuerTypeACME:
		if input.Server == "" || input.Email == "" || input.DNSCredentialID == nil {
			return nil, errors.New("server, email and dnsCredentialId are required for an ACME issuer")
		}
		providerType, creds, err := s.dnsCreds.DecryptedCredentials(*input.DNSCredentialID)
		if err != nil {
			return nil, fmt.Errorf("resolve DNS credential: %w", err)
		}
		if err := s.repo.Create(iss); err != nil {
			return nil, err
		}
		issuerName := "iss-" + iss.ID.String()
		accountSecret := issuerName + "-account"
		solverSecret := issuerName + "-solver"
		var eabSecret string
		if input.EABHMACKey != "" {
			eabSecret = issuerName + "-eab"
		}

		// Persist the resolved object names into Config BEFORE applying any
		// CRDs, mirroring the self-signed-ca branch above: if an apply below
		// fails, markError persists the row with Status=error but Config
		// already populated (including DNSCredentialID, so the referential
		// guard in DNSCredentialService.Delete also sees this issuer), so
		// Delete can still find and clean up whatever secrets were actually
		// created in-cluster.
		iss.Config = models.IssuerConfig{
			Server: input.Server, Email: input.Email, EABKeyID: input.EABKeyID, DNSCredentialID: input.DNSCredentialID,
			IssuerName: issuerName, AccountSecretName: accountSecret, SolverSecretName: solverSecret,
			EABSecretName: eabSecret,
		}
		if err := s.repo.Update(iss); err != nil {
			return nil, err
		}

		if providerType == "cloudflare" {
			solver := kubernetes.CloudflareSolverSecret(solverSecret, ns, creds["apiToken"])
			if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.SecretGVR, solver); err != nil {
				return s.markError(iss, err)
			}
		}

		if eabSecret != "" {
			eab := kubernetes.EABSecret(eabSecret, ns, input.EABHMACKey)
			if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.SecretGVR, eab); err != nil {
				return s.markError(iss, err)
			}
		}

		acme := kubernetes.ACMEIssuer(kubernetes.ACMEIssuerConfig{
			Name: issuerName, Namespace: ns, Server: input.Server, Email: input.Email,
			AccountSecretName: accountSecret, ProviderType: providerType, SolverSecretName: solverSecret,
			EABKeyID: input.EABKeyID, EABSecretName: eabSecret,
		})
		if err := s.controlPlane.ApplyNamespaced(ctx, kubernetes.CertManagerIssuerGVR, acme); err != nil {
			return s.markError(iss, err)
		}
	default:
		return nil, errors.New("unsupported issuer type: " + input.Type)
	}

	iss.Status = models.IssuerStatusReady
	iss.StatusMessage = "Issuer created"
	_ = s.repo.Update(iss)
	return iss, nil
}

func (s *CertificateIssuerService) markError(iss *models.CertificateIssuer, cause error) (*models.CertificateIssuer, error) {
	iss.Status = models.IssuerStatusError
	iss.StatusMessage = cause.Error()
	_ = s.repo.Update(iss)
	return iss, nil // persisted with error status; handler returns 201 with the issuer body (matches domain_service)
}

func (s *CertificateIssuerService) List() ([]models.CertificateIssuer, error) { return s.repo.List() }
func (s *CertificateIssuerService) GetByID(id uuid.UUID) (*models.CertificateIssuer, error) {
	return s.repo.GetByID(id)
}

// Delete removes the issuer's namespaced cert-manager Issuer (and, for a
// self-signed CA, its CA Certificate; for an ACME issuer, its DNS-01 solver,
// account key, and EAB HMAC secrets) from the control cluster, then the row.
// Kubernetes deletes are best-effort (not-found is not an error, see
// ControlPlaneClient.Delete) so a partially-applied issuer can still be
// cleaned up. Removing the acme secrets matters beyond tidiness: the solver
// secret holds a plaintext DNS provider API token, so leaving it behind after
// Delete would be a credential leak.
//
// Delete first checks that no ManagedCertificate still references the
// issuer, returning an "in use" error (mapped to 409 by the handler) rather
// than orphaning certificates whose issuer disappeared out from under them.
func (s *CertificateIssuerService) Delete(id uuid.UUID) error {
	iss, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}
	n, err := s.managedCertRepo.CountByIssuer(id)
	if err != nil {
		return err
	}
	if n > 0 {
		return errors.New("certificate issuer is in use by one or more managed certificates")
	}
	ctx := context.Background()
	if iss.Config.IssuerName != "" {
		_ = s.controlPlane.Delete(ctx, kubernetes.CertManagerIssuerGVR, iss.Config.IssuerName, true)
	}
	if iss.Config.CASecretName != "" {
		_ = s.controlPlane.Delete(ctx, kubernetes.CertManagerCertificateGVR, iss.Config.CASecretName, true)
	}
	if iss.Config.SolverSecretName != "" {
		_ = s.controlPlane.Delete(ctx, kubernetes.SecretGVR, iss.Config.SolverSecretName, true)
	}
	if iss.Config.AccountSecretName != "" {
		_ = s.controlPlane.Delete(ctx, kubernetes.SecretGVR, iss.Config.AccountSecretName, true)
	}
	if iss.Config.EABSecretName != "" {
		_ = s.controlPlane.Delete(ctx, kubernetes.SecretGVR, iss.Config.EABSecretName, true)
	}
	return s.repo.Delete(id)
}
