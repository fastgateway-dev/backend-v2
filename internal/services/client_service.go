package services

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ClientService handles client business logic
type ClientService struct {
	clientRepo           repository.ClientRepositoryInterface
	clientIPRepo         repository.ClientIPRepositoryInterface
	clientHeaderRepo     repository.ClientHeaderRepositoryInterface
	teamRepo             repository.TeamRepositoryInterface
	clientAttachmentRepo repository.ClientAttachmentRepositoryInterface
	routeRepo            repository.RouteRepositoryInterface
	k8sService           KubernetesServiceInterface

	// state is the sole writer of route.Status. See route_state.go.
	// routeRepo arrives through SetRouteRepository rather than the
	// constructor, so state is built there.
	state *routeStateMachine
}

// NewClientService creates a new ClientService
func NewClientService(
	clientRepo repository.ClientRepositoryInterface,
	clientIPRepo repository.ClientIPRepositoryInterface,
	teamRepo repository.TeamRepositoryInterface,
) *ClientService {
	return &ClientService{
		clientRepo:   clientRepo,
		clientIPRepo: clientIPRepo,
		teamRepo:     teamRepo,
	}
}

// SetClientAttachmentRepository sets the client attachment repository (for IP cascade)
func (s *ClientService) SetClientAttachmentRepository(repo repository.ClientAttachmentRepositoryInterface) {
	s.clientAttachmentRepo = repo
}

// SetRouteRepository sets the route repository (for IP cascade).
//
// The state machine is built here, not in NewClientService: routeRepo is not
// a constructor parameter, so this setter is the first point at which it is
// available.
func (s *ClientService) SetRouteRepository(repo repository.RouteRepositoryInterface) {
	s.routeRepo = repo
	s.state = &routeStateMachine{repo: repo}
}

// SetKubernetesService sets the Kubernetes service (for API key secrets)
func (s *ClientService) SetKubernetesService(k8sService KubernetesServiceInterface) {
	s.k8sService = k8sService
}

// SetClientHeaderRepository sets the client header repository
func (s *ClientService) SetClientHeaderRepository(repo repository.ClientHeaderRepositoryInterface) {
	s.clientHeaderRepo = repo
}

// CreateClientInput represents the input for creating a client
type CreateClientInput struct {
	Name               string    `json:"name" binding:"required"`
	Description        string    `json:"description"`
	TeamID             uuid.UUID `json:"teamId" binding:"required"`
	ContactName        string    `json:"contactName"`
	ContactEmail       string    `json:"contactEmail"`
	ClientIDHeaderName string    `json:"clientIdHeaderName"` // Header for client ID routing (default: x-client-id)
}

// UpdateClientInput represents the input for updating a client
type UpdateClientInput struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	ContactName        string `json:"contactName"`
	ContactEmail       string `json:"contactEmail"`
	ClientIDHeaderName string `json:"clientIdHeaderName"` // Header for client ID routing
}

// CreateClientIPInput represents the input for adding an IP address
type CreateClientIPInput struct {
	CIDR        string `json:"cidr" binding:"required"`
	Description string `json:"description"`
}

// CreateClientHeaderInput represents the input for adding a header
type CreateClientHeaderInput struct {
	Name        string   `json:"name" binding:"required"`
	Values      []string `json:"values" binding:"required"`
	Description string   `json:"description"`
}

// Create creates a new client
func (s *ClientService) Create(input *CreateClientInput, createdBy uuid.UUID) (*models.Client, error) {
	// Validate team exists
	_, err := s.teamRepo.GetByID(input.TeamID)
	if err != nil {
		return nil, errors.New("team not found")
	}

	// Check name uniqueness
	exists, err := s.clientRepo.ExistsByName(input.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("client name already exists")
	}

	// Set default client ID header name if not provided
	clientIDHeaderName := input.ClientIDHeaderName
	if clientIDHeaderName == "" {
		clientIDHeaderName = "x-client-id"
	}

	client := &models.Client{
		TeamID:             input.TeamID,
		Name:               input.Name,
		Description:        input.Description,
		ContactName:        input.ContactName,
		ContactEmail:       input.ContactEmail,
		ClientIDHeaderName: clientIDHeaderName,
		CreatedBy:          createdBy,
	}

	if err := s.clientRepo.Create(client); err != nil {
		return nil, err
	}

	// Reload with relationships
	return s.clientRepo.GetByID(client.ID)
}

