package services

import (
	"encoding/json"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RouteVersionService handles route version history operations
type RouteVersionService struct {
	versionRepo              repository.RouteVersionRepositoryInterface
	routeRepo                repository.RouteRepositoryInterface
	securityPolicyRepo       repository.SecurityPolicyRepositoryInterface
	backendTrafficPolicyRepo repository.BackendTrafficPolicyRepositoryInterface
	envoyExtensionPolicyRepo repository.EnvoyExtensionPolicyRepositoryInterface
	wafPolicyRepo            repository.WafPolicyRepositoryInterface
	routeService             *RouteService
}

// NewRouteVersionService creates a new RouteVersionService
func NewRouteVersionService(
	versionRepo repository.RouteVersionRepositoryInterface,
	routeRepo repository.RouteRepositoryInterface,
) *RouteVersionService {
	return &RouteVersionService{
		versionRepo: versionRepo,
		routeRepo:   routeRepo,
	}
}

// SetSecurityPolicyRepo sets the security policy repository
func (s *RouteVersionService) SetSecurityPolicyRepo(repo repository.SecurityPolicyRepositoryInterface) {
	s.securityPolicyRepo = repo
}

// SetBackendTrafficPolicyRepo sets the backend traffic policy repository
func (s *RouteVersionService) SetBackendTrafficPolicyRepo(repo repository.BackendTrafficPolicyRepositoryInterface) {
	s.backendTrafficPolicyRepo = repo
}

// SetEnvoyExtensionPolicyRepo sets the envoy extension policy repository
func (s *RouteVersionService) SetEnvoyExtensionPolicyRepo(repo repository.EnvoyExtensionPolicyRepositoryInterface) {
	s.envoyExtensionPolicyRepo = repo
}

// SetWafPolicyRepo sets the WAF policy repository
func (s *RouteVersionService) SetWafPolicyRepo(repo repository.WafPolicyRepositoryInterface) {
	s.wafPolicyRepo = repo
}

// SetRouteService sets the route service (for rollback)
func (s *RouteVersionService) SetRouteService(svc *RouteService) {
	s.routeService = svc
}

// CreateVersion snapshots the current route state as a new numbered version.
// Called after a successful deploy.
func (s *RouteVersionService) CreateVersion(route *models.Route, approval *models.Approval, deployedBy uuid.UUID) error {
	// Build the config snapshot from the current DB state
	snapshot := models.RouteApprovalSnapshot{
		RouteConfig: &route.Config,
	}

	// Read current security policy
	if s.securityPolicyRepo != nil {
		sp, err := s.securityPolicyRepo.GetByRouteID(route.ID)
		if err != nil && err != gorm.ErrRecordNotFound {
			return fmt.Errorf("failed to read security policy: %w", err)
		}
		if sp != nil {
			snapshot.SecurityPolicy = &sp.Config
		}
	}

	// Read current backend traffic policy
	if s.backendTrafficPolicyRepo != nil {
		btp, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID)
		if err != nil && err != gorm.ErrRecordNotFound {
			return fmt.Errorf("failed to read backend traffic policy: %w", err)
		}
		if btp != nil {
			snapshot.BackendTrafficPolicy = &btp.Config
		}
	}

	// Read current envoy extension policy
	if s.envoyExtensionPolicyRepo != nil {
		eep, err := s.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
		if err != nil && err != gorm.ErrRecordNotFound {
			return fmt.Errorf("failed to read envoy extension policy: %w", err)
		}
		if eep != nil {
			snapshot.EnvoyExtensionPolicy = &eep.Config
		}
	}

	// Read current WAF policy
	if s.wafPolicyRepo != nil {
		waf, err := s.wafPolicyRepo.GetByRouteID(route.ID)
		if err != nil && err != gorm.ErrRecordNotFound {
			return fmt.Errorf("failed to read WAF policy: %w", err)
		}
		if waf != nil {
			snapshot.WafPolicy = &waf.Config
		}
	}

	// Marshal snapshot to JSON
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal config snapshot: %w", err)
	}

	// Get the next version number
	maxVersion, err := s.versionRepo.GetMaxVersion(route.ID)
	if err != nil {
		return fmt.Errorf("failed to get max version: %w", err)
	}
	nextVersion := maxVersion + 1

	// Determine change description and approval ID
	var changeDescription string
	var approvalID *uuid.UUID
	if approval != nil {
		changeDescription = approval.ChangeDescription
		approvalID = &approval.ID
	}

	version := &models.RouteVersion{
		RouteID:           route.ID,
		Version:           nextVersion,
		ConfigSnapshot:    snapshotJSON,
		RouteDescription:  route.Description,
		Protocol:          route.Protocol,
		SecurityMode:      route.SecurityMode,
		ChangeDescription: changeDescription,
		ApprovalID:        approvalID,
		DeployedBy:        deployedBy,
	}

	if err := s.versionRepo.Create(version); err != nil {
		return fmt.Errorf("failed to create route version: %w", err)
	}

	return nil
}

