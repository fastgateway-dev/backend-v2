package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
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
	routeUpdater             RouteUpdater
}

// RouteUpdater resubmits a route configuration through the normal update
// (and therefore approval) flow. Update is the only method
// RouteVersionService calls on RouteService, from Rollback.
//
// RouteVersionService declares it; *RouteService satisfies it structurally.
// RouteService in turn needs RouteVersionRecorder, so the two form a genuine
// cycle: main.go supplies this side as a RouteUpdaterFunc closure over the
// routeService variable, which keeps the dependency required at construction
// instead of arriving through a setter afterwards.
type RouteUpdater interface {
	Update(routeID uuid.UUID, input *UpdateRouteInput, submittedBy uuid.UUID) (*models.Route, error)
}

// RouteUpdaterFunc adapts a plain function to RouteUpdater.
type RouteUpdaterFunc func(routeID uuid.UUID, input *UpdateRouteInput, submittedBy uuid.UUID) (*models.Route, error)

// Update calls f.
func (f RouteUpdaterFunc) Update(routeID uuid.UUID, input *UpdateRouteInput, submittedBy uuid.UUID) (*models.Route, error) {
	return f(routeID, input, submittedBy)
}

// RouteVersionServiceDeps carries everything RouteVersionService needs.
// Every field is required: before Phase 2E four of these arrived through
// setters, and nil-guards existed to tolerate the ones that might not have
// been called.
type RouteVersionServiceDeps struct {
	VersionRepo              repository.RouteVersionRepositoryInterface
	RouteRepo                repository.RouteRepositoryInterface
	SecurityPolicyRepo       repository.SecurityPolicyRepositoryInterface
	BackendTrafficPolicyRepo repository.BackendTrafficPolicyRepositoryInterface
	EnvoyExtensionPolicyRepo repository.EnvoyExtensionPolicyRepositoryInterface
	WafPolicyRepo            repository.WafPolicyRepositoryInterface

	// RouteUpdater resubmits a historical configuration through the normal
	// update flow on Rollback. See RouteUpdater.
	RouteUpdater RouteUpdater
}

// NewRouteVersionService builds a fully-wired RouteVersionService. It panics
// if a required dependency is missing: before Phase 2E these arrived through
// setters after construction, so a forgotten wiring line degraded silently
// at runtime instead of failing at start-up. Master design section 6.6.
func NewRouteVersionService(deps RouteVersionServiceDeps) *RouteVersionService {
	var missing []string
	if deps.VersionRepo == nil {
		missing = append(missing, "VersionRepo")
	}
	if deps.RouteRepo == nil {
		missing = append(missing, "RouteRepo")
	}
	if deps.SecurityPolicyRepo == nil {
		missing = append(missing, "SecurityPolicyRepo")
	}
	if deps.BackendTrafficPolicyRepo == nil {
		missing = append(missing, "BackendTrafficPolicyRepo")
	}
	if deps.EnvoyExtensionPolicyRepo == nil {
		missing = append(missing, "EnvoyExtensionPolicyRepo")
	}
	if deps.WafPolicyRepo == nil {
		missing = append(missing, "WafPolicyRepo")
	}
	if deps.RouteUpdater == nil {
		missing = append(missing, "RouteUpdater")
	}
	if len(missing) > 0 {
		panic("services.NewRouteVersionService: missing required dependency: " + strings.Join(missing, ", "))
	}

	return &RouteVersionService{
		versionRepo:              deps.VersionRepo,
		routeRepo:                deps.RouteRepo,
		securityPolicyRepo:       deps.SecurityPolicyRepo,
		backendTrafficPolicyRepo: deps.BackendTrafficPolicyRepo,
		envoyExtensionPolicyRepo: deps.EnvoyExtensionPolicyRepo,
		wafPolicyRepo:            deps.WafPolicyRepo,
		routeUpdater:             deps.RouteUpdater,
	}
}