// GetByID returns a client by ID
func (s *ClientService) GetByID(id uuid.UUID) (*models.Client, error) {
	return s.clientRepo.GetByID(id)
}

// Update updates a client
func (s *ClientService) Update(id uuid.UUID, input *UpdateClientInput) (*models.Client, error) {
	client, err := s.clientRepo.GetByID(id)
	if err != nil {
		return nil, errors.New("client not found")
	}

	// Check name uniqueness if name is being changed
	if input.Name != "" && input.Name != client.Name {
		exists, err := s.clientRepo.ExistsByNameExcluding(input.Name, id)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, errors.New("client name already exists")
		}
		client.Name = input.Name
	}

	if input.Description != "" || input.Description == "" {
		client.Description = input.Description
	}
	if input.ContactName != "" || input.ContactName == "" {
		client.ContactName = input.ContactName
	}
	if input.ContactEmail != "" || input.ContactEmail == "" {
		client.ContactEmail = input.ContactEmail
	}
	if input.ClientIDHeaderName != "" {
		client.ClientIDHeaderName = input.ClientIDHeaderName
	}

	if err := s.clientRepo.Update(client); err != nil {
		return nil, err
	}

	return s.clientRepo.GetByID(id)
}

// Delete deletes a client and cleans up associated K8s resources
func (s *ClientService) Delete(ctx context.Context, id uuid.UUID) error {
	client, err := s.clientRepo.GetByID(id)
	if err != nil {
		return errors.New("client not found")
	}

	// Clean up K8s secrets (API key + mTLS CA)
	// Derive project IDs from the client's team (not attachments, which may already be gone)
	if s.k8sService != nil && s.teamRepo != nil && (client.APIKeyEnabled || client.MTLSEnabled) {
		projectIDs := make(map[uuid.UUID]bool)
		teamProjects, err := s.teamRepo.ListTeamProjects(client.TeamID)
		if err != nil {
			log.Printf("Warning: failed to list team projects for client %s cleanup: %v", id, err)
		}
		for _, tp := range teamProjects {
			if tp.ProjectID != uuid.Nil {
				projectIDs[tp.ProjectID] = true
			}
		}
		for pid := range projectIDs {
			if client.APIKeyEnabled {
				if err := s.k8sService.DeleteAPIKeySecret(ctx, pid, id); err != nil {
					log.Printf("Warning: failed to delete API key secret for client %s in project %s: %v", id, pid, err)
				}
			}
			if client.MTLSEnabled && client.MTLSCASecret != "" {
				if err := s.k8sService.DeleteSecret(ctx, pid, kubernetes.FastGatewayNamespace, client.MTLSCASecret); err != nil {
					log.Printf("Warning: failed to delete mTLS CA secret for client %s in project %s: %v", id, pid, err)
				}
			}
		}
	}

	return s.clientRepo.Delete(id)
}

// List returns paginated clients
func (s *ClientService) List(page, limit int, teamID *uuid.UUID) ([]models.Client, int64, error) {
	return s.clientRepo.List(page, limit, teamID)
}

// AddIP adds an IP address to a client
func (s *ClientService) AddIP(clientID uuid.UUID, input *CreateClientIPInput, createdBy uuid.UUID) (*models.ClientIPAddress, error) {
	// Verify client exists
	_, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	// Validate CIDR format
	cidr := strings.TrimSpace(input.CIDR)
	if err := validateCIDR(cidr); err != nil {
		return nil, err
	}

	ip := &models.ClientIPAddress{
		ClientID:    clientID,
		CIDR:        cidr,
		Description: input.Description,
		CreatedBy:   createdBy,
	}

	if err := s.clientIPRepo.Create(ip); err != nil {
		return nil, err
	}

	// Cascade: mark affected routes as pending_deploy.
	// RETURN: AddIP's success value is the created IP row, which the caller
	// can re-read with ListIPs, so surfacing the cascade failure destroys
	// nothing. A silently stale allowlist would.
	if err := s.cascadeToAttachedRoutes(clientID, s.attachmentsWithIPAllowlist,
		"client ip allowlist changed"); err != nil {
		return nil, err
	}

	return s.clientIPRepo.GetByID(ip.ID)
}

