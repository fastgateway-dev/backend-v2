package services

import (
	"errors"
	"fmt"
	"strings"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// ClientAttachmentService is the client_attachment half of the approval
// contract: internal/approval owns stage planning and traversal, and calls
// back here when an approval reaches a terminal state.
//
// createApproval, resolveTeamScope, findStage, validateStageReviewer and
// allStagesApproved used to live in this file -- a second, separately
// written copy of what ApprovalService did for routes. They are gone;
// internal/approval is the single implementation, exercised by
// internal/approval/planning_test.go and traversal_test.go.
var _ approvalpkg.Completer = (*ClientAttachmentService)(nil)

// ClientAttachmentService handles client-route attachment business logic
type ClientAttachmentService struct {
	attachmentRepo     repository.ClientAttachmentRepositoryInterface
	approvalRepo       repository.UnifiedApprovalRepositoryInterface
	clientRepo         repository.ClientRepositoryInterface
	routeRepo          repository.RouteRepositoryInterface
	domainRepo         repository.DomainRepositoryInterface
	projectRepo        repository.ProjectRepositoryInterface
	domainSettingsRepo repository.DomainSettingsRepositoryInterface

	// approvals is the shared approval engine. A required constructor
	// dependency since Phase 2E Task 6: the engine holds this service as a
	// Completer, so main.go builds the engine first and registers the
	// completers afterwards.
	approvals *approvalpkg.Engine

	// state is the sole writer of route.Status. See route_state.go.
	state *routeStateMachine
}

// ClientAttachmentServiceDeps carries everything ClientAttachmentService
// needs. Every field is required.
//
// Phase 2E Task 7 deleted three of them -- PolicyRepo, TeamRepo and
// StageReviewRepo. Each was stored in a field that nothing in this file ever
// read: approval planning moved to internal/approval in Phase 2D, and the
// repositories it used moved with it, but the constructor kept demanding
// them. ProjectRepo and DomainSettingsRepo are live and stay.
type ClientAttachmentServiceDeps struct {
	AttachmentRepo     repository.ClientAttachmentRepositoryInterface
	ApprovalRepo       repository.UnifiedApprovalRepositoryInterface
	ClientRepo         repository.ClientRepositoryInterface
	RouteRepo          repository.RouteRepositoryInterface
	DomainRepo         repository.DomainRepositoryInterface
	ProjectRepo        repository.ProjectRepositoryInterface
	DomainSettingsRepo repository.DomainSettingsRepositoryInterface

	// Approvals owns approval planning and traversal. The engine calls back
	// into this service as a Completer, so main.go builds the engine first
	// and registers the completers afterwards.
	Approvals *approvalpkg.Engine
}

// NewClientAttachmentService builds a fully-wired ClientAttachmentService.
// It panics if a required dependency is missing: before Phase 2E these
// arrived through setters after construction, so a forgotten wiring line
// degraded silently at runtime instead of failing at start-up. Master
// design section 6.6.
func NewClientAttachmentService(deps ClientAttachmentServiceDeps) *ClientAttachmentService {
	var missing []string
	if deps.AttachmentRepo == nil {
		missing = append(missing, "AttachmentRepo")
	}
	if deps.ApprovalRepo == nil {
		missing = append(missing, "ApprovalRepo")
	}
	if deps.ClientRepo == nil {
		missing = append(missing, "ClientRepo")
	}
	if deps.RouteRepo == nil {
		missing = append(missing, "RouteRepo")
	}
	if deps.DomainRepo == nil {
		missing = append(missing, "DomainRepo")
	}
	if deps.ProjectRepo == nil {
		missing = append(missing, "ProjectRepo")
	}
	if deps.DomainSettingsRepo == nil {
		missing = append(missing, "DomainSettingsRepo")
	}
	if deps.Approvals == nil {
		missing = append(missing, "Approvals")
	}
	if len(missing) > 0 {
		panic("services.NewClientAttachmentService: missing required dependency: " + strings.Join(missing, ", "))
	}

	return &ClientAttachmentService{
		attachmentRepo:     deps.AttachmentRepo,
		approvalRepo:       deps.ApprovalRepo,
		clientRepo:         deps.ClientRepo,
		routeRepo:          deps.RouteRepo,
		domainRepo:         deps.DomainRepo,
		projectRepo:        deps.ProjectRepo,
		domainSettingsRepo: deps.DomainSettingsRepo,
		approvals:          deps.Approvals,
		// routeRepo is already a constructor dependency, so the state
		// machine needs no setter of its own.
		state: &routeStateMachine{repo: deps.RouteRepo},
	}
}

// AttachFromRouteInput represents input for attaching a client from the route side
type AttachFromRouteInput struct {
	ClientID          uuid.UUID               `json:"clientId" binding:"required"`
	EnableIPAllowlist bool                    `json:"enableIpAllowlist"`
	EnableAPIKey      bool                    `json:"enableApiKey"`
	EnableJWT         bool                    `json:"enableJwt"`
	EnableBasicAuth   bool                    `json:"enableBasicAuth"`
	EnableMTLS        bool                    `json:"enableMtls"`
	EnableHeaderAuth  bool                    `json:"enableHeaderAuth"`
	RateLimitConfig   *models.RateLimitConfig `json:"rateLimitConfig,omitempty"`
	ExtAuth           *models.ExtAuthConfig   `json:"extAuth,omitempty"`
}

// AttachFromClientInput represents input for attaching a client from the client side
type AttachFromClientInput struct {
	RouteID           uuid.UUID               `json:"routeId" binding:"required"`
	ProjectID         uuid.UUID               `json:"projectId" binding:"required"`
	EnableIPAllowlist bool                    `json:"enableIpAllowlist"`
	EnableAPIKey      bool                    `json:"enableApiKey"`
	EnableJWT         bool                    `json:"enableJwt"`
	EnableBasicAuth   bool                    `json:"enableBasicAuth"`
	EnableMTLS        bool                    `json:"enableMtls"`
	EnableHeaderAuth  bool                    `json:"enableHeaderAuth"`
	RateLimitConfig   *models.RateLimitConfig `json:"rateLimitConfig,omitempty"`
	ExtAuth           *models.ExtAuthConfig   `json:"extAuth,omitempty"`
}

// AttachFromRoute creates a client-route attachment initiated from the route side
// The route team submits; approval stages from policy determine who must approve
func (s *ClientAttachmentService) AttachFromRoute(
	routeID uuid.UUID,
	input *AttachFromRouteInput,
	submittedBy uuid.UUID,
) (*models.ClientRouteAttachment, error) {
	// Validate client exists
	client, err := s.clientRepo.GetByID(input.ClientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	// Validate route exists
	route, err := s.routeRepo.GetByID(routeID)
	if err != nil {
		return nil, errors.New("route not found")
	}

	// Reject attachment for general mode routes
	if route.SecurityMode == models.SecurityModeGeneral || route.SecurityMode == "" {
		return nil, errors.New("client attachments are not available for routes with general security mode; switch to client security mode first")
	}

	// Check if attachment already exists
	existing, err := s.attachmentRepo.GetByClientAndRoute(client.ID, route.ID)
	canReuse := err == nil && (existing.Status == models.AttachmentStatusRemoved || existing.Status == models.AttachmentStatusRejected)
	if err == nil && !canReuse {
		return nil, errors.New("client is already attached or has a pending attachment to this route")
	}

	// Validate: at least one authentication method must be enabled
	if !input.EnableIPAllowlist && !input.EnableAPIKey && !input.EnableJWT && !input.EnableMTLS {
		return nil, errors.New("at least one authentication method must be enabled")
	}

	// Validate: if API key is enabled, client must have an API key configured
	if input.EnableAPIKey && !client.APIKeyEnabled {
		return nil, errors.New("client does not have an API key configured; generate one first")
	}

	// Validate: if JWT is enabled, client must have JWT configured
	if input.EnableJWT && !client.JWTEnabled {
		return nil, errors.New("client does not have JWT configured; configure JWT first")
	}

	// Validate: mTLS needs both a client-side config and an mTLS-enabled
	// domain. AttachFromRoute and AttachFromClient carried byte-identical
	// copies of this check before Phase 2D; validateMTLSPairing is the one
	// implementation.
	if err := s.validateMTLSPairing(input.EnableMTLS, client, route.DomainID); err != nil {
		return nil, err
	}

	// Validate: if IP allowlist is enabled, client must have IP addresses configured
	if input.EnableIPAllowlist && client.IPAddressCount == 0 {
		return nil, errors.New("client has no IP addresses configured; add IP addresses first")
	}

	// Validate: if header auth is enabled, client must have headers configured
	if input.EnableHeaderAuth && client.HeaderCount == 0 {
		return nil, errors.New("client has no headers configured; add headers first")
	}

	// Validate: if rate limit config is provided, validate it
	if input.RateLimitConfig != nil {
		if err := input.RateLimitConfig.Validate(); err != nil {
			return nil, fmt.Errorf("rateLimitConfig: %w", err)
		}
	}

	// Validate: if ext auth config is provided, validate it
	if input.ExtAuth != nil {
		if err := input.ExtAuth.Validate(); err != nil {
			return nil, fmt.Errorf("extAuth: %w", err)
		}
	}

	// Get project ID via the route's domain
	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	var attachment *models.ClientRouteAttachment

	if canReuse {
		// Reuse existing attachment record (was rejected/removed)
		existing.EnableIPAllowlist = input.EnableIPAllowlist
		existing.EnableAPIKey = input.EnableAPIKey
		existing.EnableJWT = input.EnableJWT
		existing.EnableBasicAuth = input.EnableBasicAuth
		existing.EnableMTLS = input.EnableMTLS
		existing.EnableHeaderAuth = input.EnableHeaderAuth
		existing.RateLimitConfig = input.RateLimitConfig
		existing.ExtAuth = input.ExtAuth
		existing.Status = models.AttachmentStatusPendingAttach
		existing.CreatedBy = submittedBy

		if err := s.attachmentRepo.Update(existing); err != nil {
			return nil, err
		}
		attachment = existing
	} else {
		// Create new attachment
		attachment = &models.ClientRouteAttachment{
			ClientID:          client.ID,
			RouteID:           route.ID,
			EnableIPAllowlist: input.EnableIPAllowlist,
			EnableAPIKey:      input.EnableAPIKey,
			EnableJWT:         input.EnableJWT,
			EnableBasicAuth:   input.EnableBasicAuth,
			EnableMTLS:        input.EnableMTLS,
			EnableHeaderAuth:  input.EnableHeaderAuth,
			RateLimitConfig:   input.RateLimitConfig,
			ExtAuth:           input.ExtAuth,
			Status:            models.AttachmentStatusPendingAttach,
			CreatedBy:         submittedBy,
		}

		if err := s.attachmentRepo.Create(attachment); err != nil {
			return nil, err
		}
	}

	// Check if approvals are disabled for this project
	project, err := s.projectRepo.GetByID(domain.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("failed to check project approval settings: %w", err)
	}
	if !project.ApprovalEnabled {
		// Skip approval — set route to pending_deploy, attachment to approved.
		// ORDER MATTERS. The route transition is attempted FIRST, and
		// the attachment is only marked approved once it has succeeded.
		//
		// Until fix round 1 of Task 10+11 this ran the other way round,
		// and a rejected transition left a PERSISTED approved attachment
		// pointing at an untouched route -- a partial write the pre-2D
		// code could not produce, because pre-2D the route write could
		// not be rejected at all. Reordering is preferred to a rollback:
		// a compensating attachmentRepo.Update can itself fail, leaving
		// the same inconsistency with an extra failure mode. Bailing here
		// leaves the attachment at pending_attach with no approval
		// attached, which is exactly the state a failed approvals.Submit
		// already leaves behind on the approvals-enabled path below.
		//
		// To owns route.Status and nothing else, and nothing else on the
		// route has been mutated here, so its no-op path drops nothing.
		if err := s.state.To(SiteAttachFromRoute, route, models.RouteStatusPendingDeploy,
			"client attached from route side, project approvals disabled"); err != nil {
			return nil, err
		}
		attachment.Status = models.AttachmentStatusApproved
		if err := s.attachmentRepo.Update(attachment); err != nil {
			return nil, err
		}
		return s.attachmentRepo.GetByID(attachment.ID)
	}

	// Submit the approval. The engine plans the stages from the project's
	// client_attachment policy and persists the approval itself, so there is
	// no separate approvalRepo.Create here (Engine.Submit writes).
	if _, err := s.approvals.Submit(approvalpkg.Spec{
		ProjectID:   domain.ProjectID,
		EntityType:  models.ApprovalEntityClientAttachment,
		EntityID:    attachment.ID,
		Action:      models.ApprovalActionAttach,
		SubmittedBy: submittedBy,
	}); err != nil {
		return nil, err
	}

	return s.attachmentRepo.GetByID(attachment.ID)
}

// AttachFromClient creates a client-route attachment initiated from the client side
// The client team submits; approval stages from policy determine who must approve
func (s *ClientAttachmentService) AttachFromClient(
	clientID uuid.UUID,
	input *AttachFromClientInput,
	submittedBy uuid.UUID,
) (*models.ClientRouteAttachment, error) {
	// Validate client exists
	client, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	// Validate route exists
	route, err := s.routeRepo.GetByID(input.RouteID)
	if err != nil {
		return nil, errors.New("route not found")
	}

	// Reject attachment for general mode routes
	if route.SecurityMode == models.SecurityModeGeneral || route.SecurityMode == "" {
		return nil, errors.New("client attachments are not available for routes with general security mode; switch to client security mode first")
	}

	// Verify route belongs to the specified project
	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}
	if domain.ProjectID != input.ProjectID {
		return nil, errors.New("route does not belong to the specified project")
	}

	// Check if attachment already exists
	existing, err := s.attachmentRepo.GetByClientAndRoute(client.ID, route.ID)
	canReuse := err == nil && (existing.Status == models.AttachmentStatusRemoved || existing.Status == models.AttachmentStatusRejected)
	if err == nil && !canReuse {
		return nil, errors.New("client is already attached or has a pending attachment to this route")
	}

	// Validate: at least one authentication method must be enabled
	if !input.EnableIPAllowlist && !input.EnableAPIKey && !input.EnableJWT && !input.EnableMTLS {
		return nil, errors.New("at least one authentication method must be enabled")
	}

	// Validate: if API key is enabled, client must have an API key configured
	if input.EnableAPIKey && !client.APIKeyEnabled {
		return nil, errors.New("client does not have an API key configured; generate one first")
	}

	// Validate: if JWT is enabled, client must have JWT configured
	if input.EnableJWT && !client.JWTEnabled {
		return nil, errors.New("client does not have JWT configured; configure JWT first")
	}

	// Validate: mTLS needs both a client-side config and an mTLS-enabled
	// domain. AttachFromRoute and AttachFromClient carried byte-identical
	// copies of this check before Phase 2D; validateMTLSPairing is the one
	// implementation.
	if err := s.validateMTLSPairing(input.EnableMTLS, client, route.DomainID); err != nil {
		return nil, err
	}

	// Validate: if IP allowlist is enabled, client must have IP addresses configured
	if input.EnableIPAllowlist && client.IPAddressCount == 0 {
		return nil, errors.New("client has no IP addresses configured; add IP addresses first")
	}

	// Validate: if header auth is enabled, client must have headers configured
	if input.EnableHeaderAuth && client.HeaderCount == 0 {
		return nil, errors.New("client has no headers configured; add headers first")
	}

	// Validate: if rate limit config is provided, validate it
	if input.RateLimitConfig != nil {
		if err := input.RateLimitConfig.Validate(); err != nil {
			return nil, fmt.Errorf("rateLimitConfig: %w", err)
		}
	}

	// Validate: if ext auth config is provided, validate it
	if input.ExtAuth != nil {
		if err := input.ExtAuth.Validate(); err != nil {
			return nil, fmt.Errorf("extAuth: %w", err)
		}
	}

	var attachment *models.ClientRouteAttachment

	if canReuse {
		// Reuse existing attachment record (was rejected/removed)
		existing.EnableIPAllowlist = input.EnableIPAllowlist
		existing.EnableAPIKey = input.EnableAPIKey
		existing.EnableJWT = input.EnableJWT
		existing.EnableBasicAuth = input.EnableBasicAuth
		existing.EnableMTLS = input.EnableMTLS
		existing.EnableHeaderAuth = input.EnableHeaderAuth
		existing.RateLimitConfig = input.RateLimitConfig
		existing.ExtAuth = input.ExtAuth
		existing.Status = models.AttachmentStatusPendingAttach
		existing.CreatedBy = submittedBy

		if err := s.attachmentRepo.Update(existing); err != nil {
			return nil, err
		}
		attachment = existing
	} else {
		// Create new attachment
		attachment = &models.ClientRouteAttachment{
			ClientID:          client.ID,
			RouteID:           route.ID,
			EnableIPAllowlist: input.EnableIPAllowlist,
			EnableAPIKey:      input.EnableAPIKey,
			EnableJWT:         input.EnableJWT,
			EnableBasicAuth:   input.EnableBasicAuth,
			EnableMTLS:        input.EnableMTLS,
			EnableHeaderAuth:  input.EnableHeaderAuth,
			RateLimitConfig:   input.RateLimitConfig,
			ExtAuth:           input.ExtAuth,
			Status:            models.AttachmentStatusPendingAttach,
			CreatedBy:         submittedBy,
		}

		if err := s.attachmentRepo.Create(attachment); err != nil {
			return nil, err
		}
	}

	// Check if approvals are disabled for this project
	project, err := s.projectRepo.GetByID(domain.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("failed to check project approval settings: %w", err)
	}
	if !project.ApprovalEnabled {
		// Skip approval — set route to pending_deploy, attachment to approved.
		// ORDER MATTERS. The route transition is attempted FIRST, and
		// the attachment is only marked approved once it has succeeded.
		//
		// Until fix round 1 of Task 10+11 this ran the other way round,
		// and a rejected transition left a PERSISTED approved attachment
		// pointing at an untouched route -- a partial write the pre-2D
		// code could not produce, because pre-2D the route write could
		// not be rejected at all. Reordering is preferred to a rollback:
		// a compensating attachmentRepo.Update can itself fail, leaving
		// the same inconsistency with an extra failure mode. Bailing here
		// leaves the attachment at pending_attach with no approval
		// attached, which is exactly the state a failed approvals.Submit
		// already leaves behind on the approvals-enabled path below.
		//
		// To owns route.Status and nothing else, and nothing else on the
		// route has been mutated here, so its no-op path drops nothing.
		if err := s.state.To(SiteAttachFromClient, route, models.RouteStatusPendingDeploy,
			"client attached from client side, project approvals disabled"); err != nil {
			return nil, err
		}
		attachment.Status = models.AttachmentStatusApproved
		if err := s.attachmentRepo.Update(attachment); err != nil {
			return nil, err
		}
		return s.attachmentRepo.GetByID(attachment.ID)
	}

	// Submit the approval. The engine plans the stages from the project's
	// client_attachment policy and persists the approval itself, so there is
	// no separate approvalRepo.Create here (Engine.Submit writes).
	if _, err := s.approvals.Submit(approvalpkg.Spec{
		ProjectID:   domain.ProjectID,
		EntityType:  models.ApprovalEntityClientAttachment,
		EntityID:    attachment.ID,
		Action:      models.ApprovalActionAttach,
		SubmittedBy: submittedBy,
	}); err != nil {
		return nil, err
	}

	return s.attachmentRepo.GetByID(attachment.ID)
}

// RequestDetach creates a detachment request for an existing attachment
func (s *ClientAttachmentService) RequestDetach(attachmentID uuid.UUID, submittedBy uuid.UUID) (*models.ClientRouteAttachment, error) {
	attachment, err := s.attachmentRepo.GetByID(attachmentID)
	if err != nil {
		return nil, errors.New("attachment not found")
	}

	// Only active attachments can be detached
	if attachment.Status != models.AttachmentStatusActive {
		return nil, errors.New("only active attachments can be detached")
	}

	// Get project ID via the route's domain
	route, err := s.routeRepo.GetByID(attachment.RouteID)
	if err != nil {
		return nil, errors.New("route not found")
	}
	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, errors.New("domain not found")
	}

	// Check if approvals are disabled for this project
	project, err := s.projectRepo.GetByID(domain.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("failed to check project approval settings: %w", err)
	}
	if !project.ApprovalEnabled {
		// Skip approval — set route to pending_deploy, attachment to removed.
		// ORDER MATTERS. The route transition is attempted FIRST, and
		// the attachment is only marked removed once it has succeeded.
		//
		// Until fix round 1 of Task 10+11 this ran the other way round,
		// and a rejected transition left a PERSISTED removed attachment
		// pointing at an untouched route -- a partial write the pre-2D
		// code could not produce, because pre-2D the route write could
		// not be rejected at all. Reordering is preferred to a rollback:
		// a compensating attachmentRepo.Update can itself fail, leaving
		// the same inconsistency with an extra failure mode. Bailing here
		// leaves the attachment at pending_attach with no approval
		// attached, which is exactly the state a failed approvals.Submit
		// already leaves behind on the approvals-enabled path below.
		// Here the partial write was worse still: a persisted "removed"
		// attachment against a route that was never queued for redeploy
		// means the client keeps working in Kubernetes while the database
		// says it is detached.
		//
		// To owns route.Status and nothing else, and nothing else on the
		// route has been mutated here, so its no-op path drops nothing.
		if err := s.state.To(SiteRequestDetach, route, models.RouteStatusPendingDeploy,
			"client detach requested, project approvals disabled"); err != nil {
			return nil, err
		}
		attachment.Status = models.AttachmentStatusRemoved
		if err := s.attachmentRepo.Update(attachment); err != nil {
			return nil, err
		}
		return s.attachmentRepo.GetByID(attachment.ID)
	}

	// Update attachment status
	attachment.Status = models.AttachmentStatusPendingDetach
	if err := s.attachmentRepo.Update(attachment); err != nil {
		return nil, err
	}

	// Submit the approval. See the note in AttachFromRoute: Engine.Submit
	// both plans the stages and persists the approval.
	if _, err := s.approvals.Submit(approvalpkg.Spec{
		ProjectID:   domain.ProjectID,
		EntityType:  models.ApprovalEntityClientAttachment,
		EntityID:    attachment.ID,
		Action:      models.ApprovalActionDetach,
		SubmittedBy: submittedBy,
	}); err != nil {
		return nil, err
	}

	return s.attachmentRepo.GetByID(attachment.ID)
}

