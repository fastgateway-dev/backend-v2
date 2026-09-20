package clients

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// CAReader is the narrow slice of *cluster.ControlPlaneClient that
// ClientCertificateService needs: reading a single Secret out of the
// control-plane namespace (the issuer's CA Secret) by GroupVersionResource
// and name. Defined here (rather than depending on the concrete
// *cluster.ControlPlaneClient type) so tests can substitute a fake without
// standing up a fake dynamic Kubernetes client.
type CAReader interface {
	Get(ctx context.Context, gvr schema.GroupVersionResource, name string, namespaced bool) (*unstructured.Unstructured, error)
}

// var assertion: *cluster.ControlPlaneClient satisfies CAReader today. If
// its Get signature ever changes, this line breaks the build instead of
// letting NewClientCertificateService's caller silently pass an
// incompatible client at construction time.
var _ CAReader = (*cluster.ControlPlaneClient)(nil)

// Sentinel errors AttachCertificate/DetachCertificate return, for the
// handler to map to HTTP status codes (see internal/handlers/
// client_certificate_handler.go). Mirrors the ErrDomainNotFound /
// ErrCertificateWrongUsage / ErrCertificateNotReady pattern DomainService
// uses for the analogous server-certificate attach flow
// (internal/services/domain_service.go).
var (
	// ErrNotClientCert is returned by AttachCertificate when the managed
	// certificate's Usage is not models.ManagedCertUsageClient. Only a
	// client-usage certificate can identify an API client via mTLS.
	ErrNotClientCert = errors.New("certificate usage must be client")

	// ErrCertNotReadyForAttach is returned by AttachCertificate when the
	// certificate's Status is not models.ManagedCertStatusReady. Attaching
	// a not-yet-issued certificate would point the client's mTLS
	// configuration at a leaf secret that does not exist yet.
	ErrCertNotReadyForAttach = errors.New("certificate is not ready")

	// ErrCertProjectNotAccessible is returned by AttachCertificate when the
	// client's team has no role in the certificate's project (via
	// TeamRepo.ListTeamProjects). This is the team<->project bridge: a
	// client belongs to a team, not a project, but a managed certificate
	// belongs to a project, so attaching one to the other is only allowed
	// when the client's team actually has access to that project.
	ErrCertProjectNotAccessible = errors.New("client's team does not have access to the certificate's project")

	// ErrCertAlreadyAttached is returned by AttachCertificate when the
	// certificate is already attached to a different client. The
	// client<->certificate relationship is 1:1 (enforced here, not by a DB
	// constraint), so a certificate already spoken for must be detached
	// from its current client first.
	ErrCertAlreadyAttached = errors.New("certificate is already attached to another client")

	// ErrClientHasManagedCert is returned by AttachCertificate when the
	// client already has a different managed certificate attached. Detach
	// it first.
	ErrClientHasManagedCert = errors.New("client already has a managed certificate attached; detach it first")
)

// ClientCertificateServiceDeps carries everything ClientCertificateService
// needs. Every field is required.
//
// Deliberately excludes any domain/route/deploy dependency: AttachCertificate
// only sets the client's mTLS fields (MTLSEnabled, MTLSCAPem, MTLSSANs,
// ManagedCertificateID, ...) and persists the client row. It does not
// re-apply anything to the cluster -- the CA Secret and ClientTrafficPolicy
// materialize at the next domain deploy, exactly like the existing
// UpdateClientMTLS (internal/services/clients/client_auth.go), which also
// just writes the client row and lets route_deploy_clients.go materialize
// mTLS at deploy time. That is also why this service is separate from the
// eighteen-method ClientService rather than another method on it: it is the
// one part of client management that needs control-plane (in-cluster) access,
// and keeping it in its own file/service contains that dependency instead of
// spreading it across ClientService's constructor.
type ClientCertificateServiceDeps struct {
	ClientRepo repository.ClientRepositoryInterface
	CertRepo   repository.ManagedCertificateRepositoryInterface
	IssuerRepo repository.CertificateIssuerRepositoryInterface
	TeamRepo   repository.TeamRepositoryInterface
	CAReader   CAReader
}

