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

// SetRouteRepository sets the route repository (for IP cascade)
func (s *ClientService) SetRouteRepository(repo repository.RouteRepositoryInterface) {
	s.routeRepo = repo
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

	// Cascade: mark affected routes as pending_deploy
	s.cascadeIPChangeToRoutes(clientID)

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

	// Cascade: mark affected routes as pending_deploy
	s.cascadeIPChangeToRoutes(clientID)

	return nil
}

// cascadeIPChangeToRoutes marks routes with active IP-allowlisted attachments
// for this client as pending_deploy so they pick up the new IPs on next deploy
func (s *ClientService) cascadeIPChangeToRoutes(clientID uuid.UUID) {
	if s.clientAttachmentRepo == nil || s.routeRepo == nil {
		return
	}

	// Get active attachments with IP allowlisting for this client
	attachments, err := s.clientAttachmentRepo.ListActiveByClientIDWithIPAllowlist(clientID)
	if err != nil {
		return
	}

	for _, attachment := range attachments {
		route, err := s.routeRepo.GetByID(attachment.RouteID)
		if err != nil {
			continue
		}

		// Only mark as pending_deploy if currently active
		if route.Status == models.RouteStatusActive {
			route.Status = models.RouteStatusPendingDeploy
			if err := s.routeRepo.Update(route); err != nil {
				// Log and continue, don't fail the IP operation
				continue
			}
		}
	}
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

	s.cascadeHeaderChangeToRoutes(clientID)

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
	s.cascadeHeaderChangeToRoutes(clientID)
	return nil
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

	// Cascade: mark affected routes as pending_deploy
	s.cascadeMethodChangeToRoutes(clientID)

	return s.clientRepo.GetByID(clientID)
}

// cascadeMethodChangeToRoutes marks routes with active attachments
// for this client as pending_deploy so they pick up the method changes on next deploy.
// Uses ListByClientID (broader than header-auth only) because methods apply to all attachments.
func (s *ClientService) cascadeMethodChangeToRoutes(clientID uuid.UUID) {
	if s.clientAttachmentRepo == nil || s.routeRepo == nil {
		return
	}
	attachments, err := s.clientAttachmentRepo.ListByClientID(clientID)
	if err != nil {
		return
	}
	for _, attachment := range attachments {
		if attachment.Status != models.AttachmentStatusActive {
			continue
		}
		route, err := s.routeRepo.GetByID(attachment.RouteID)
		if err != nil {
			continue
		}
		if route.Status == models.RouteStatusActive {
			route.Status = models.RouteStatusPendingDeploy
			_ = s.routeRepo.Update(route)
		}
	}
}

// cascadeHeaderChangeToRoutes marks routes with active header-auth attachments
// for this client as pending_deploy so they pick up the new headers on next deploy
func (s *ClientService) cascadeHeaderChangeToRoutes(clientID uuid.UUID) {
	if s.clientAttachmentRepo == nil || s.routeRepo == nil {
		return
	}
	attachments, err := s.clientAttachmentRepo.ListActiveByClientIDWithHeaderAuth(clientID)
	if err != nil {
		return
	}
	for _, attachment := range attachments {
		route, err := s.routeRepo.GetByID(attachment.RouteID)
		if err != nil {
			continue
		}
		if route.Status == models.RouteStatusActive {
			route.Status = models.RouteStatusPendingDeploy
			_ = s.routeRepo.Update(route)
		}
	}
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

	// Cascade: mark affected routes as pending_deploy
	s.cascadeAPIKeyChangeToRoutes(clientID)

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

	// Cascade: mark affected routes as pending_deploy
	s.cascadeAPIKeyChangeToRoutes(clientID)

	return nil
}

// cascadeAPIKeyChangeToRoutes marks routes with active API-key-enabled attachments
// for this client as pending_deploy so they pick up the change on next deploy
func (s *ClientService) cascadeAPIKeyChangeToRoutes(clientID uuid.UUID) {
	if s.clientAttachmentRepo == nil || s.routeRepo == nil {
		return
	}

	// Get active attachments with API key for this client
	attachments, err := s.clientAttachmentRepo.ListActiveByClientIDWithAPIKey(clientID)
	if err != nil {
		return
	}

	for _, attachment := range attachments {
		route, err := s.routeRepo.GetByID(attachment.RouteID)
		if err != nil {
			continue
		}

		// Only mark as pending_deploy if currently active
		if route.Status == models.RouteStatusActive {
			route.Status = models.RouteStatusPendingDeploy
			if err := s.routeRepo.Update(route); err != nil {
				// Log and continue, don't fail the API key operation
				continue
			}
		}
	}
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

	// Cascade: mark affected routes as pending_deploy
	s.cascadeJWTChangeToRoutes(clientID)

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

	// Cascade: mark affected routes as pending_deploy
	s.cascadeJWTChangeToRoutes(clientID)

	return nil
}

// cascadeJWTChangeToRoutes marks routes with active JWT-enabled attachments
// for this client as pending_deploy so they pick up the change on next deploy
func (s *ClientService) cascadeJWTChangeToRoutes(clientID uuid.UUID) {
	if s.clientAttachmentRepo == nil || s.routeRepo == nil {
		log.Printf("WARNING: Cannot cascade JWT change for client %s: missing repository", clientID)
		return
	}

	// Get active attachments with JWT for this client
	attachments, err := s.clientAttachmentRepo.ListActiveByClientIDWithJWT(clientID)
	if err != nil {
		log.Printf("WARNING: Failed to list JWT attachments for client %s: %v", clientID, err)
		return
	}

	for _, attachment := range attachments {
		route, err := s.routeRepo.GetByID(attachment.RouteID)
		if err != nil {
			log.Printf("WARNING: Failed to get route %s for JWT cascade: %v", attachment.RouteID, err)
			continue
		}

		// Only mark as pending_deploy if currently active
		if route.Status == models.RouteStatusActive {
			route.Status = models.RouteStatusPendingDeploy
			if err := s.routeRepo.Update(route); err != nil {
				log.Printf("WARNING: Failed to cascade JWT change to route %s: %v", route.ID, err)
				continue
			}
		}
	}
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