// ListVersions lists all versions for a route with pagination
func (s *RouteVersionService) ListVersions(routeID uuid.UUID, page, limit int) ([]models.RouteVersion, int64, error) {
	return s.versionRepo.ListByRouteID(routeID, page, limit)
}

// GetVersion gets a specific version of a route
func (s *RouteVersionService) GetVersion(routeID uuid.UUID, version int) (*models.RouteVersion, error) {
	return s.versionRepo.GetByRouteIDAndVersion(routeID, version)
}

// Rollback loads a historical version's config and submits it as a normal route update (through approval flow).
func (s *RouteVersionService) Rollback(routeID uuid.UUID, targetVersion int, submittedBy uuid.UUID) (*models.Route, error) {
	if s.routeService == nil {
		return nil, fmt.Errorf("route service not configured")
	}

	// Load the target version
	rv, err := s.versionRepo.GetByRouteIDAndVersion(routeID, targetVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to get version %d: %w", targetVersion, err)
	}

	// Deserialize the snapshot
	var snapshot models.RouteApprovalSnapshot
	if err := json.Unmarshal(rv.ConfigSnapshot, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config snapshot: %w", err)
	}

	// Build the UpdateRouteInput from the snapshot
	input := &UpdateRouteInput{
		Description:       rv.RouteDescription,
		ChangeDescription: fmt.Sprintf("Rollback to version %d", targetVersion),
	}

	// Map route config
	if snapshot.RouteConfig != nil {
		input.Config = *snapshot.RouteConfig
	}

	// Map security policy config -> SecurityPolicyInput
	if snapshot.SecurityPolicy != nil {
		input.SecurityPolicy = mapSecurityPolicyConfigToInput(snapshot.SecurityPolicy)
	}

	// Map backend traffic policy config -> BackendTrafficPolicyInput
	if snapshot.BackendTrafficPolicy != nil {
		input.BackendTrafficPolicy = mapBackendTrafficPolicyConfigToInput(snapshot.BackendTrafficPolicy)
	}

	// Map envoy extension policy config -> EnvoyExtensionPolicyInput
	if snapshot.EnvoyExtensionPolicy != nil {
		input.ExtensionPolicy = mapEnvoyExtensionPolicyConfigToInput(snapshot.EnvoyExtensionPolicy)
	}

	// Map WAF policy config -> WafPolicyInput
	if snapshot.WafPolicy != nil {
		input.WafPolicy = mapWafPolicyConfigToInput(snapshot.WafPolicy)
	}

	// Submit through normal update flow (which creates an approval)
	return s.routeService.Update(routeID, input, submittedBy)
}

