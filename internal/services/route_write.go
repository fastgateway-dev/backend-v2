package services

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
)

// ensureReferenceGrantsForDomain verifies backend namespace ReferenceGrants include
// the domain's namespace. This is a deploy-time safety net.
func (s *RouteService) ensureReferenceGrantsForDomain(ctx context.Context, route *models.Route, domain *models.Domain) {
	if len(route.Config.Backends) == 0 {
		return
	}
	for _, backend := range route.Config.Backends {
		ns := backend.Namespace
		if ns == "" || ns == domain.Namespace {
			continue
		}
		rgName := generateReferenceGrantName(domain.ProjectID, ns)
		exists, _ := s.k8sRefGrants.ReferenceGrantExists(ctx, domain.ProjectID, ns, rgName)
		if !exists {
			log.Printf("Deploy safety net: ReferenceGrant missing in %s for domain %s, skipping (will be created on next namespace sync)", ns, domain.Namespace)
		}
	}
}

// CreateRouteInput represents input for creating a route
type CreateRouteInput struct {
	Name                 string                               `json:"name" binding:"required,min=1,max=63"`
	Description          string                               `json:"description"`
	Protocol             models.RouteProtocol                 `json:"protocol"`
	SecurityMode         models.SecurityMode                  `json:"securityMode"`
	TeamID               uuid.UUID                            `json:"teamId" binding:"required"`
	Config               models.RouteConfig                   `json:"config" binding:"required"`
	SecurityPolicy       *routeplan.SecurityPolicyInput       `json:"securityPolicy,omitempty"`       // Optional security policy (CORS, auth)
	BackendTrafficPolicy *routeplan.BackendTrafficPolicyInput `json:"backendTrafficPolicy,omitempty"` // Optional backend traffic policy (compression)
	ExtensionPolicy      *routeplan.EnvoyExtensionPolicyInput `json:"extensionPolicy,omitempty"`      // Optional extension policy (Lua, Wasm)
	WafPolicy            *routeplan.WafPolicyInput            `json:"wafPolicy,omitempty"`            // Optional WAF policy
	ChangeDescription    string                               `json:"changeDescription,omitempty"`
	AIReview             json.RawMessage                      `json:"aiReview,omitempty"`
	Labels               models.Labels                        `json:"labels,omitempty"`
}

// UpdateRouteInput represents input for updating a route
type UpdateRouteInput struct {
	Description          string                               `json:"description"`
	Config               models.RouteConfig                   `json:"config" binding:"required"`
	SecurityPolicy       *routeplan.SecurityPolicyInput       `json:"securityPolicy,omitempty"`       // Optional security policy (CORS, auth)
	BackendTrafficPolicy *routeplan.BackendTrafficPolicyInput `json:"backendTrafficPolicy,omitempty"` // Optional backend traffic policy (compression)
	ExtensionPolicy      *routeplan.EnvoyExtensionPolicyInput `json:"extensionPolicy,omitempty"`      // Optional extension policy (Lua, Wasm)
	WafPolicy            *routeplan.WafPolicyInput            `json:"wafPolicy,omitempty"`            // Optional WAF policy
	ChangeDescription    string                               `json:"changeDescription,omitempty"`
	AIReview             json.RawMessage                      `json:"aiReview,omitempty"`
	Labels               models.Labels                        `json:"labels,omitempty"`
}