// CreateVersion snapshots the current route state as a new numbered version.
// Called after a successful deploy.
func (s *RouteVersionService) CreateVersion(route *models.Route, approval *models.Approval, deployedBy uuid.UUID) error {
	// Build the config snapshot from the current DB state
	snapshot := models.RouteApprovalSnapshot{
		RouteConfig: &route.Config,
	}

	// Read current security policy
	sp, err := s.securityPolicyRepo.GetByRouteID(route.ID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed to read security policy: %w", err)
	}
	if sp != nil {
		snapshot.SecurityPolicy = &sp.Config
	}

	// Read current backend traffic policy
	btp, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed to read backend traffic policy: %w", err)
	}
	if btp != nil {
		snapshot.BackendTrafficPolicy = &btp.Config
	}

	// Read current envoy extension policy
	eep, err := s.envoyExtensionPolicyRepo.GetByRouteID(route.ID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed to read envoy extension policy: %w", err)
	}
	if eep != nil {
		snapshot.EnvoyExtensionPolicy = &eep.Config
	}

	// Read current WAF policy
	waf, err := s.wafPolicyRepo.GetByRouteID(route.ID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed to read WAF policy: %w", err)
	}
	if waf != nil {
		snapshot.WafPolicy = &waf.Config
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

	// Map security policy config -> routeplan.SecurityPolicyInput
	if snapshot.SecurityPolicy != nil {
		input.SecurityPolicy = mapSecurityPolicyConfigToInput(snapshot.SecurityPolicy)
	}

	// Map backend traffic policy config -> routeplan.BackendTrafficPolicyInput
	if snapshot.BackendTrafficPolicy != nil {
		input.BackendTrafficPolicy = routeplan.MapBackendTrafficPolicyConfigToInput(snapshot.BackendTrafficPolicy)
	}

	// Map envoy extension policy config -> routeplan.EnvoyExtensionPolicyInput
	if snapshot.EnvoyExtensionPolicy != nil {
		input.ExtensionPolicy = mapEnvoyExtensionPolicyConfigToInput(snapshot.EnvoyExtensionPolicy)
	}

	// Map WAF policy config -> routeplan.WafPolicyInput
	if snapshot.WafPolicy != nil {
		input.WafPolicy = mapWafPolicyConfigToInput(snapshot.WafPolicy)
	}

	// Submit through normal update flow (which creates an approval)
	return s.routeUpdater.Update(routeID, input, submittedBy)
}

// mapSecurityPolicyConfigToInput converts a stored models.SecurityPolicyConfig back to routeplan.SecurityPolicyInput
func mapSecurityPolicyConfigToInput(cfg *models.SecurityPolicyConfig) *routeplan.SecurityPolicyInput {
	input := &routeplan.SecurityPolicyInput{
		CORS:    cfg.CORS,
		ExtAuth: cfg.ExtAuth,
	}

	// Map Authorization config -> routeplan.AuthorizationInput
	if cfg.Authorization != nil && len(cfg.Authorization.Rules) > 0 {
		rule := cfg.Authorization.Rules[0]
		authInput := &routeplan.AuthorizationInput{
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

	// Map APIKeyAuth config -> routeplan.APIKeyAuthInput
	if cfg.APIKeyAuth != nil {
		apiKeyInput := &routeplan.APIKeyAuthInput{}
		if len(cfg.APIKeyAuth.CredentialRefs) > 0 {
			apiKeyInput.SecretName = cfg.APIKeyAuth.CredentialRefs[0].Name
		}
		if len(cfg.APIKeyAuth.ExtractFrom) > 0 && len(cfg.APIKeyAuth.ExtractFrom[0].Headers) > 0 {
			apiKeyInput.HeaderName = cfg.APIKeyAuth.ExtractFrom[0].Headers[0]
		}
		input.APIKeyAuth = apiKeyInput
	}

	// Map JWT config -> routeplan.JWTInput
	if cfg.JWT != nil && len(cfg.JWT.Providers) > 0 {
		provider := cfg.JWT.Providers[0]
		jwtInput := &routeplan.JWTInput{
			Issuer:         provider.Issuer,
			Audiences:      provider.Audiences,
			ClaimToHeaders: provider.ClaimToHeaders,
		}
		if provider.RemoteJWKS != nil {
			jwtInput.JWKSURL = provider.RemoteJWKS.URI
		}
		input.JWT = jwtInput
	}

	// Map OIDC config -> routeplan.OIDCInput
	if cfg.OIDC != nil {
		oidcInput := &routeplan.OIDCInput{
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

// mapEnvoyExtensionPolicyConfigToInput converts a stored models.EnvoyExtensionPolicyConfig back to routeplan.EnvoyExtensionPolicyInput
func mapEnvoyExtensionPolicyConfigToInput(cfg *models.EnvoyExtensionPolicyConfig) *routeplan.EnvoyExtensionPolicyInput {
	return &routeplan.EnvoyExtensionPolicyInput{
		Lua:  cfg.Lua,
		Wasm: cfg.Wasm,
	}
}

// mapWafPolicyConfigToInput converts a stored models.WafPolicyConfig back to routeplan.WafPolicyInput
func mapWafPolicyConfigToInput(cfg *models.WafPolicyConfig) *routeplan.WafPolicyInput {
	return &routeplan.WafPolicyInput{
		Mode:             cfg.Mode,
		Rulesets:         cfg.Rulesets,
		AnomalyThreshold: cfg.AnomalyThreshold,
		ParanoiaLevel:    cfg.ParanoiaLevel,
		DisabledRuleIDs:  cfg.DisabledRuleIDs,
		CustomDirectives: cfg.CustomDirectives,
	}
}