// RemoveIP removes an IP address from a client
func (s *ClientService) RemoveIP(clientID uuid.UUID, ipID uuid.UUID) error {
	ip, err := s.clientIPRepo.GetByID(ipID)
	if err != nil {
		return errors.New("IP address not found")
	}

	if ip.ClientID != clientID {
		return errors.New("IP address does not belong to this client")
	}

	if err := s.clientIPRepo.Delete(ipID); err != nil {
		return err
	}

	// Cascade: mark affected routes as pending_deploy.
	// RETURN: error-only signature, nothing to lose, and a route still
	// serving a removed IP is exactly what the caller needs to hear about.
	return s.cascadeToAttachedRoutes(clientID, s.attachmentsWithIPAllowlist,
		"client ip allowlist changed")
}

// The five attachment queries the cascades used, one adapter each.
//
// They exist because cascadeToAttachedRoutes takes the query as a parameter
// and clientAttachmentRepo is an OPTIONAL dependency: taking a method value
// straight off a nil interface (s.clientAttachmentRepo.ListActiveBy...)
// panics where it is written, before the callee's nil check can run. A
// method value on the non-nil *ClientService is safe, and the body is not
// evaluated until the cascade has checked its wiring.
func (s *ClientService) attachmentsWithIPAllowlist(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	return s.clientAttachmentRepo.ListActiveByClientIDWithIPAllowlist(clientID)
}

func (s *ClientService) attachmentsWithHeaderAuth(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	return s.clientAttachmentRepo.ListActiveByClientIDWithHeaderAuth(clientID)
}

func (s *ClientService) attachmentsWithAPIKey(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	return s.clientAttachmentRepo.ListActiveByClientIDWithAPIKey(clientID)
}

func (s *ClientService) attachmentsWithJWT(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	return s.clientAttachmentRepo.ListActiveByClientIDWithJWT(clientID)
}

// allAttachments backs the allowed-methods cascade. Methods apply to every
// attachment, not just one credential kind, so the query is the unfiltered
// ListByClientID; cascadeToAttachedRoutes applies the active filter that
// cascadeMethodChangeToRoutes used to apply inline.
func (s *ClientService) allAttachments(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	return s.clientAttachmentRepo.ListByClientID(clientID)
}

