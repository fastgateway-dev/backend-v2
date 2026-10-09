package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/fastgateway-dev/backend-v2/internal/routestate"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type routeWrite struct {
	routeRepo                repository.RouteRepositoryInterface
	domainRepo               repository.DomainRepositoryInterface
	teamRepo                 repository.TeamRepositoryInterface
	projectRepo              repository.ProjectRepositoryInterface
	approvalRepo             repository.UnifiedApprovalRepositoryInterface
	securityPolicyRepo       repository.SecurityPolicyRepositoryInterface
	backendTrafficPolicyRepo repository.BackendTrafficPolicyRepositoryInterface
	envoyExtensionPolicyRepo repository.EnvoyExtensionPolicyRepositoryInterface
	wafPolicyRepo            repository.WafPolicyRepositoryInterface

	k8sRefGrants ReferenceGrantChecker

	// streams resolves the Stream an L4 (tcp/udp) route belongs to; its
	// ProjectID is the project an L4 route's approval is submitted under
	// (a domain route uses its Domain's).
	streams StreamReader

	approvals *approvalpkg.Engine

	state *routestate.Machine

	assembler *routeAssembler
	query     *routeQuery

	// l4Ports guards L4 listener-port collisions (see validateL4Listener).
	// It is wired after construction (SetL4PortChecker) because the
	// StreamService that implements it is built after RouteService.
	l4Ports L4PortChecker
}

// ensureReferenceGrantsForDomain verifies backend namespace ReferenceGrants include
// the domain's namespace. This is a deploy-time safety net.
func (w *routeWrite) ensureReferenceGrantsForDomain(ctx context.Context, route *models.Route, domain *models.Domain) {
	if len(route.Config.Backends) == 0 {
		return
	}
	for _, backend := range route.Config.Backends {
		ns := backend.Namespace
		if ns == "" || ns == domain.Namespace {
			continue
		}
		rgName := generateReferenceGrantName(domain.ProjectID, ns)
		exists, _ := w.k8sRefGrants.ReferenceGrantExists(ctx, domain.ProjectID, ns, rgName)
		if !exists {
			log.Printf("Deploy safety net: ReferenceGrant missing in %s for domain %s, skipping (will be created on next namespace sync)", ns, domain.Namespace)
		}
	}
}

// Create creates a new route (submits for approval).
//
// An HTTP/gRPC route is created under domainID. An L4 (tcp/udp) route is
// created under input.StreamID with domainID == uuid.Nil (see
// CreateForStream). Exactly one owner must be given.
func (w *routeWrite) Create(domainID uuid.UUID, input *CreateRouteInput, createdBy uuid.UUID) (*models.Route, error) {
	return w.create(domainID, input, createdBy, nil)
}

// CreateForStream creates an L4 route under streamID, which must exist and
// belong to projectID (a stream of another project is reported as
// ErrStreamNotFound so IDs cannot be probed across projects). It overrides any
// input.StreamID with streamID.
func (w *routeWrite) CreateForStream(projectID, streamID uuid.UUID, input *CreateRouteInput, createdBy uuid.UUID) (*models.Route, error) {
	in := *input
	in.StreamID = &streamID
	return w.create(uuid.Nil, &in, createdBy, &projectID)
}

// resolveStream loads a Stream, mapping any lookup failure to ErrStreamNotFound
// (like the domain path's "domain not found") and, when wantProject is set,
// requiring the stream to belong to it.
func (w *routeWrite) resolveStream(streamID uuid.UUID, wantProject *uuid.UUID) (*models.Stream, error) {
	stream, err := w.streams.GetByID(streamID)
	if err != nil || stream == nil {
		return nil, ErrStreamNotFound
	}
	if wantProject != nil && stream.ProjectID != *wantProject {
		return nil, ErrStreamNotFound
	}
	return stream, nil
}

// routeProjectID returns the project a persisted route belongs to - its
// Stream's for an L4 route, its Domain's otherwise - together with the
// Domain (nil for an L4 route, which has none). It is the single place that
// branches on the owner before any *route.DomainID dereference in the write
// path.
func (w *routeWrite) routeProjectID(route *models.Route) (uuid.UUID, *models.Domain, error) {
	if err := validateRouteOwner(route.DomainID, route.StreamID); err != nil {
		return uuid.Nil, nil, err
	}
	if route.IsL4() {
		if route.StreamID == nil {
			return uuid.Nil, nil, errors.New("an L4 route must belong to a stream")
		}
		stream, err := w.resolveStream(*route.StreamID, nil)
		if err != nil {
			return uuid.Nil, nil, err
		}
		return stream.ProjectID, nil, nil
	}
	if route.DomainID == nil {
		return uuid.Nil, nil, errors.New("a non-L4 route must belong to a domain")
	}
	domain, err := w.domainRepo.GetByID(*route.DomainID)
	if err != nil {
		return uuid.Nil, nil, errors.New("domain not found")
	}
	return domain.ProjectID, domain, nil
}