// Create creates a new route (submits for approval)
func (s *RouteService) Create(domainID uuid.UUID, input *CreateRouteInput, createdBy uuid.UUID) (*models.Route, error) {
	// Validate route name - no spaces allowed
	if strings.Contains(input.Name, " ") {
		return nil, errors.New("route name cannot contain spaces")
	}

	// Validate route name format - must be lowercase alphanumeric with dashes
	if !isValidK8sName(input.Name) {
		return nil, errors.New("route name must be lowercase alphanumeric with dashes only (e.g., 'user-api')")
	}

	// Check if route name already exists in domain
	exists, err := s.routeRepo.ExistsByName(domainID, input.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("route name already exists in this domain")
	}

	// Verify domain exists
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	if err := s.validateRouteTrafficPolicies(&input.Config, input.BackendTrafficPolicy, domain.ProjectID); err != nil {
		return nil, err
	}

	if err := validateDirectResponseInput(&input.Config, input.BackendTrafficPolicy); err != nil {
		return nil, err
	}

	// Verify team exists
	_, err = s.teamRepo.GetByID(input.TeamID)
	if err != nil {
		return nil, errors.New("team not found")
	}

	protocol := input.Protocol
	if protocol == "" {
		protocol = models.RouteProtocolHTTP
	}

	// Default security mode
	securityMode := input.SecurityMode
	if securityMode == "" {
		securityMode = models.SecurityModeGeneral
	}

	if err := validateWriteSecurityMode(securityMode, input.SecurityPolicy, true); err != nil {
		return nil, err
	}

	if err := s.validateRouteShapeAndConflicts(&input.Config, input.BackendTrafficPolicy, protocol, domainID, nil); err != nil {
		return nil, err
	}

	// Generate route UUID first so we can use it for K8s resource name
	routeID := s.newID()

	// Generate K8s resource name: {route-name}-{first-8-chars-of-route-uuid}.
	// Safe only because input.Name was already validated by isValidK8sName above
	// (see the comment on that function) - kubernetes.RouteK8sName sanitizes
	// differently than isValidK8sName rejects, so they only agree on validated input.
	k8sRouteName := kubernetes.RouteK8sName(input.Name, routeID.String())

	// Validate labels
	if input.Labels != nil {
		if err := models.ValidateLabels(input.Labels); err != nil {
			return nil, err
		}
	}

	route := &models.Route{
		DomainID:     domainID,
		TeamID:       input.TeamID,
		Name:         input.Name,
		Description:  input.Description,
		Protocol:     protocol,
		SecurityMode: securityMode,
		Config:       input.Config,
		Status:       models.RouteStatusPendingCreate,
		K8sRouteName: k8sRouteName,
		CreatedBy:    createdBy,
		Labels:       input.Labels,
	}
	route.ID = routeID // Set the pre-generated UUID

	if err := s.routeRepo.Create(route); err != nil {
		return nil, err
	}

	// Build config snapshot for unified approval
	var snapshotSP *models.SecurityPolicyConfig
	if input.SecurityPolicy != nil {
		snapshot := models.SecurityPolicyConfig{
			CORS: input.SecurityPolicy.CORS,
		}
		if securityMode == models.SecurityModeGeneral {
			snapshot.Authorization = routeplan.BuildAuthorizationConfigFromInput(input.SecurityPolicy.Authorization)
			snapshot.APIKeyAuth = routeplan.BuildAPIKeyAuthConfigFromInput(input.SecurityPolicy.APIKeyAuth)
			snapshot.JWT = routeplan.BuildJWTConfigFromInput(input.SecurityPolicy.JWT)
			snapshot.OIDC = routeplan.BuildOIDCConfigFromInput(input.SecurityPolicy.OIDC)
		}
		// ExtAuth is allowed in both modes
		snapshot.ExtAuth = input.SecurityPolicy.ExtAuth
		snapshotSP = &snapshot
	}

	var snapshotBTP *models.BackendTrafficPolicyConfig
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() {
		snapshotBTP = &models.BackendTrafficPolicyConfig{
			Compression:      input.BackendTrafficPolicy.Compression,
			Retry:            input.BackendTrafficPolicy.Retry,
			LoadBalancer:     input.BackendTrafficPolicy.LoadBalancer,
			CircuitBreaker:   input.BackendTrafficPolicy.CircuitBreaker,
			HealthCheck:      input.BackendTrafficPolicy.HealthCheck,
			FaultInjection:   input.BackendTrafficPolicy.FaultInjection,
			RateLimit:        input.BackendTrafficPolicy.RateLimit,
			RequestBuffer:    input.BackendTrafficPolicy.RequestBuffer,
			ResponseOverride: input.BackendTrafficPolicy.ResponseOverride,
			Timeout:          input.BackendTrafficPolicy.Timeout,
		}
	}

	var snapshotEEP *models.EnvoyExtensionPolicyConfig
	if input.ExtensionPolicy != nil && input.ExtensionPolicy.HasContent() {
		snapshotEEP = &models.EnvoyExtensionPolicyConfig{
			Lua:     input.ExtensionPolicy.Lua,
			Wasm:    input.ExtensionPolicy.Wasm,
			ExtProc: input.ExtensionPolicy.ExtProc,
		}
	}

	var snapshotWaf *models.WafPolicyConfig
	if input.WafPolicy != nil {
		wafCfg := models.WafPolicyConfig{
			Mode:             input.WafPolicy.Mode,
			Rulesets:         input.WafPolicy.Rulesets,
			AnomalyThreshold: input.WafPolicy.AnomalyThreshold,
			ParanoiaLevel:    input.WafPolicy.ParanoiaLevel,
			DisabledRuleIDs:  input.WafPolicy.DisabledRuleIDs,
			CustomDirectives: input.WafPolicy.CustomDirectives,
		}
		if err := wafCfg.Validate(); err == nil {
			snapshotWaf = &wafCfg
		}
	}

	approval, fastPath, err := s.submitCreateApproval(route, domain, input, createdBy, snapshotSP, snapshotBTP, snapshotEEP, snapshotWaf)
	if err != nil {
		return nil, err
	}
	if fastPath {
		return route, nil
	}

	if err := s.persistCreatePolicies(route, domain.ProjectID, securityMode, input); err != nil {
		return nil, err
	}

	route.PendingApproval = approval
	return route, nil
}