// cascadeToAttachedRoutes marks every active route attached to this client as
// pending_deploy, so it picks up the client's changed configuration on the
// next deploy.
//
// Before Phase 2D this existed five times -- once per credential kind
// (cascadeIPChangeToRoutes, cascadeMethodChangeToRoutes,
// cascadeHeaderChangeToRoutes, cascadeAPIKeyChangeToRoutes,
// cascadeJWTChangeToRoutes) -- differing only in which repository query
// selected the attachments. Two behaviours changed in the collapse:
//
//  1. ERRORS PROPAGATE. Every copy discarded every failure, including the
//     final routeRepo.Update. After an API-key revocation that meant the
//     route kept serving the revoked credential with nothing logged.
//     Failures are now collected and returned as one aggregate error, and
//     one bad row does NOT stop the fan-out.
//  2. UNIFORM ACTIVE FILTERING. Four copies relied on a pre-filtered
//     ListActiveByClientIDWith* query; cascadeMethodChangeToRoutes used the
//     unfiltered ListByClientID plus a Go-side status check. The check now
//     runs here for every query -- redundant for the filtered ones, and free.
//
// Missing wiring is NOT an error. clientAttachmentRepo and routeRepo arrive
// through optional setters, and every caller's primary side effect (the IP
// row, the new API key, the revoked JWT config) has already been persisted by
// the time the cascade runs. Pre-2D an unwired service made the cascade a
// silent no-op; turning that into a returned error would fail every client
// mutation in a deployment that never wired the cascade at all. It is logged
// instead.
func (s *ClientService) cascadeToAttachedRoutes(
	clientID uuid.UUID,
	list func(uuid.UUID) ([]models.ClientRouteAttachment, error),
	reason string,
) error {
	// DELIBERATE, TIME-BOXED DEVIATION from master design section 6.6
	// (constructor wiring), recorded under controller ruling R13 -- not
	// considered design, and not to be read as one.
	//
	// cmd/server/main.go wires both repositories unconditionally, so in
	// production this branch is unreachable; the nil path exists only because
	// the test tree constructs ClientService without them. The consistent fix
	// is to make clientAttachmentRepo and routeRepo constructor parameters
	// and update the call sites, which IS section 6.6 and belongs to Phase
	// 2E. Until then this logs and returns nil rather than erroring, because
	// returning an error here -- with 8 of the 9 call sites now propagating
	// it -- would fail client mutations for every caller that has not wired
	// the cascade.
	if s.clientAttachmentRepo == nil || s.routeRepo == nil || s.state == nil {
		log.Printf("WARNING: cannot cascade %q for client %s: route repository not wired", reason, clientID)
		return nil
	}

	attachments, err := list(clientID)
	if err != nil {
		return fmt.Errorf("cascade %q for client %s: list attachments: %w", reason, clientID, err)
	}

	var failures []error
	for _, attachment := range attachments {
		if attachment.Status != models.AttachmentStatusActive {
			continue
		}
		route, err := s.routeRepo.GetByID(attachment.RouteID)
		if err != nil {
			failures = append(failures, fmt.Errorf("route %s: %w", attachment.RouteID, err))
			continue
		}
		// Only routes that are live in Kubernetes need redeploying; anything
		// else picks the change up when it is deployed for the first time.
		if route.Status != models.RouteStatusActive {
			continue
		}
		if err := s.state.To(route, models.RouteStatusPendingDeploy, reason); err != nil {
			failures = append(failures, fmt.Errorf("route %s: %w", route.ID, err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("cascade %q for client %s: %w", reason, clientID, errors.Join(failures...))
	}
	return nil
}

// ListIPs returns all IP addresses for a client
func (s *ClientService) ListIPs(clientID uuid.UUID) ([]models.ClientIPAddress, error) {
	// Verify client exists
	_, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	return s.clientIPRepo.ListByClientID(clientID)
}

// AddHeader adds a header to a client
func (s *ClientService) AddHeader(clientID uuid.UUID, input *CreateClientHeaderInput, createdBy uuid.UUID) (*models.ClientHeader, error) {
	_, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, errors.New("header name is required")
	}
	if len(input.Values) == 0 {
		return nil, errors.New("at least one header value is required")
	}
	for _, v := range input.Values {
		if strings.TrimSpace(v) == "" {
			return nil, errors.New("header values must not be empty")
		}
	}

	header := &models.ClientHeader{
		ClientID:    clientID,
		Name:        name,
		Values:      models.StringList(input.Values),
		Description: input.Description,
		CreatedBy:   createdBy,
	}

	if err := s.clientHeaderRepo.Create(header); err != nil {
		return nil, err
	}

	// RETURN: the created header is re-readable via ListHeaders.
	if err := s.cascadeToAttachedRoutes(clientID, s.attachmentsWithHeaderAuth,
		"client header auth changed"); err != nil {
		return nil, err
	}

	return s.clientHeaderRepo.GetByID(header.ID)
}

// RemoveHeader removes a header from a client
func (s *ClientService) RemoveHeader(clientID uuid.UUID, headerID uuid.UUID) error {
	header, err := s.clientHeaderRepo.GetByID(headerID)
	if err != nil {
		return errors.New("header not found")
	}
	if header.ClientID != clientID {
		return errors.New("header does not belong to this client")
	}
	if err := s.clientHeaderRepo.Delete(headerID); err != nil {
		return err
	}
	// RETURN: error-only signature; a route still matching a removed header
	// value is a live authorization gap.
	return s.cascadeToAttachedRoutes(clientID, s.attachmentsWithHeaderAuth,
		"client header auth changed")
}

// ListHeaders returns all headers for a client
func (s *ClientService) ListHeaders(clientID uuid.UUID) ([]models.ClientHeader, error) {
	_, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}
	return s.clientHeaderRepo.ListByClientID(clientID)
}

// SetAllowedMethods sets the allowed HTTP methods for a client
func (s *ClientService) SetAllowedMethods(clientID uuid.UUID, methods []string) (*models.Client, error) {
	client, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	// Validate methods
	validMethods := map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true, "OPTIONS": true}
	for _, m := range methods {
		if !validMethods[strings.ToUpper(m)] {
			return nil, fmt.Errorf("invalid HTTP method: %s", m)
		}
	}

	// Normalize to uppercase and deduplicate
	seen := make(map[string]bool)
	var normalized []string
	for _, m := range methods {
		upper := strings.ToUpper(m)
		if !seen[upper] {
			seen[upper] = true
			normalized = append(normalized, upper)
		}
	}

	if len(normalized) == 0 {
		client.AllowedMethods = nil
	} else {
		client.AllowedMethods = models.StringList(normalized)
	}

	if err := s.clientRepo.Update(client); err != nil {
		return nil, err
	}

	// Cascade: mark affected routes as pending_deploy.
	// RETURN: the success value is the client record, already persisted and
	// re-readable with Get.
	if err := s.cascadeToAttachedRoutes(clientID, s.allAttachments,
		"client allowed methods changed"); err != nil {
		return nil, err
	}

	return s.clientRepo.GetByID(clientID)
}

