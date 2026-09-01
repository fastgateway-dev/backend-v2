package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
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
		exists, _ := s.k8sService.ReferenceGrantExists(ctx, domain.ProjectID, ns, rgName)
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

	// Validate backend required fields (namespace, service, port)
	if err := s.validateBackendRequiredFields(&input.Config); err != nil {
		return nil, err
	}

	// Validate backend namespaces are managed by the project
	if err := s.validateBackendNamespaces(domain.ProjectID, &input.Config); err != nil {
		return nil, err
	}

	// Validate mirror targets (must be different from primary backends)
	if err := s.validateMirrorTargets(&input.Config); err != nil {
		return nil, err
	}

	// Validate failover configuration (must have at least one primary when fallback exists)
	if err := s.validateFailoverConfig(&input.Config); err != nil {
		return nil, err
	}

	// Validate retry configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.Retry != nil {
		if err := input.BackendTrafficPolicy.Retry.Validate(); err != nil {
			return nil, fmt.Errorf("invalid retry configuration: %w", err)
		}
	}

	// Validate load balancer configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.LoadBalancer != nil {
		if err := input.BackendTrafficPolicy.LoadBalancer.Validate(); err != nil {
			return nil, fmt.Errorf("invalid load balancer configuration: %w", err)
		}
	}

	// Validate circuit breaker configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.CircuitBreaker != nil {
		if err := input.BackendTrafficPolicy.CircuitBreaker.Validate(); err != nil {
			return nil, fmt.Errorf("invalid circuit breaker configuration: %w", err)
		}
	}

	// Validate health check configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HealthCheck != nil {
		if err := input.BackendTrafficPolicy.HealthCheck.Validate(); err != nil {
			return nil, fmt.Errorf("invalid health check configuration: %w", err)
		}
	}

	// Validate fault injection configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.FaultInjection != nil {
		if err := input.BackendTrafficPolicy.FaultInjection.Validate(); err != nil {
			return nil, fmt.Errorf("invalid fault injection configuration: %w", err)
		}
	}

	// Validate rate limit configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.RateLimit != nil {
		if err := input.BackendTrafficPolicy.RateLimit.Validate(); err != nil {
			return nil, fmt.Errorf("invalid rate limit configuration: %w", err)
		}
	}

	// Validate timeout configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.Timeout != nil {
		if err := input.BackendTrafficPolicy.Timeout.Validate(); err != nil {
			return nil, fmt.Errorf("invalid timeout configuration: %w", err)
		}
	}

	// Validate direct response configuration if provided
	if input.Config.RouteType == models.RouteTypeDirectResponse {
		if input.Config.DirectResponse == nil {
			return nil, errors.New("directResponse configuration is required for directResponse route type")
		}
		if err := input.Config.DirectResponse.Validate(); err != nil {
			return nil, fmt.Errorf("invalid direct response configuration: %w", err)
		}
		// Direct response routes cannot have backends
		if len(input.Config.Backends) > 0 {
			return nil, errors.New("directResponse routes cannot have backends")
		}
		// Direct response routes cannot have URL rewrite
		if input.Config.URLRewrite != nil {
			return nil, errors.New("directResponse routes cannot have URL rewrite")
		}
		// Direct response routes cannot have request header modifier
		if input.Config.RequestHeaderModifier != nil {
			return nil, errors.New("directResponse routes cannot have request header modifier")
		}
		// Direct response routes cannot have backend traffic policy
		if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() {
			return nil, errors.New("directResponse routes cannot have backend traffic policy")
		}
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

	// Validate security mode specific config
	if securityMode == models.SecurityModeGeneral {
		if err := validateSecurityModeGeneral(input.SecurityPolicy); err != nil {
			return nil, err
		}
	} else if securityMode == models.SecurityModeClient {
		if err := validateSecurityModeClient(input.SecurityPolicy); err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("invalid security mode: %s (must be 'general' or 'client')", securityMode)
	}

	// Validate protocol-specific config
	if protocol == models.RouteProtocolGRPC {
		if err := validateGRPCRouteConfig(&input.Config); err != nil {
			return nil, err
		}
		if err := validateGRPCBackendTrafficPolicy(input.BackendTrafficPolicy); err != nil {
			return nil, err
		}
	} else {
		if err := validateHTTPRouteConfig(&input.Config); err != nil {
			return nil, err
		}
	}

	// Validate essential route configuration
	if err := validateRouteConfig(&input.Config, protocol); err != nil {
		return nil, err
	}

	// Check for matcher conflicts with existing routes in the domain
	if err := s.validateMatcherConflict(domainID, &input.Config, nil); err != nil {
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

	// Check if approvals are disabled for this project
	if s.projectRepo != nil {
		project, err := s.projectRepo.GetByID(domain.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("failed to check project approval settings: %w", err)
		}
		if !project.ApprovalEnabled {
			// Skip approval — set route directly to approved.
			// route was just persisted at pending_create (struct literal
			// above), so this is pending_create -> approved and To always
			// writes; nothing else has been mutated since routeRepo.Create.
			if err := s.state.To(route, models.RouteStatusApproved,
				"route created, project approvals disabled"); err != nil {
				return nil, err
			}
			return route, nil
		}
	}

	configSnapshot, _ := json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig:          &input.Config,
		SecurityPolicy:       snapshotSP,
		BackendTrafficPolicy: snapshotBTP,
		EnvoyExtensionPolicy: snapshotEEP,
		WafPolicy:            snapshotWaf,
	})

	// Submit plans the stages and persists the approval; the service no
	// longer builds either.
	approval, err := s.approvals.Submit(approvalpkg.Spec{
		ProjectID:         domain.ProjectID,
		EntityType:        models.ApprovalEntityRoute,
		EntityID:          route.ID,
		Action:            models.ApprovalActionCreate,
		ConfigSnapshot:    configSnapshot,
		SubmittedBy:       createdBy,
		ChangeDescription: input.ChangeDescription,
		AIReview:          input.AIReview,
	})
	if err != nil {
		return nil, err
	}

	// Create SecurityPolicy if provided
	if input.SecurityPolicy != nil && s.securityPolicyRepo != nil {
		spConfig := models.SecurityPolicyConfig{
			CORS: input.SecurityPolicy.CORS,
		}
		if securityMode == models.SecurityModeGeneral {
			spConfig.Authorization = routeplan.BuildAuthorizationConfigFromInput(input.SecurityPolicy.Authorization)
			spConfig.APIKeyAuth = routeplan.BuildAPIKeyAuthConfigFromInput(input.SecurityPolicy.APIKeyAuth)
			spConfig.JWT = routeplan.BuildJWTConfigFromInput(input.SecurityPolicy.JWT)
			spConfig.OIDC = routeplan.BuildOIDCConfigFromInput(input.SecurityPolicy.OIDC)
		}
		// ExtAuth is allowed in both modes
		spConfig.ExtAuth = input.SecurityPolicy.ExtAuth
		securityPolicy := &models.SecurityPolicy{
			RouteID:   route.ID,
			ProjectID: domain.ProjectID,
			Config:    spConfig,
		}
		if err := s.securityPolicyRepo.Create(securityPolicy); err != nil {
			return nil, fmt.Errorf("failed to create security policy: %w", err)
		}
	}

	// Create BackendTrafficPolicy if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() && s.backendTrafficPolicyRepo != nil {
		backendTrafficPolicy := &models.BackendTrafficPolicy{
			RouteID:   &route.ID,
			ProjectID: domain.ProjectID,
			Config: models.BackendTrafficPolicyConfig{
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
			},
		}
		if err := s.backendTrafficPolicyRepo.Create(backendTrafficPolicy); err != nil {
			return nil, fmt.Errorf("failed to create backend traffic policy: %w", err)
		}
	}

	// Create EnvoyExtensionPolicy if provided
	if input.ExtensionPolicy != nil && input.ExtensionPolicy.HasContent() && s.envoyExtensionPolicyRepo != nil {
		extensionPolicy := &models.EnvoyExtensionPolicy{
			RouteID:   &route.ID,
			ProjectID: domain.ProjectID,
			Config: models.EnvoyExtensionPolicyConfig{
				Lua:     input.ExtensionPolicy.Lua,
				Wasm:    input.ExtensionPolicy.Wasm,
				ExtProc: input.ExtensionPolicy.ExtProc,
			},
		}
		if err := s.envoyExtensionPolicyRepo.Create(extensionPolicy); err != nil {
			return nil, fmt.Errorf("failed to create envoy extension policy: %w", err)
		}
	}

	// Create WAF policy if provided
	if input.WafPolicy != nil && s.wafPolicyRepo != nil {
		wafConfig := models.WafPolicyConfig{
			Mode:             input.WafPolicy.Mode,
			Rulesets:         input.WafPolicy.Rulesets,
			AnomalyThreshold: input.WafPolicy.AnomalyThreshold,
			ParanoiaLevel:    input.WafPolicy.ParanoiaLevel,
			DisabledRuleIDs:  input.WafPolicy.DisabledRuleIDs,
			CustomDirectives: input.WafPolicy.CustomDirectives,
		}
		if err := wafConfig.Validate(); err != nil {
			return nil, fmt.Errorf("invalid WAF policy config: %w", err)
		}

		wafPolicy := &models.WafPolicy{
			RouteID:   route.ID,
			ProjectID: domain.ProjectID,
			Config:    wafConfig,
		}
		if err := s.wafPolicyRepo.Create(wafPolicy); err != nil {
			return nil, fmt.Errorf("failed to create WAF policy: %w", err)
		}
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

	// Validate backend required fields (namespace, service, port)
	if err := s.validateBackendRequiredFields(&input.Config); err != nil {
		return nil, err
	}

	// Validate backend namespaces are managed by the project
	if err := s.validateBackendNamespaces(domain.ProjectID, &input.Config); err != nil {
		return nil, err
	}

	// Validate mirror targets (must be different from primary backends)
	if err := s.validateMirrorTargets(&input.Config); err != nil {
		return nil, err
	}

	// Validate failover configuration (must have at least one primary when fallback exists)
	if err := s.validateFailoverConfig(&input.Config); err != nil {
		return nil, err
	}

	// Validate retry configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.Retry != nil {
		if err := input.BackendTrafficPolicy.Retry.Validate(); err != nil {
			return nil, fmt.Errorf("invalid retry configuration: %w", err)
		}
	}

	// Validate load balancer configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.LoadBalancer != nil {
		if err := input.BackendTrafficPolicy.LoadBalancer.Validate(); err != nil {
			return nil, fmt.Errorf("invalid load balancer configuration: %w", err)
		}
	}

	// Validate circuit breaker configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.CircuitBreaker != nil {
		if err := input.BackendTrafficPolicy.CircuitBreaker.Validate(); err != nil {
			return nil, fmt.Errorf("invalid circuit breaker configuration: %w", err)
		}
	}

	// Validate health check configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HealthCheck != nil {
		if err := input.BackendTrafficPolicy.HealthCheck.Validate(); err != nil {
			return nil, fmt.Errorf("invalid health check configuration: %w", err)
		}
	}

	// Validate fault injection configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.FaultInjection != nil {
		if err := input.BackendTrafficPolicy.FaultInjection.Validate(); err != nil {
			return nil, fmt.Errorf("invalid fault injection configuration: %w", err)
		}
	}

	// Validate rate limit configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.RateLimit != nil {
		if err := input.BackendTrafficPolicy.RateLimit.Validate(); err != nil {
			return nil, fmt.Errorf("invalid rate limit configuration: %w", err)
		}
	}

	// Validate timeout configuration if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.Timeout != nil {
		if err := input.BackendTrafficPolicy.Timeout.Validate(); err != nil {
			return nil, fmt.Errorf("invalid timeout configuration: %w", err)
		}
	}

	// Validate security mode specific config (use route's existing security mode)
	if route.SecurityMode == models.SecurityModeGeneral || route.SecurityMode == "" {
		if err := validateSecurityModeGeneral(input.SecurityPolicy); err != nil {
			return nil, err
		}
	} else if route.SecurityMode == models.SecurityModeClient {
		if err := validateSecurityModeClient(input.SecurityPolicy); err != nil {
			return nil, err
		}
	}

	// Validate protocol-specific config
	if route.Protocol == models.RouteProtocolGRPC {
		if err := validateGRPCRouteConfig(&input.Config); err != nil {
			return nil, err
		}
		if err := validateGRPCBackendTrafficPolicy(input.BackendTrafficPolicy); err != nil {
			return nil, err
		}
	} else {
		if err := validateHTTPRouteConfig(&input.Config); err != nil {
			return nil, err
		}
	}

	// Validate essential route configuration
	if err := validateRouteConfig(&input.Config, route.Protocol); err != nil {
		return nil, err
	}

	// Check for matcher conflicts with existing routes in the domain
	if err := s.validateMatcherConflict(route.DomainID, &input.Config, &id); err != nil {
		return nil, err
	}

	// Validate direct response configuration if provided
	if input.Config.RouteType == models.RouteTypeDirectResponse {
		if input.Config.DirectResponse == nil {
			return nil, errors.New("directResponse configuration is required for directResponse route type")
		}
		if err := input.Config.DirectResponse.Validate(); err != nil {
			return nil, fmt.Errorf("invalid direct response configuration: %w", err)
		}
		// Direct response routes cannot have backends
		if len(input.Config.Backends) > 0 {
			return nil, errors.New("directResponse routes cannot have backends")
		}
		// Direct response routes cannot have URL rewrite
		if input.Config.URLRewrite != nil {
			return nil, errors.New("directResponse routes cannot have URL rewrite")
		}
		// Direct response routes cannot have request header modifier
		if input.Config.RequestHeaderModifier != nil {
			return nil, errors.New("directResponse routes cannot have request header modifier")
		}
		// Direct response routes cannot have backend traffic policy
		if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() {
			return nil, errors.New("directResponse routes cannot have backend traffic policy")
		}
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
	if s.securityPolicyRepo != nil {
		if existingSP, err := s.securityPolicyRepo.GetByRouteID(route.ID); err == nil && existingSP != nil {
			spConfig := existingSP.Config
			previousSecurityPolicy = &spConfig
		}
	}

	// Capture previous BackendTrafficPolicy config (before update)
	var previousBackendTrafficPolicy *models.BackendTrafficPolicyConfig
	if s.backendTrafficPolicyRepo != nil {
		if existingBTP, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID); err == nil && existingBTP != nil {
			btpConfig := existingBTP.Config
			previousBackendTrafficPolicy = &btpConfig
		}
	}

	// Capture previous EnvoyExtensionPolicy config (before update)
	var previousEnvoyExtensionPolicy *models.EnvoyExtensionPolicyConfig
	if s.envoyExtensionPolicyRepo != nil {
		if existingEEP, err := s.envoyExtensionPolicyRepo.GetByRouteID(route.ID); err == nil && existingEEP != nil {
			eepConfig := existingEEP.Config
			previousEnvoyExtensionPolicy = &eepConfig
		}
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
	// routeStateMachine.To owns route.Status and nothing else, and it does
	// NOT write on a no-op transition (see its CONTRACT comment). Description
	// and Labels above are exactly the mutations the pre-2D unconditional
	// routeRepo.Update persisted, so an already-pending_update route — an
	// orphan whose approval submit failed — must still be written explicitly
	// or those edits are silently dropped.
	if route.Status == models.RouteStatusPendingUpdate {
		if err := s.routeRepo.Update(route); err != nil {
			return nil, err
		}
	} else if err := s.state.To(route, models.RouteStatusPendingUpdate,
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
			Lua:  input.ExtensionPolicy.Lua,
			Wasm: input.ExtensionPolicy.Wasm,
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
	if s.wafPolicyRepo != nil {
		if existingWaf, err := s.wafPolicyRepo.GetByRouteID(route.ID); err == nil && existingWaf != nil {
			prevWaf := existingWaf.Config
			previousWafPolicy = &prevWaf
		}
	}

	// Check if approvals are disabled for this project
	if s.projectRepo != nil {
		project, err := s.projectRepo.GetByID(domain.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("failed to check project approval settings: %w", err)
		}
		if !project.ApprovalEnabled {
			// Skip approval — set route directly to pending_deploy.
			// route sits at pending_update, persisted above, and no field
			// other than Status has been touched since.
			if err := s.state.To(route, models.RouteStatusPendingDeploy,
				"route update submitted, project approvals disabled"); err != nil {
				return nil, err
			}
			return route, nil
		}
	}

	configSnapshot, _ := json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig:          &input.Config,
		SecurityPolicy:       updateSnapshotSP,
		BackendTrafficPolicy: updateSnapshotBTP,
		EnvoyExtensionPolicy: updateSnapshotEEP,
		WafPolicy:            updateSnapshotWaf,
	})

	// Build previous config snapshot
	var prevConfigSnapshot json.RawMessage
	prevConfigSnapshot, _ = json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig:          &previousConfig,
		SecurityPolicy:       previousSecurityPolicy,
		BackendTrafficPolicy: previousBackendTrafficPolicy,
		EnvoyExtensionPolicy: previousEnvoyExtensionPolicy,
		WafPolicy:            previousWafPolicy,
	})

	approval, err := s.approvals.Submit(approvalpkg.Spec{
		ProjectID:         domain.ProjectID,
		EntityType:        models.ApprovalEntityRoute,
		EntityID:          route.ID,
		Action:            models.ApprovalActionUpdate,
		ConfigSnapshot:    configSnapshot,
		PreviousConfig:    prevConfigSnapshot,
		SubmittedBy:       submittedBy,
		ChangeDescription: input.ChangeDescription,
		AIReview:          input.AIReview,
	})
	if err != nil {
		return nil, err
	}

	// Update or create SecurityPolicy if provided
	if input.SecurityPolicy != nil && s.securityPolicyRepo != nil {
		spConfig := models.SecurityPolicyConfig{
			CORS: input.SecurityPolicy.CORS,
		}
		if route.SecurityMode == models.SecurityModeGeneral || route.SecurityMode == "" {
			spConfig.Authorization = routeplan.BuildAuthorizationConfigFromInput(input.SecurityPolicy.Authorization)
			spConfig.APIKeyAuth = routeplan.BuildAPIKeyAuthConfigFromInput(input.SecurityPolicy.APIKeyAuth)
			spConfig.JWT = routeplan.BuildJWTConfigFromInput(input.SecurityPolicy.JWT)
			spConfig.OIDC = routeplan.BuildOIDCConfigFromInput(input.SecurityPolicy.OIDC)
		}
		// ExtAuth is allowed in both modes
		spConfig.ExtAuth = input.SecurityPolicy.ExtAuth
		securityPolicy := &models.SecurityPolicy{
			RouteID:   route.ID,
			ProjectID: domain.ProjectID,
			Config:    spConfig,
		}
		if err := s.securityPolicyRepo.Upsert(securityPolicy); err != nil {
			return nil, fmt.Errorf("failed to update security policy: %w", err)
		}
	} else if input.SecurityPolicy == nil && s.securityPolicyRepo != nil {
		// If SecurityPolicy is explicitly nil, delete existing one
		_ = s.securityPolicyRepo.DeleteByRouteID(route.ID)
	}

	// Update or create BackendTrafficPolicy if provided
	if input.BackendTrafficPolicy != nil && input.BackendTrafficPolicy.HasContent() && s.backendTrafficPolicyRepo != nil {
		backendTrafficPolicy := &models.BackendTrafficPolicy{
			RouteID:   &route.ID,
			ProjectID: domain.ProjectID,
			Config: models.BackendTrafficPolicyConfig{
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
			},
		}
		if err := s.backendTrafficPolicyRepo.Upsert(backendTrafficPolicy); err != nil {
			return nil, fmt.Errorf("failed to update backend traffic policy: %w", err)
		}
	} else if (input.BackendTrafficPolicy == nil || !input.BackendTrafficPolicy.HasContent()) && s.backendTrafficPolicyRepo != nil {
		// If BackendTrafficPolicy is explicitly nil or has no content, delete existing one
		_ = s.backendTrafficPolicyRepo.DeleteByRouteID(route.ID)
	}

	// Update or create EnvoyExtensionPolicy if provided
	if input.ExtensionPolicy != nil && input.ExtensionPolicy.HasContent() && s.envoyExtensionPolicyRepo != nil {
		extensionPolicy := &models.EnvoyExtensionPolicy{
			RouteID:   &route.ID,
			ProjectID: domain.ProjectID,
			Config: models.EnvoyExtensionPolicyConfig{
				Lua:     input.ExtensionPolicy.Lua,
				Wasm:    input.ExtensionPolicy.Wasm,
				ExtProc: input.ExtensionPolicy.ExtProc,
			},
		}
		if err := s.envoyExtensionPolicyRepo.Upsert(extensionPolicy); err != nil {
			return nil, fmt.Errorf("failed to update envoy extension policy: %w", err)
		}
	} else if (input.ExtensionPolicy == nil || !input.ExtensionPolicy.HasContent()) && s.envoyExtensionPolicyRepo != nil {
		// If ExtensionPolicy is explicitly nil or has no content, delete existing one
		_ = s.envoyExtensionPolicyRepo.DeleteByRouteID(route.ID)
	}

	// Update WAF policy if provided
	if input.WafPolicy != nil && s.wafPolicyRepo != nil {
		wafConfig := models.WafPolicyConfig{
			Mode:             input.WafPolicy.Mode,
			Rulesets:         input.WafPolicy.Rulesets,
			AnomalyThreshold: input.WafPolicy.AnomalyThreshold,
			ParanoiaLevel:    input.WafPolicy.ParanoiaLevel,
			DisabledRuleIDs:  input.WafPolicy.DisabledRuleIDs,
			CustomDirectives: input.WafPolicy.CustomDirectives,
		}
		if err := wafConfig.Validate(); err != nil {
			return nil, fmt.Errorf("invalid WAF policy config: %w", err)
		}

		wafPolicy := &models.WafPolicy{
			RouteID:   route.ID,
			ProjectID: domain.ProjectID,
			Config:    wafConfig,
		}
		if err := s.wafPolicyRepo.Upsert(wafPolicy); err != nil {
			return nil, fmt.Errorf("failed to update WAF policy: %w", err)
		}
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
	if err := s.state.To(route, models.RouteStatusPendingDelete,
		"route deletion submitted"); err != nil {
		return nil, err
	}

	// Capture current policy configs for the previous config snapshot
	var deletePrevSP *models.SecurityPolicyConfig
	if s.securityPolicyRepo != nil {
		if existingSP, err := s.securityPolicyRepo.GetByRouteID(route.ID); err == nil && existingSP != nil {
			spConfig := existingSP.Config
			deletePrevSP = &spConfig
		}
	}

	var deletePrevBTP *models.BackendTrafficPolicyConfig
	if s.backendTrafficPolicyRepo != nil {
		if existingBTP, err := s.backendTrafficPolicyRepo.GetByRouteID(route.ID); err == nil && existingBTP != nil {
			btpConfig := existingBTP.Config
			deletePrevBTP = &btpConfig
		}
	}

	var deletePrevEEP *models.EnvoyExtensionPolicyConfig
	if s.envoyExtensionPolicyRepo != nil {
		if existingEEP, err := s.envoyExtensionPolicyRepo.GetByRouteID(route.ID); err == nil && existingEEP != nil {
			eepConfig := existingEEP.Config
			deletePrevEEP = &eepConfig
		}
	}

	var deletePrevWaf *models.WafPolicyConfig
	if s.wafPolicyRepo != nil {
		if existingWaf, err := s.wafPolicyRepo.GetByRouteID(route.ID); err == nil && existingWaf != nil {
			wafConfig := existingWaf.Config
			deletePrevWaf = &wafConfig
		}
	}

	// Check if approvals are disabled for this project
	if s.projectRepo != nil {
		project, err := s.projectRepo.GetByID(domain.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("failed to check project approval settings: %w", err)
		}
		if !project.ApprovalEnabled {
			// Skip approval — set route directly to pending_deploy.
			// route sits at pending_delete, persisted above.
			if err := s.state.To(route, models.RouteStatusPendingDeploy,
				"route deletion submitted, project approvals disabled"); err != nil {
				return nil, err
			}
			return route, nil
		}
	}

	// Build config snapshot (current config being deleted)
	configSnapshot, _ := json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig:          &route.Config,
		SecurityPolicy:       deletePrevSP,
		BackendTrafficPolicy: deletePrevBTP,
		EnvoyExtensionPolicy: deletePrevEEP,
		WafPolicy:            deletePrevWaf,
	})

	approval, err := s.approvals.Submit(approvalpkg.Spec{
		ProjectID:      domain.ProjectID,
		EntityType:     models.ApprovalEntityRoute,
		EntityID:       route.ID,
		Action:         models.ApprovalActionDelete,
		ConfigSnapshot: configSnapshot,
		SubmittedBy:    submittedBy,
	})
	if err != nil {
		return nil, err
	}

	route.PendingApproval = approval
	return route, nil
}