// Update updates a route (submits for approval)
func (s *RouteService) Update(id uuid.UUID, input *UpdateRouteInput, submittedBy uuid.UUID) (*models.Route, error) {
	route, err := s.routeRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Get domain to validate namespaces
	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	if err := s.validateRouteTrafficPolicies(&input.Config, input.BackendTrafficPolicy, domain.ProjectID); err != nil {
		return nil, err
	}

	if err := validateWriteSecurityMode(route.SecurityMode, input.SecurityPolicy, false); err != nil {
		return nil, err
	}

	if err := s.validateRouteShapeAndConflicts(&input.Config, input.BackendTrafficPolicy, route.Protocol, route.DomainID, &id); err != nil {
		return nil, err
	}

	if err := validateDirectResponseInput(&input.Config, input.BackendTrafficPolicy); err != nil {
		return nil, err
	}

	// Check if there's already a pending approval
	existing, err := s.approvalRepo.GetPendingByEntityID(models.ApprovalEntityRoute, id)
	if err == nil && existing != nil {
		return nil, errors.New("there is already a pending approval for this route")
	}

	// Store previous config
	previousConfig := route.Config

	// Capture previous SecurityPolicy config (before update)
	var previousSecurityPolicy *models.SecurityPolicyConfig
	if existingSP, err := s.securityPolicyRepo.GetByRouteID(route.ID); err == nil && existingSP != nil {
		spConfig := existingSP.Config
		previousSecurityPolicy = &spConfig
	}

	// Capture previous BackendTrafficPolicy config (before update)
	var previousBackendTrafficPolicy *models.BackendTrafficPolicyConfig
	if existingBTP, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID); err == nil && existingBTP != nil {
		btpConfig := existingBTP.Config
		previousBackendTrafficPolicy = &btpConfig
	}

	// Capture previous EnvoyExtensionPolicy config (before update)
	var previousEnvoyExtensionPolicy *models.EnvoyExtensionPolicyConfig
	if existingEEP, err := s.envoyExtensionPolicyRepo.GetByRouteID(route.ID); err == nil && existingEEP != nil {
		eepConfig := existingEEP.Config
		previousEnvoyExtensionPolicy = &eepConfig
	}

	// Apply the caller's field changes BEFORE the status transition, so the
	// state machine's write carries them.
	if input.Description != "" {
		route.Description = input.Description
	}
	if input.Labels != nil {
		if err := models.ValidateLabels(input.Labels); err != nil {
			return nil, err
		}
		route.Labels = input.Labels
	}

	// Update route status.
	//
	// routestate.Machine.To owns route.Status and nothing else, and it does
	// NOT write on a no-op transition (see its CONTRACT comment). Description
	// and Labels above are exactly the mutations the pre-2D unconditional
	// routeRepo.Update persisted, so an already-pending_update route — an
	// orphan whose approval submit failed — must still be written explicitly
	// or those edits are silently dropped.
	if route.Status == models.RouteStatusPendingUpdate {
		if err := s.routeRepo.Update(route); err != nil {
			return nil, err
		}
	} else if err := s.state.To(models.SiteRouteUpdate, route, models.RouteStatusPendingUpdate,
		"route update submitted"); err != nil {
		return nil, err
	}

	// Build config snapshot for unified approval
	var updateSnapshotSP *models.SecurityPolicyConfig
	if input.SecurityPolicy != nil {
		snapshot := models.SecurityPolicyConfig{
			CORS: input.SecurityPolicy.CORS,
		}
		if route.SecurityMode == models.SecurityModeGeneral || route.SecurityMode == "" {
			snapshot.Authorization = routeplan.BuildAuthorizationConfigFromInput(input.SecurityPolicy.Authorization)
			snapshot.APIKeyAuth = routeplan.BuildAPIKeyAuthConfigFromInput(input.SecurityPolicy.APIKeyAuth)
			snapshot.JWT = routeplan.BuildJWTConfigFromInput(input.SecurityPolicy.JWT)
			snapshot.OIDC = routeplan.BuildOIDCConfigFromInput(input.SecurityPolicy.OIDC)
		}
		// ExtAuth is allowed in both modes
		snapshot.ExtAuth = input.SecurityPolicy.ExtAuth
		updateSnapshotSP = &snapshot
	}

	var updateSnapshotBTP *models.BackendTrafficPolicyConfig
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() {
		updateSnapshotBTP = &models.BackendTrafficPolicyConfig{
			Compression:      input.BackendTrafficPolicy.Compression,
			Retry:            input.BackendTrafficPolicy.Retry,
			LoadBalancer:     input.BackendTrafficPolicy.LoadBalancer,
			CircuitBreaker:   input.BackendTrafficPolicy.CircuitBreaker,
			HealthCheck:      input.BackendTrafficPolicy.HealthCheck,
			FaultInjection:   input.BackendTrafficPolicy.FaultInjection,
			RateLimit:        input.BackendTrafficPolicy.RateLimit,
			RequestBuffer:    input.BackendTrafficPolicy.RequestBuffer,
			ResponseOverride: input.BackendTrafficPolicy.ResponseOverride,
			Timeout:          input.BackendTrafficPolicy.Timeout,
		}
	}

	var updateSnapshotEEP *models.EnvoyExtensionPolicyConfig
	if input.ExtensionPolicy != nil && input.ExtensionPolicy.HasContent() {
		updateSnapshotEEP = &models.EnvoyExtensionPolicyConfig{
			Lua:     input.ExtensionPolicy.Lua,
			Wasm:    input.ExtensionPolicy.Wasm,
			ExtProc: input.ExtensionPolicy.ExtProc,
		}
	}

	// Build WAF snapshot for proposed config
	var updateSnapshotWaf *models.WafPolicyConfig
	if input.WafPolicy != nil {
		wafCfg := models.WafPolicyConfig{
			Mode:             input.WafPolicy.Mode,
			Rulesets:         input.WafPolicy.Rulesets,
			AnomalyThreshold: input.WafPolicy.AnomalyThreshold,
			ParanoiaLevel:    input.WafPolicy.ParanoiaLevel,
			DisabledRuleIDs:  input.WafPolicy.DisabledRuleIDs,
			CustomDirectives: input.WafPolicy.CustomDirectives,
		}
		if err := wafCfg.Validate(); err == nil {
			updateSnapshotWaf = &wafCfg
		}
	}

	// Capture previous WAF policy for approval diff
	var previousWafPolicy *models.WafPolicyConfig
	if existingWaf, err := s.wafPolicyRepo.GetByRouteID(route.ID); err == nil && existingWaf != nil {
		prevWaf := existingWaf.Config
		previousWafPolicy = &prevWaf
	}

	approval, fastPath, err := s.submitUpdateApproval(route, domain, input, submittedBy, updateApprovalSnapshots{
		ProposedSecurityPolicy:       updateSnapshotSP,
		ProposedBackendTrafficPolicy: updateSnapshotBTP,
		ProposedEnvoyExtensionPolicy: updateSnapshotEEP,
		ProposedWafPolicy:            updateSnapshotWaf,

		PreviousConfig:               previousConfig,
		PreviousSecurityPolicy:       previousSecurityPolicy,
		PreviousBackendTrafficPolicy: previousBackendTrafficPolicy,
		PreviousEnvoyExtensionPolicy: previousEnvoyExtensionPolicy,
		PreviousWafPolicy:            previousWafPolicy,
	})
	if err != nil {
		return nil, err
	}
	if fastPath {
		return route, nil
	}

	if err := s.persistUpdatePolicies(route, domain.ProjectID, input); err != nil {
		return nil, err
	}

	route.PendingApproval = approval
	return route, nil
}