// validateCIDR validates that the input is a valid CIDR notation or IP address
func validateCIDR(cidr string) error {
	// Try parsing as CIDR first
	_, _, err := net.ParseCIDR(cidr)
	if err == nil {
		return nil
	}

	// Try parsing as plain IP address (will be treated as /32 or /128)
	ip := net.ParseIP(cidr)
	if ip != nil {
		return nil
	}

	return errors.New("invalid CIDR notation or IP address: " + cidr)
}

// GenerateAPIKeyInput represents the input for generating an API key
type GenerateAPIKeyInput struct {
	HeaderName string `json:"headerName"`
}

// GenerateAPIKeyResponse represents the response from generating an API key
type GenerateAPIKeyResponse struct {
	APIKey     string    `json:"apiKey"`     // Only shown once
	Prefix     string    `json:"prefix"`     // e.g., "fg_live_xxxx"
	HeaderName string    `json:"headerName"` // Header to use for requests
	CreatedAt  time.Time `json:"createdAt"`
}

// GenerateAPIKey generates a new API key for a client
func (s *ClientService) GenerateAPIKey(ctx context.Context, clientID uuid.UUID, input *GenerateAPIKeyInput, createdBy uuid.UUID) (*GenerateAPIKeyResponse, error) {
	client, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	// Default header name
	headerName := "x-api-key"
	if input != nil && input.HeaderName != "" {
		headerName = strings.ToLower(strings.TrimSpace(input.HeaderName))
	}

	// Generate API key: fg_live_ + 24 random hex characters
	randomBytes := make([]byte, 12)
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, errors.New("failed to generate random bytes")
	}
	apiKey := "fg_live_" + hex.EncodeToString(randomBytes)
	prefix := apiKey[:12] + "****" // "fg_live_xxxx****"

	// Hash the API key for audit
	hash, err := bcrypt.GenerateFromPassword([]byte(apiKey), bcrypt.DefaultCost)
	if err != nil {
		return nil, errors.New("failed to hash API key")
	}

	// Encode the API key for storage (base64 encoding - in production use AES encryption)
	encodedKey := base64.StdEncoding.EncodeToString([]byte(apiKey))

	// Update client record (K8s secrets will be created at deploy time per project)
	now := time.Now()
	client.APIKeyEnabled = true
	client.APIKeyHash = string(hash)
	client.APIKeyEncrypted = encodedKey
	client.APIKeyPrefix = prefix
	client.APIKeyHeaderName = headerName
	client.APIKeyCreatedAt = &now
	client.APIKeyCreatedBy = &createdBy

	if err := s.clientRepo.Update(client); err != nil {
		return nil, err
	}

	// Cascade: mark affected routes as pending_deploy.
	// LOG, do not return. This is the one call site of the nine where
	// reporting the error would destroy information: the response below
	// carries the PLAINTEXT api key, which is shown once and is not
	// recoverable from the database (only its bcrypt hash is stored). The
	// client record has already been updated, so returning here would leave
	// a client holding a key nobody can read. The cascade only delays a
	// redeploy; losing the key does not.
	if err := s.cascadeToAttachedRoutes(clientID, s.attachmentsWithAPIKey,
		"client api key rotated"); err != nil {
		log.Printf("WARNING: %v", err)
	}

	return &GenerateAPIKeyResponse{
		APIKey:     apiKey,
		Prefix:     prefix,
		HeaderName: headerName,
		CreatedAt:  now,
	}, nil
}