// ApproveStage approves a specific stage of a client-attachment approval.
//
// The stage machine this used to implement by hand now lives in
// internal/approval, which is also where the pre-2D divergences from the
// route copy were resolved (see traversal.go's DIVERGENCE comments): the
// repository error is no longer masked as "approval not found", the stage
// is validated before self-review is checked, and an empty
// RequiredPermission no longer skips the permission check.
//
// The method is kept so the handler wiring in cmd/server/main.go
// (clientAttachmentHandler.ApproveStage) and
// ClientAttachmentServiceInterface stay unchanged.
func (s *ClientAttachmentService) ApproveStage(approvalID, stageID uuid.UUID, reviewer *models.User) (*models.Approval, error) {
	return s.approvals.ApproveStage(approvalID, stageID, reviewer)
}

// RejectStage rejects a specific stage, which rejects the whole approval.
// Delegates to the engine; see ApproveStage. Note the engine now REQUIRES a
// non-empty comment, which this copy did not.
func (s *ClientAttachmentService) RejectStage(approvalID, stageID uuid.UUID, reviewer *models.User, comment string) (*models.Approval, error) {
	return s.approvals.RejectStage(approvalID, stageID, reviewer, comment)
}

// GetApproval returns an approval by ID
func (s *ClientAttachmentService) GetApproval(id uuid.UUID) (*models.Approval, error) {
	return s.approvalRepo.GetByID(id)
}

