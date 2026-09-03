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
	"net/url"
	"strings"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

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
		if client.MTLSCASecret != "" {
			teamProjects, tpErr := s.teamRepo.ListTeamProjects(client.TeamID)
			if tpErr == nil {
				for _, tp := range teamProjects {
					if tp.ProjectID != uuid.Nil {
						_ = s.k8sSecrets.DeleteSecret(ctx, tp.ProjectID, kubernetes.FastGatewayNamespace, client.MTLSCASecret)
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