// ClientCertificateService attaches/detaches a managed client certificate
// (models.ManagedCertUsageClient) onto a Client's mTLS configuration.
type ClientCertificateService struct {
	clientRepo repository.ClientRepositoryInterface
	certRepo   repository.ManagedCertificateRepositoryInterface
	issuerRepo repository.CertificateIssuerRepositoryInterface
	teamRepo   repository.TeamRepositoryInterface
	caReader   CAReader
}

// NewClientCertificateService builds a fully-wired ClientCertificateService.
// It panics if a required dependency is missing (Master design section
// 6.6's fail-at-startup rule; mirrors NewClientAttachmentService).
func NewClientCertificateService(deps ClientCertificateServiceDeps) *ClientCertificateService {
	var missing []string
	if deps.ClientRepo == nil {
		missing = append(missing, "ClientRepo")
	}
	if deps.CertRepo == nil {
		missing = append(missing, "CertRepo")
	}
	if deps.IssuerRepo == nil {
		missing = append(missing, "IssuerRepo")
	}
	if deps.TeamRepo == nil {
		missing = append(missing, "TeamRepo")
	}
	if deps.CAReader == nil {
		missing = append(missing, "CAReader")
	}
	if len(missing) > 0 {
		panic("clients.NewClientCertificateService: missing required dependency: " + strings.Join(missing, ", "))
	}

	return &ClientCertificateService{
		clientRepo: deps.ClientRepo,
		certRepo:   deps.CertRepo,
		issuerRepo: deps.IssuerRepo,
		teamRepo:   deps.TeamRepo,
		caReader:   deps.CAReader,
	}
}

// AttachCertificate points a client's mTLS configuration at a managed
// client-usage certificate: it validates the certificate and the client's
// access to it, reads the issuing CA's PEM out of the control-plane cluster,
// derives the client's MTLS* fields from the certificate, and persists the
// client row. It does not touch the cluster itself -- see the deps doc
// comment above for why.
func (s *ClientCertificateService) AttachCertificate(ctx context.Context, clientID, certID, actingUser uuid.UUID) (*models.Client, error) {
	cert, err := s.certRepo.GetByID(certID)
	if err != nil {
		return nil, fmt.Errorf("get certificate: %w", err)
	}
	if cert.Usage != models.ManagedCertUsageClient {
		return nil, ErrNotClientCert
	}
	if cert.Status != models.ManagedCertStatusReady {
		return nil, ErrCertNotReadyForAttach
	}

	client, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, fmt.Errorf("get client: %w", err)
	}

	// Team<->project bridge: the client belongs to a team, the certificate
	// belongs to a project, so attaching is only allowed when the client's
	// team has some role in the certificate's project.
	roles, err := s.teamRepo.ListTeamProjects(client.TeamID)
	if err != nil {
		return nil, fmt.Errorf("list team projects: %w", err)
	}
	accessible := false
	for _, role := range roles {
		if role.ProjectID == cert.ProjectID {
			accessible = true
			break
		}
	}
	if !accessible {
		return nil, ErrCertProjectNotAccessible
	}

	// 1:1 checks: the certificate must not already be attached to a
	// *different* client, and this client must not already have a
	// *different* managed certificate attached (detach first).
	existing, err := s.clientRepo.GetByManagedCertificateID(certID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check existing certificate attachment: %w", err)
	}
	if err == nil && existing.ID != clientID {
		return nil, ErrCertAlreadyAttached
	}
	if client.ManagedCertificateID != nil {
		return nil, ErrClientHasManagedCert
	}

	issuer, err := s.issuerRepo.GetByID(cert.IssuerID)
	if err != nil {
		return nil, fmt.Errorf("get issuer: %w", err)
	}

	caPEM, err := s.readIssuerCAPEM(ctx, issuer)
	if err != nil {
		return nil, err
	}

	var sans models.MTLSSANList
	for _, dns := range cert.Config.DNSNames {
		sans = append(sans, models.MTLSSANEntry{Type: "DNS", Value: dns})
	}
	for _, uri := range cert.Config.URISANs {
		sans = append(sans, models.MTLSSANEntry{Type: "URI", Value: uri})
	}

	now := time.Now()
	client.MTLSEnabled = true
	client.MTLSCAName = cert.Name
	client.MTLSCASecret = fmt.Sprintf("fastgateway-client-%s-mtls-ca", clientID.String()[:8])
	client.MTLSCASecretKey = "ca.crt"
	client.MTLSCAPem = string(caPEM)
	client.MTLSSANs = sans
	client.MTLSHashes = nil
	client.ManagedCertificateID = &certID
	client.MTLSCreatedBy = &actingUser
	client.MTLSCreatedAt = &now

	if err := s.clientRepo.Update(client); err != nil {
		return nil, fmt.Errorf("update client: %w", err)
	}

	return client, nil
}