// ListApprovalsByProjectID returns paginated client attachment approvals for a project
func (s *ClientAttachmentService) ListApprovalsByProjectID(projectID uuid.UUID, page, limit int, status string) ([]models.Approval, int64, error) {
	return s.approvalRepo.ListByProjectID(projectID, page, limit, status, string(models.ApprovalEntityClientAttachment))
}

// ListByClientID returns all attachments for a client
func (s *ClientAttachmentService) ListByClientID(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	attachments, err := s.attachmentRepo.ListByClientID(clientID)
	if err != nil {
		return nil, err
	}

	// Load pending approvals for each attachment
	for i := range attachments {
		if attachments[i].Status == models.AttachmentStatusPendingAttach ||
			attachments[i].Status == models.AttachmentStatusPendingDetach ||
			attachments[i].Status == models.AttachmentStatusPendingUpdate {
			pending, err := s.approvalRepo.GetPendingByEntityID(models.ApprovalEntityClientAttachment, attachments[i].ID)
			if err == nil {
				attachments[i].PendingApproval = pending
			}
		}
	}

	return attachments, nil
}

// ListByRouteID returns all attachments for a route
func (s *ClientAttachmentService) ListByRouteID(routeID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	attachments, err := s.attachmentRepo.ListByRouteID(routeID)
	if err != nil {
		return nil, err
	}

	// Load pending approvals for each attachment
	for i := range attachments {
		if attachments[i].Status == models.AttachmentStatusPendingAttach ||
			attachments[i].Status == models.AttachmentStatusPendingDetach ||
			attachments[i].Status == models.AttachmentStatusPendingUpdate {
			pending, err := s.approvalRepo.GetPendingByEntityID(models.ApprovalEntityClientAttachment, attachments[i].ID)
			if err == nil {
				attachments[i].PendingApproval = pending
			}
		}
	}

	return attachments, nil
}