// mapSecurityPolicyConfigToInput converts a stored SecurityPolicyConfig back to SecurityPolicyInput
func mapSecurityPolicyConfigToInput(cfg *models.SecurityPolicyConfig) *SecurityPolicyInput {
	input := &SecurityPolicyInput{
		CORS:    cfg.CORS,
		ExtAuth: cfg.ExtAuth,
	}

	// Map Authorization config -> AuthorizationInput
	if cfg.Authorization != nil && len(cfg.Authorization.Rules) > 0 {
		rule := cfg.Authorization.Rules[0]
		authInput := &AuthorizationInput{
			AllowedCIDRs: rule.Principal.ClientCIDRs,
		}
		if len(rule.Principal.Headers) > 0 {
			authInput.Headers = rule.Principal.Headers
		}
		if rule.Operation != nil && len(rule.Operation.Methods) > 0 {
			authInput.Methods = rule.Operation.Methods
		}
		if len(authInput.AllowedCIDRs) > 0 || len(authInput.Headers) > 0 || len(authInput.Methods) > 0 {
			input.Authorization = authInput
		}
	}

	// Map APIKeyAuth config -> APIKeyAuthInput
	if cfg.APIKeyAuth != nil {
		apiKeyInput := &APIKeyAuthInput{}
		if len(cfg.APIKeyAuth.CredentialRefs) > 0 {
			apiKeyInput.SecretName = cfg.APIKeyAuth.CredentialRefs[0].Name
		}
		if len(cfg.APIKeyAuth.ExtractFrom) > 0 && len(cfg.APIKeyAuth.ExtractFrom[0].Headers) > 0 {
			apiKeyInput.HeaderName = cfg.APIKeyAuth.ExtractFrom[0].Headers[0]
		}
		input.APIKeyAuth = apiKeyInput
	}

	// Map JWT config -> JWTInput
	if cfg.JWT != nil && len(cfg.JWT.Providers) > 0 {
		provider := cfg.JWT.Providers[0]
		jwtInput := &JWTInput{
			Issuer:         provider.Issuer,
			Audiences:      provider.Audiences,
			ClaimToHeaders: provider.ClaimToHeaders,
		}
		if provider.RemoteJWKS != nil {
			jwtInput.JWKSURL = provider.RemoteJWKS.URI
		}
		input.JWT = jwtInput
	}

	// Map OIDC config -> OIDCInput
	if cfg.OIDC != nil {
		oidcInput := &OIDCInput{
			ClientID:     cfg.OIDC.ClientID,
			RedirectURL:  cfg.OIDC.RedirectURL,
			LogoutPath:   cfg.OIDC.LogoutPath,
			Scopes:       cfg.OIDC.Scopes,
			CookieDomain: cfg.OIDC.CookieDomain,
		}
		if cfg.OIDC.Provider != nil {
			oidcInput.Issuer = cfg.OIDC.Provider.Issuer
		}
		if cfg.OIDC.ClientSecret != nil {
			oidcInput.ClientSecretName = cfg.OIDC.ClientSecret.Name
		}
		input.OIDC = oidcInput
	}

	return input
}

// mapBackendTrafficPolicyConfigToInput converts a stored BackendTrafficPolicyConfig back to BackendTrafficPolicyInput
func mapBackendTrafficPolicyConfigToInput(cfg *models.BackendTrafficPolicyConfig) *BackendTrafficPolicyInput {
	return &BackendTrafficPolicyInput{
		Compression:      cfg.Compression,
		Retry:            cfg.Retry,
		LoadBalancer:     cfg.LoadBalancer,
		CircuitBreaker:   cfg.CircuitBreaker,
		HealthCheck:      cfg.HealthCheck,
		FaultInjection:   cfg.FaultInjection,
		RateLimit:        cfg.RateLimit,
		RequestBuffer:    cfg.RequestBuffer,
		ResponseOverride: cfg.ResponseOverride,
		Timeout:          cfg.Timeout,
	}
}

// mapEnvoyExtensionPolicyConfigToInput converts a stored EnvoyExtensionPolicyConfig back to EnvoyExtensionPolicyInput
func mapEnvoyExtensionPolicyConfigToInput(cfg *models.EnvoyExtensionPolicyConfig) *EnvoyExtensionPolicyInput {
	return &EnvoyExtensionPolicyInput{
		Lua:  cfg.Lua,
		Wasm: cfg.Wasm,
	}
}

// mapWafPolicyConfigToInput converts a stored WafPolicyConfig back to WafPolicyInput
func mapWafPolicyConfigToInput(cfg *models.WafPolicyConfig) *WafPolicyInput {
	return &WafPolicyInput{
		Mode:             cfg.Mode,
		Rulesets:         cfg.Rulesets,
		AnomalyThreshold: cfg.AnomalyThreshold,
		ParanoiaLevel:    cfg.ParanoiaLevel,
		DisabledRuleIDs:  cfg.DisabledRuleIDs,
		CustomDirectives: cfg.CustomDirectives,
	}
}