// Delete requests deletion of a route (submits for approval)
func (s *RouteService) Delete(id uuid.UUID, submittedBy uuid.UUID) (*models.Route, error) {
	route, err := s.routeRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Check if there's already a pending approval
	existing, err := s.approvalRepo.GetPendingByEntityID(models.ApprovalEntityRoute, id)
	if err == nil && existing != nil {
		return nil, errors.New("there is already a pending approval for this route")
	}

	// Get domain for project ID
	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, err
	}

	// Update route status. Delete mutates no other route field, so To's
	// no-op path (an already-pending_delete orphan) drops nothing.
	if err := s.state.To(models.SiteRouteDelete, route, models.RouteStatusPendingDelete,
		"route deletion submitted"); err != nil {
		return nil, err
	}

	// Capture current policy configs for the previous config snapshot
	var deletePrevSP *models.SecurityPolicyConfig
	if existingSP, err := s.securityPolicyRepo.GetByRouteID(route.ID); err == nil && existingSP != nil {
		spConfig := existingSP.Config
		deletePrevSP = &spConfig
	}

	var deletePrevBTP *models.BackendTrafficPolicyConfig
	if existingBTP, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID); err == nil && existingBTP != nil {
		btpConfig := existingBTP.Config
		deletePrevBTP = &btpConfig
	}

	var deletePrevEEP *models.EnvoyExtensionPolicyConfig
	if existingEEP, err := s.envoyExtensionPolicyRepo.GetByRouteID(route.ID); err == nil && existingEEP != nil {
		eepConfig := existingEEP.Config
		deletePrevEEP = &eepConfig
	}

	var deletePrevWaf *models.WafPolicyConfig
	if existingWaf, err := s.wafPolicyRepo.GetByRouteID(route.ID); err == nil && existingWaf != nil {
		wafConfig := existingWaf.Config
		deletePrevWaf = &wafConfig
	}

	approval, fastPath, err := s.submitDeleteApproval(route, domain, submittedBy, deletePrevSP, deletePrevBTP, deletePrevEEP, deletePrevWaf)
	if err != nil {
		return nil, err
	}
	if fastPath {
		return route, nil
	}

	route.PendingApproval = approval
	return route, nil
}