// GetAttachment returns an attachment by ID
func (s *ClientAttachmentService) GetAttachment(id uuid.UUID) (*models.ClientRouteAttachment, error) {
	return s.attachmentRepo.GetByID(id)
}

// OnApproved implements approval.Completer. It is the former
// OnApprovalComplete, renamed to satisfy the interface; the body is
// unchanged.
func (s *ClientAttachmentService) OnApproved(approval *models.Approval) error {
	attachment, err := s.attachmentRepo.GetByID(approval.EntityID)
	if err != nil {
		return err
	}

	// For detach approvals, mark as removed; for attach approvals, mark as approved
	if approval.Action == models.ApprovalActionDetach {
		attachment.Status = models.AttachmentStatusRemoved
	} else {
		attachment.Status = models.AttachmentStatusApproved
	}
	if err := s.attachmentRepo.Update(attachment); err != nil {
		return err
	}

	// Move the route to pending_deploy so the route team can deploy
	route, err := s.routeRepo.GetByID(attachment.RouteID)
	if err != nil {
		return err
	}

	if route.Status == models.RouteStatusActive {
		// The assignment of pending_deploy to route.Status that used to sit
		// here was DEAD:
		// updateRouteStatus re-fetches its own copy of the route and writes
		// that one, so this local mutation was never persisted and never
		// read. Deleted in Phase 2D rather than migrated — migrating a write
		// that does nothing would have added a spurious transition.
		return s.updateRouteStatus(route.ID, models.RouteStatusPendingDeploy,
			fmt.Sprintf("attachment approval %s approved (action %s)", approval.ID, approval.Action))
	}

	return nil
}