// AttachableCertificates lists the managed client certificates the given
// client could attach: usage=client, status=ready, scoped to the projects
// the client's team has a role in (the same team<->project bridge
// AttachCertificate enforces), and not already attached to any client. This
// lets the frontend pre-scope the attach picker instead of the caller
// walking every accessible certificate and filtering client-side.
func (s *ClientCertificateService) AttachableCertificates(clientID uuid.UUID) ([]models.ManagedCertificate, error) {
	client, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, fmt.Errorf("get client: %w", err)
	}

	roles, err := s.teamRepo.ListTeamProjects(client.TeamID)
	if err != nil {
		return nil, fmt.Errorf("list team projects: %w", err)
	}
	if len(roles) == 0 {
		return nil, nil
	}

	projectIDs := make([]uuid.UUID, 0, len(roles))
	for _, role := range roles {
		projectIDs = append(projectIDs, role.ProjectID)
	}

	certs, err := s.certRepo.ListAttachableClientCerts(projectIDs)
	if err != nil {
		return nil, fmt.Errorf("list attachable client certs: %w", err)
	}
	return certs, nil
}

// readIssuerCAPEM reads the issuer's CA Secret out of the control-plane
// cluster and base64-decodes its CA certificate. Mirrors the decode pattern
// in internal/certdist/adapters.go's ReadLeafSecret, but reads ca.crt
// (falling back to tls.crt, since a self-signed-CA issuer's own Secret is
// laid out like a TLS secret with the CA cert in tls.crt) instead of a leaf
// tls.crt/tls.key pair.
func (s *ClientCertificateService) readIssuerCAPEM(ctx context.Context, issuer *models.CertificateIssuer) ([]byte, error) {
	secretName := issuer.Config.CASecretName
	obj, err := s.caReader.Get(ctx, kubernetes.CoreSecretGVR, secretName, true)
	if err != nil {
		return nil, fmt.Errorf("read issuer CA secret %q: %w", secretName, err)
	}

	data, ok, err := unstructured.NestedStringMap(obj.Object, "data")
	if err != nil || !ok {
		return nil, fmt.Errorf("issuer CA secret %q has no data", secretName)
	}

	caEnc, ok := data["ca.crt"]
	if !ok {
		caEnc, ok = data["tls.crt"]
	}
	if !ok {
		return nil, fmt.Errorf("issuer CA secret %q missing ca.crt/tls.crt", secretName)
	}

	caPEM, err := base64.StdEncoding.DecodeString(caEnc)
	if err != nil {
		return nil, fmt.Errorf("decoding CA cert from issuer secret %q: %w", secretName, err)
	}
	return caPEM, nil
}

// DetachCertificate clears a client's managed certificate FK and its
// derived mTLS fields, reverting the client to no mTLS. It is idempotent: a
// client with no managed certificate attached is returned unchanged rather
// than erroring. A previously-configured BYO mTLS configuration is not
// restored automatically -- re-add it via UpdateClientMTLS if that's wanted.
func (s *ClientCertificateService) DetachCertificate(ctx context.Context, clientID, actingUser uuid.UUID) (*models.Client, error) {
	client, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, fmt.Errorf("get client: %w", err)
	}

	if client.ManagedCertificateID == nil {
		return client, nil
	}

	client.ManagedCertificateID = nil
	client.MTLSEnabled = false
	client.MTLSCAName = ""
	client.MTLSCASecret = ""
	client.MTLSCASecretKey = ""
	client.MTLSCAPem = ""
	client.MTLSSANs = nil
	client.MTLSHashes = nil

	if err := s.clientRepo.Update(client); err != nil {
		return nil, fmt.Errorf("update client: %w", err)
	}

	return client, nil
}