// mapL4PersistError turns a unique violation at the L4 route insert into
// ErrPortCollision. The collision pre-check (validateL4Listener) and the insert
// are not atomic, so two concurrent creates of the same (stream, protocol,
// port) both pass the pre-check and one loses at idx_route_stream_proto_port.
// That index is the only unique constraint a fresh L4 insert can violate (the
// route ID is newly minted and the domain-name index ignores NULL domain_id),
// so any unique violation here is that collision; it must be a 409, not a 500.
// It matches gorm.ErrDuplicatedKey (when gorm error translation is on) and
// Postgres SQLSTATE 23505.
func mapL4PersistError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("%w: another route on this stream already uses that protocol and port", ErrPortCollision)
	}
	var sqlErr interface{ SQLState() string }
	if errors.As(err, &sqlErr) && sqlErr.SQLState() == "23505" {
		return fmt.Errorf("%w: another route on this stream already uses that protocol and port", ErrPortCollision)
	}
	return err
}

func (w *routeWrite) create(domainID uuid.UUID, input *CreateRouteInput, createdBy uuid.UUID, wantProject *uuid.UUID) (*models.Route, error) {
	var domainPtr *uuid.UUID
	if domainID != uuid.Nil {
		domainPtr = &domainID
	}
	if err := validateRouteOwner(domainPtr, input.StreamID); err != nil {
		return nil, err
	}
	isStream := input.StreamID != nil

	// Validate route name - no spaces allowed
	if strings.Contains(input.Name, " ") {
		return nil, errors.New("route name cannot contain spaces")
	}

	// Validate route name format - must be lowercase alphanumeric with dashes
	if !isValidK8sName(input.Name) {
		return nil, errors.New("route name must be lowercase alphanumeric with dashes only (e.g., 'user-api')")
	}

	// Resolve the owner (domain or stream) and its project, and check the
	// route name is free within it.
	var projectID uuid.UUID
	if isStream {
		if input.Protocol != models.RouteProtocolTCP && input.Protocol != models.RouteProtocolUDP {
			return nil, ErrRouteProtocolNotL4
		}
		stream, err := w.resolveStream(*input.StreamID, wantProject)
		if err != nil {
			return nil, err
		}
		projectID = stream.ProjectID

		exists, err := w.routeRepo.ExistsByStreamAndName(stream.ID, input.Name)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, errors.New("route name already exists in this stream")
		}
	} else {
		// Check if route name already exists in domain
		exists, err := w.routeRepo.ExistsByName(domainID, input.Name)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, errors.New("route name already exists in this domain")
		}

		// Verify domain exists
		domain, err := w.domainRepo.GetByID(domainID)
		if err != nil {
			return nil, errors.New("domain not found")
		}
		projectID = domain.ProjectID
	}

	if err := w.validateRouteTrafficPolicies(&input.Config, input.BackendTrafficPolicy, projectID); err != nil {
		return nil, err
	}

	if err := validateDirectResponseInput(&input.Config, input.BackendTrafficPolicy); err != nil {
		return nil, err
	}

	// Verify team exists
	if _, err := w.teamRepo.GetByID(input.TeamID); err != nil {
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

	if protocol == models.RouteProtocolTCP || protocol == models.RouteProtocolUDP {
		if err := validateL4PolicyInputs(protocol, securityMode, input.SecurityPolicy, input.ExtensionPolicy, input.WafPolicy, input.BackendTrafficPolicy); err != nil {
			return nil, err
		}
	}

	if err := w.validateRouteShapeAndConflicts(&input.Config, input.BackendTrafficPolicy, protocol, domainID, input.StreamID, nil); err != nil {
		return nil, err
	}

	// Generate route UUID first so we can use it for K8s resource name
	routeID := w.assembler.newID()

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
		DomainID:     domainPtr,
		StreamID:     input.StreamID,
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

	if err := w.routeRepo.Create(route); err != nil {
		if isStream {
			err = mapL4PersistError(err)
		}
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

	approval, fastPath, err := w.submitCreateApproval(route, projectID, input, createdBy, snapshotSP, snapshotBTP, snapshotEEP, snapshotWaf)
	if err != nil {
		return nil, err
	}
	if fastPath {
		return route, nil
	}

	if err := w.persistCreatePolicies(route, projectID, securityMode, input); err != nil {
		return nil, err
	}

	route.PendingApproval = approval
	return route, nil
}

// Update updates a route (submits for approval)
func (w *routeWrite) Update(id uuid.UUID, input *UpdateRouteInput, submittedBy uuid.UUID) (*models.Route, error) {
	route, err := w.routeRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Resolve the owning project to validate namespaces. An L4 route has a
	// Stream and no Domain, so this must precede every *route.DomainID use.
	projectID, _, err := w.routeProjectID(route)
	if err != nil {
		return nil, err
	}
	var domainID uuid.UUID // zero for an L4 route, which has no matcher domain
	if route.DomainID != nil {
		domainID = *route.DomainID
	}

	if err := w.validateRouteTrafficPolicies(&input.Config, input.BackendTrafficPolicy, projectID); err != nil {
		return nil, err
	}

	if err := validateWriteSecurityMode(route.SecurityMode, input.SecurityPolicy, false); err != nil {
		return nil, err
	}

	if route.IsL4() {
		if err := validateL4PolicyInputs(route.Protocol, route.SecurityMode, input.SecurityPolicy, input.ExtensionPolicy, input.WafPolicy, input.BackendTrafficPolicy); err != nil {
			return nil, err
		}
	}

	if err := w.validateRouteShapeAndConflicts(&input.Config, input.BackendTrafficPolicy, route.Protocol, domainID, route.StreamID, &id); err != nil {
		return nil, err
	}

	if err := validateDirectResponseInput(&input.Config, input.BackendTrafficPolicy); err != nil {
		return nil, err
	}

	// Check if there's already a pending approval
	existing, err := w.approvalRepo.GetPendingByEntityID(models.ApprovalEntityRoute, id)
	if err == nil && existing != nil {
		return nil, errors.New("there is already a pending approval for this route")
	}

	// Store previous config
	previousConfig := route.Config

	// Capture previous SecurityPolicy config (before update)
	var previousSecurityPolicy *models.SecurityPolicyConfig
	if existingSP, err := w.securityPolicyRepo.GetByRouteID(route.ID); err == nil && existingSP != nil {
		spConfig := existingSP.Config
		previousSecurityPolicy = &spConfig
	}

	// Capture previous BackendTrafficPolicy config (before update)
	var previousBackendTrafficPolicy *models.BackendTrafficPolicyConfig
	if existingBTP, err := w.backendTrafficPolicyRepo.GetByRouteID(route.ID); err == nil && existingBTP != nil {
		btpConfig := existingBTP.Config
		previousBackendTrafficPolicy = &btpConfig
	}

	// Capture previous EnvoyExtensionPolicy config (before update)
	var previousEnvoyExtensionPolicy *models.EnvoyExtensionPolicyConfig
	if existingEEP, err := w.envoyExtensionPolicyRepo.GetByRouteID(route.ID); err == nil && existingEEP != nil {
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
		if err := w.routeRepo.Update(route); err != nil {
			return nil, err
		}
	} else if err := w.state.To(models.SiteRouteUpdate, route, models.RouteStatusPendingUpdate,
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
	if existingWaf, err := w.wafPolicyRepo.GetByRouteID(route.ID); err == nil && existingWaf != nil {
		prevWaf := existingWaf.Config
		previousWafPolicy = &prevWaf
	}

	approval, fastPath, err := w.submitUpdateApproval(route, projectID, input, submittedBy, updateApprovalSnapshots{
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

	if err := w.persistUpdatePolicies(route, projectID, input); err != nil {
		return nil, err
	}

	route.PendingApproval = approval
	return route, nil
}

// Delete requests deletion of a route (submits for approval)
func (w *routeWrite) Delete(id uuid.UUID, submittedBy uuid.UUID) (*models.Route, error) {
	route, err := w.routeRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Check if there's already a pending approval
	existing, err := w.approvalRepo.GetPendingByEntityID(models.ApprovalEntityRoute, id)
	if err == nil && existing != nil {
		return nil, errors.New("there is already a pending approval for this route")
	}

	// Resolve the owning project (the Stream's for an L4 route, which has no
	// Domain).
	projectID, _, err := w.routeProjectID(route)
	if err != nil {
		return nil, err
	}

	// Update route status. Delete mutates no other route field, so To's
	// no-op path (an already-pending_delete orphan) drops nothing.
	if err := w.state.To(models.SiteRouteDelete, route, models.RouteStatusPendingDelete,
		"route deletion submitted"); err != nil {
		return nil, err
	}

	// Capture current policy configs for the previous config snapshot
	var deletePrevSP *models.SecurityPolicyConfig
	if existingSP, err := w.securityPolicyRepo.GetByRouteID(route.ID); err == nil && existingSP != nil {
		spConfig := existingSP.Config
		deletePrevSP = &spConfig
	}

	var deletePrevBTP *models.BackendTrafficPolicyConfig
	if existingBTP, err := w.backendTrafficPolicyRepo.GetByRouteID(route.ID); err == nil && existingBTP != nil {
		btpConfig := existingBTP.Config
		deletePrevBTP = &btpConfig
	}

	var deletePrevEEP *models.EnvoyExtensionPolicyConfig
	if existingEEP, err := w.envoyExtensionPolicyRepo.GetByRouteID(route.ID); err == nil && existingEEP != nil {
		eepConfig := existingEEP.Config
		deletePrevEEP = &eepConfig
	}

	var deletePrevWaf *models.WafPolicyConfig
	if existingWaf, err := w.wafPolicyRepo.GetByRouteID(route.ID); err == nil && existingWaf != nil {
		wafConfig := existingWaf.Config
		deletePrevWaf = &wafConfig
	}

	approval, fastPath, err := w.submitDeleteApproval(route, projectID, submittedBy, deletePrevSP, deletePrevBTP, deletePrevEEP, deletePrevWaf)
	if err != nil {
		return nil, err
	}
	if fastPath {
		return route, nil
	}

	route.PendingApproval = approval
	return route, nil
}