// RevokeAPIKey revokes the API key for a client
func (s *ClientService) RevokeAPIKey(ctx context.Context, clientID uuid.UUID) error {
	client, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return errors.New("client not found")
	}

	if !client.APIKeyEnabled {
		return errors.New("client does not have an API key")
	}

	// Clear API key fields (K8s secrets will be cleaned up during route undeploy/redeploy)
	client.APIKeyEnabled = false
	client.APIKeyHash = ""
	client.APIKeyEncrypted = ""
	client.APIKeyPrefix = ""
	client.APIKeyCreatedAt = nil
	client.APIKeyCreatedBy = nil

	if err := s.clientRepo.Update(client); err != nil {
		return err
	}

	// Cascade: mark affected routes as pending_deploy.
	// RETURN: this is the motivating case for the change. Swallowed here, a
	// failed cascade leaves the route serving the REVOKED credential with
	// nothing logged.
	return s.cascadeToAttachedRoutes(clientID, s.attachmentsWithAPIKey,
		"client api key revoked")
}

// GetAPIKeyForDeploy retrieves the plaintext API key from DB for deployment
// This is used during route deployment to get the actual key value for HTTPRoute matching
func (s *ClientService) GetAPIKeyForDeploy(_ context.Context, client *models.Client) (string, error) {
	if !client.APIKeyEnabled {
		return "", errors.New("client does not have an API key enabled")
	}

	if client.APIKeyEncrypted == "" {
		return "", errors.New("API key data not found")
	}

	// Decode the API key (base64 encoded - in production use AES decryption)
	decoded, err := base64.StdEncoding.DecodeString(client.APIKeyEncrypted)
	if err != nil {
		return "", errors.New("failed to decode API key")
	}

	return string(decoded), nil
}

// ConfigureJWTInput represents the input for configuring JWT authentication
type ConfigureJWTInput struct {
	Issuer         string                    `json:"issuer" binding:"required"`
	JWKSURL        string                    `json:"jwksUrl" binding:"required"`
	Audiences      []string                  `json:"audiences,omitempty"`
	RequiredClaims []models.JWTRequiredClaim `json:"requiredClaims,omitempty"`
	ClaimToHeaders []models.JWTClaimToHeader `json:"claimToHeaders,omitempty"`
}

// ConfigureJWTResponse represents the response from configuring JWT
type ConfigureJWTResponse struct {
	JWTEnabled        bool                      `json:"jwtEnabled"`
	JWTIssuer         string                    `json:"jwtIssuer"`
	JWTJWKSURL        string                    `json:"jwtJwksUrl"`
	JWTAudiences      []string                  `json:"jwtAudiences,omitempty"`
	JWTRequiredClaims []models.JWTRequiredClaim `json:"jwtRequiredClaims,omitempty"`
	JWTClaimToHeaders []models.JWTClaimToHeader `json:"jwtClaimToHeaders,omitempty"`
	JWTCreatedAt      time.Time                 `json:"jwtCreatedAt"`
	JWTCreatedBy      *uuid.UUID                `json:"jwtCreatedBy,omitempty"`
}