// OnRejected implements approval.Completer. It is the former
// OnApprovalRejected, renamed to satisfy the interface; the body is
// unchanged.
func (s *ClientAttachmentService) OnRejected(approval *models.Approval) error {
	attachment, err := s.attachmentRepo.GetByID(approval.EntityID)
	if err != nil {
		return err
	}

	attachment.Status = models.AttachmentStatusRejected
	return s.attachmentRepo.Update(attachment)
}

// OnCancelled implements approval.Completer.
//
// The client-attachment approval API exposes no cancel endpoint of its own,
// but the UNIFIED approval API does (POST /approvals/:id/cancel ->
// ApprovalService.CancelApproval), and it accepts any entity type. Pre-2D,
// ApprovalService.onApprovalCancelled dispatched a cancelled
// client_attachment approval to ClientAttachmentService.OnApprovalRejected
// (approval_service.go:568-571 at HEAD) -- i.e. a cancelled attachment
// approval left the attachment marked "rejected", exactly like a rejection.
//
// That is reproduced here rather than invented: OnCancelled is OnRejected.
// TestApprovalService_CancelApproval_ClientAttachment pins it.
func (s *ClientAttachmentService) OnCancelled(approval *models.Approval) error {
	return s.OnRejected(approval)
}

// validateMTLSPairing checks that an mTLS-enabled attachment has both a
// client-side mTLS config and an mTLS-enabled domain.
//
// AttachFromRoute and AttachFromClient carried byte-identical copies of
// this check before Phase 2D. The parameter list is exactly what those
// blocks read: the request's EnableMTLS flag, the client (for MTLSEnabled)
// and the route's domain ID (for the domain settings lookup); the domain
// settings repository comes from the receiver. Every early return, and its
// message, is unchanged -- including treating a lookup error, a missing
// settings row and a disabled mTLS config as the same failure.
func (s *ClientAttachmentService) validateMTLSPairing(enableMTLS bool, client *models.Client, domainID uuid.UUID) error {
	if !enableMTLS {
		return nil
	}

	// Client must have mTLS configured.
	if !client.MTLSEnabled {
		return errors.New("client does not have mTLS configured; configure mTLS first")
	}

	// Domain must have mTLS enabled.
	settings, err := s.domainSettingsRepo.GetByDomainID(domainID)
	if err != nil || settings == nil || settings.Config.MTLS == nil || !settings.Config.MTLS.Enabled {
		return errors.New("domain mTLS must be enabled before attaching mTLS clients; enable mTLS in domain settings first")
	}

	return nil
}

// updateRouteStatus updates only the status of a route.
//
// It re-fetches the route rather than taking its caller's copy, so a TOCTOU
// gap survives between the caller's read of route.Status and this one. What
// changed in Phase 2D is the consequence: the write now goes through
// routeStateMachine.To, which validates the RE-FETCHED status. A route that
// moved to a state with no legal edge to `status` in between now produces an
// error instead of being blindly overwritten. The gap is narrowed, not
// closed: a concurrent move to a state that does have such an edge is still
// accepted silently. Closing it needs the caller's route passed in (or an
// optimistic-concurrency check on the repository), which is out of scope
// here.
func (s *ClientAttachmentService) updateRouteStatus(routeID uuid.UUID, status models.RouteStatus, reason string) error {
	route, err := s.routeRepo.GetByID(routeID)
	if err != nil {
		return err
	}
	return s.state.To(SiteAttachmentApproved, route, status, reason)
}