// ConfigureJWT configures JWT authentication for a client
func (s *ClientService) ConfigureJWT(ctx context.Context, clientID uuid.UUID, input *ConfigureJWTInput, createdBy uuid.UUID) (*ConfigureJWTResponse, error) {
	client, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	// Validate issuer URL
	issuer := strings.TrimSpace(input.Issuer)
	if issuer == "" {
		return nil, errors.New("issuer is required")
	}
	if _, err := url.ParseRequestURI(issuer); err != nil {
		return nil, errors.New("invalid issuer URL format")
	}

	// Validate JWKS URL
	jwksURL := strings.TrimSpace(input.JWKSURL)
	if jwksURL == "" {
		return nil, errors.New("jwksUrl is required")
	}
	parsedJWKSURL, err := url.ParseRequestURI(jwksURL)
	if err != nil {
		return nil, errors.New("invalid JWKS URL format")
	}
	if parsedJWKSURL.Scheme != "https" && parsedJWKSURL.Scheme != "http" {
		return nil, errors.New("jwksUrl must use HTTP or HTTPS scheme")
	}

	// Validate required claims if provided
	for _, claim := range input.RequiredClaims {
		if claim.Name == "" {
			return nil, errors.New("claim name is required")
		}
		if len(claim.Values) == 0 {
			return nil, errors.New("claim values are required")
		}
		// Default to Exact if not specified
		if claim.ValueType != "" && claim.ValueType != "Exact" && claim.ValueType != "StringContains" {
			return nil, errors.New("claim valueType must be 'Exact' or 'StringContains'")
		}
	}

	// Validate claim to headers if provided
	for _, mapping := range input.ClaimToHeaders {
		if mapping.Claim == "" {
			return nil, errors.New("claim name is required for claim-to-header mapping")
		}
		if mapping.Header == "" {
			return nil, errors.New("header name is required for claim-to-header mapping")
		}
	}

	// Update client record
	now := time.Now()
	client.JWTEnabled = true
	client.JWTIssuer = issuer
	client.JWTJWKSURL = jwksURL
	client.JWTAudiences = input.Audiences
	client.JWTRequiredClaims = input.RequiredClaims
	client.JWTClaimToHeaders = input.ClaimToHeaders
	client.JWTCreatedAt = &now
	client.JWTCreatedBy = &createdBy

	if err := s.clientRepo.Update(client); err != nil {
		return nil, errors.New("internal: failed to update client")
	}

	// Cascade: mark affected routes as pending_deploy.
	// RETURN: the response is a projection of the client record just
	// persisted, so it is reconstructible from a Get.
	if err := s.cascadeToAttachedRoutes(clientID, s.attachmentsWithJWT,
		"client jwt configured"); err != nil {
		return nil, err
	}

	return &ConfigureJWTResponse{
		JWTEnabled:        true,
		JWTIssuer:         issuer,
		JWTJWKSURL:        jwksURL,
		JWTAudiences:      input.Audiences,
		JWTRequiredClaims: input.RequiredClaims,
		JWTClaimToHeaders: input.ClaimToHeaders,
		JWTCreatedAt:      now,
		JWTCreatedBy:      &createdBy,
	}, nil
}

// RemoveJWT removes JWT authentication from a client
func (s *ClientService) RemoveJWT(ctx context.Context, clientID uuid.UUID) error {
	client, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return errors.New("client not found")
	}

	if !client.JWTEnabled {
		return errors.New("client does not have JWT configured")
	}

	// Clear JWT fields
	client.JWTEnabled = false
	client.JWTIssuer = ""
	client.JWTJWKSURL = ""
	client.JWTAudiences = nil
	client.JWTRequiredClaims = nil
	client.JWTClaimToHeaders = nil
	client.JWTCreatedAt = nil
	client.JWTCreatedBy = nil

	if err := s.clientRepo.Update(client); err != nil {
		return errors.New("internal: failed to update client")
	}

	// Cascade: mark affected routes as pending_deploy.
	// RETURN: error-only signature, and like RevokeAPIKey a stale route keeps
	// accepting the JWT config that was just removed.
	return s.cascadeToAttachedRoutes(clientID, s.attachmentsWithJWT,
		"client jwt removed")
}

// =============================================================================
// mTLS Configuration
// =============================================================================

// UpdateClientMTLSInput represents input for updating client mTLS configuration
type UpdateClientMTLSInput struct {
	Enabled bool                  `json:"enabled"`
	CAName  string                `json:"caName"`
	CAPem   string                `json:"caPem"`
	SANs    []models.MTLSSANEntry `json:"sans"`
	Hashes  []string              `json:"hashes"`
}

// validateClientCAPEM validates a PEM-encoded CA certificate for client mTLS
func validateClientCAPEM(pemData string) error {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return errors.New("failed to decode PEM data")
	}
	if block.Type != "CERTIFICATE" {
		return fmt.Errorf("expected CERTIFICATE, got %s", block.Type)
	}
	_, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("invalid certificate: %w", err)
	}
	return nil
}

// UpdateClientMTLS updates the mTLS configuration for a client
func (s *ClientService) UpdateClientMTLS(ctx context.Context, clientID uuid.UUID, input *UpdateClientMTLSInput, updatedBy uuid.UUID) (*models.Client, error) {
	client, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	if input.Enabled {
		// Validate CA PEM
		if input.CAPem == "" {
			return nil, errors.New("CA certificate is required")
		}
		if err := validateClientCAPEM(input.CAPem); err != nil {
			return nil, fmt.Errorf("invalid CA certificate: %w", err)
		}

		// Validate at least one identifier
		if len(input.SANs) == 0 && len(input.Hashes) == 0 {
			return nil, errors.New("at least one SAN or certificate hash is required")
		}

		// Validate SANs
		for i, san := range input.SANs {
			if san.Type != "DNS" && san.Type != "URI" {
				return nil, fmt.Errorf("sans[%d]: type must be 'DNS' or 'URI'", i)
			}
			if san.Value == "" {
				return nil, fmt.Errorf("sans[%d]: value cannot be empty", i)
			}
		}

		// Validate hashes
		for i, hash := range input.Hashes {
			if len(hash) != 64 {
				return nil, fmt.Errorf("hashes[%d]: must be 64 hex characters", i)
			}
		}

		// Secret name for client CA
		secretName := fmt.Sprintf("fastgateway-client-%s-mtls-ca", clientID.String()[:8])

		// Update client
		now := time.Now()
		client.MTLSEnabled = true
		client.MTLSCAName = input.CAName
		client.MTLSCASecret = secretName
		client.MTLSCASecretKey = "ca.crt"
		client.MTLSCAPem = input.CAPem
		client.MTLSSANs = input.SANs
		client.MTLSHashes = input.Hashes
		client.MTLSCreatedAt = &now
		client.MTLSCreatedBy = &updatedBy
	} else {
		// Disable mTLS
		// Note: Active mTLS attachment check will be done by the handler
		// since it requires checking the attachment status

		// Delete K8s Secret for client CA (created at deploy time)
		if client.MTLSCASecret != "" && s.k8sService != nil && s.teamRepo != nil {
			teamProjects, tpErr := s.teamRepo.ListTeamProjects(client.TeamID)
			if tpErr == nil {
				for _, tp := range teamProjects {
					if tp.ProjectID != uuid.Nil {
						_ = s.k8sService.DeleteSecret(ctx, tp.ProjectID, kubernetes.FastGatewayNamespace, client.MTLSCASecret)
					}
				}
			}
		}

		// Clear mTLS fields
		client.MTLSEnabled = false
		client.MTLSCAName = ""
		client.MTLSCASecret = ""
		client.MTLSCASecretKey = ""
		client.MTLSCAPem = ""
		client.MTLSSANs = nil
		client.MTLSHashes = nil
	}

	if err := s.clientRepo.Update(client); err != nil {
		return nil, err
	}

	return client, nil
}
