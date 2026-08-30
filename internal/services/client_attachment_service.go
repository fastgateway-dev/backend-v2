package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// ClientAttachmentService handles client-route attachment business logic
type ClientAttachmentService struct {
	attachmentRepo     repository.ClientAttachmentRepositoryInterface
	approvalRepo       repository.UnifiedApprovalRepositoryInterface
	policyRepo         repository.ApprovalPolicyRepositoryInterface
	clientRepo         repository.ClientRepositoryInterface
	routeRepo          repository.RouteRepositoryInterface
	domainRepo         repository.DomainRepositoryInterface
	teamRepo           repository.TeamRepositoryInterface
	projectRepo        repository.ProjectRepositoryInterface
	domainSettingsRepo repository.DomainSettingsRepositoryInterface
	stageReviewRepo    repository.ApprovalStageReviewRepositoryInterface
}

// NewClientAttachmentService creates a new ClientAttachmentService
func NewClientAttachmentService(
	attachmentRepo repository.ClientAttachmentRepositoryInterface,
	approvalRepo repository.UnifiedApprovalRepositoryInterface,
	policyRepo repository.ApprovalPolicyRepositoryInterface,
	clientRepo repository.ClientRepositoryInterface,
	routeRepo repository.RouteRepositoryInterface,
	domainRepo repository.DomainRepositoryInterface,
	teamRepo repository.TeamRepositoryInterface,
	projectRepo repository.ProjectRepositoryInterface,
) *ClientAttachmentService {
	return &ClientAttachmentService{
		attachmentRepo: attachmentRepo,
		approvalRepo:   approvalRepo,
		policyRepo:     policyRepo,
		clientRepo:     clientRepo,
		routeRepo:      routeRepo,
		domainRepo:     domainRepo,
		teamRepo:       teamRepo,
		projectRepo:    projectRepo,
	}
}

func (s *ClientAttachmentService) SetDomainSettingsRepository(repo repository.DomainSettingsRepositoryInterface) {
	s.domainSettingsRepo = repo
}

// SetStageReviewRepository sets the stage review repository for multi-approver support
func (s *ClientAttachmentService) SetStageReviewRepository(repo repository.ApprovalStageReviewRepositoryInterface) {
	s.stageReviewRepo = repo
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

	// Validate: if mTLS is enabled, client must have mTLS configured
	if input.EnableMTLS && !client.MTLSEnabled {
		return nil, errors.New("client does not have mTLS configured; configure mTLS first")
	}

	// Validate: if mTLS is enabled, domain must have mTLS enabled
	if input.EnableMTLS {
		if s.domainSettingsRepo == nil {
			return nil, errors.New("domain settings not available; cannot validate mTLS")
		}
		settings, err := s.domainSettingsRepo.GetByDomainID(route.DomainID)
		if err != nil || settings == nil || settings.Config.MTLS == nil || !settings.Config.MTLS.Enabled {
			return nil, errors.New("domain mTLS must be enabled before attaching mTLS clients; enable mTLS in domain settings first")
		}
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
	if s.projectRepo != nil {
		project, err := s.projectRepo.GetByID(domain.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("failed to check project approval settings: %w", err)
		}
		if !project.ApprovalEnabled {
			// Skip approval — set attachment to approved, route to pending_deploy
			attachment.Status = models.AttachmentStatusApproved
			if err := s.attachmentRepo.Update(attachment); err != nil {
				return nil, err
			}
			route.Status = models.RouteStatusPendingDeploy
			if err := s.routeRepo.Update(route); err != nil {
				return nil, err
			}
			return s.attachmentRepo.GetByID(attachment.ID)
		}
	}

	// Create the unified approval with stages from policy
	if err := s.createApproval(domain.ProjectID, attachment.ID, models.ApprovalActionAttach, submittedBy); err != nil {
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

	// Validate: if mTLS is enabled, client must have mTLS configured
	if input.EnableMTLS && !client.MTLSEnabled {
		return nil, errors.New("client does not have mTLS configured; configure mTLS first")
	}

	// Validate: if mTLS is enabled, domain must have mTLS enabled
	if input.EnableMTLS {
		if s.domainSettingsRepo == nil {
			return nil, errors.New("domain settings not available; cannot validate mTLS")
		}
		settings, err := s.domainSettingsRepo.GetByDomainID(route.DomainID)
		if err != nil || settings == nil || settings.Config.MTLS == nil || !settings.Config.MTLS.Enabled {
			return nil, errors.New("domain mTLS must be enabled before attaching mTLS clients; enable mTLS in domain settings first")
		}
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
	if s.projectRepo != nil {
		project, err := s.projectRepo.GetByID(domain.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("failed to check project approval settings: %w", err)
		}
		if !project.ApprovalEnabled {
			// Skip approval — set attachment to approved, route to pending_deploy
			attachment.Status = models.AttachmentStatusApproved
			if err := s.attachmentRepo.Update(attachment); err != nil {
				return nil, err
			}
			route.Status = models.RouteStatusPendingDeploy
			if err := s.routeRepo.Update(route); err != nil {
				return nil, err
			}
			return s.attachmentRepo.GetByID(attachment.ID)
		}
	}

	// Create the unified approval with stages from policy
	if err := s.createApproval(domain.ProjectID, attachment.ID, models.ApprovalActionAttach, submittedBy); err != nil {
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
	if s.projectRepo != nil {
		project, err := s.projectRepo.GetByID(domain.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("failed to check project approval settings: %w", err)
		}
		if !project.ApprovalEnabled {
			// Skip approval — set attachment to removed, route to pending_deploy
			attachment.Status = models.AttachmentStatusRemoved
			if err := s.attachmentRepo.Update(attachment); err != nil {
				return nil, err
			}
			route.Status = models.RouteStatusPendingDeploy
			if err := s.routeRepo.Update(route); err != nil {
				return nil, err
			}
			return s.attachmentRepo.GetByID(attachment.ID)
		}
	}

	// Update attachment status
	attachment.Status = models.AttachmentStatusPendingDetach
	if err := s.attachmentRepo.Update(attachment); err != nil {
		return nil, err
	}

	// Create the unified approval with stages from policy
	if err := s.createApproval(domain.ProjectID, attachment.ID, models.ApprovalActionDetach, submittedBy); err != nil {
		return nil, err
	}

	return s.attachmentRepo.GetByID(attachment.ID)
}

// ApproveStage approves a specific stage in an approval
func (s *ClientAttachmentService) ApproveStage(approvalID, stageID uuid.UUID, reviewer *models.User) (*models.Approval, error) {
	approval, err := s.approvalRepo.GetByID(approvalID)
	if err != nil {
		return nil, errors.New("approval not found")
	}

	if approval.Status != models.ApprovalStatusPending {
		return nil, errors.New("approval is not pending")
	}

	// Submitter cannot approve their own submission (unless project allows self-approval)
	if approval.SubmittedBy == reviewer.ID {
		allowed := false
		if s.projectRepo != nil {
			project, err := s.projectRepo.GetByID(approval.ProjectID)
			if err == nil && project.SelfApprovalAllowed {
				allowed = true
			}
		}
		if !allowed {
			return nil, errors.New("submitter cannot approve their own submission")
		}
	}

	// Find the target stage
	stage, err := s.findStage(approval, stageID)
	if err != nil {
		return nil, err
	}

	// Validate the reviewer can approve this stage
	if err := s.validateStageReviewer(approval, stage, reviewer); err != nil {
		return nil, err
	}

	// Record the review for multi-approver tracking
	if s.stageReviewRepo != nil {
		// Check if this reviewer has already reviewed this stage
		existingReviews, err := s.stageReviewRepo.ListByStageID(stage.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing reviews: %w", err)
		}
		for _, r := range existingReviews {
			if r.ReviewerID == reviewer.ID {
				return nil, errors.New("you have already reviewed this stage")
			}
		}

		review := &models.ApprovalStageReview{
			StageID:    stage.ID,
			ReviewerID: reviewer.ID,
			Decision:   "approved",
		}
		if err := s.stageReviewRepo.Create(review); err != nil {
			return nil, fmt.Errorf("failed to record review: %w", err)
		}

		// Count approvals for this stage
		count, err := s.stageReviewRepo.CountByStageAndDecision(stage.ID, "approved")
		if err != nil {
			return nil, fmt.Errorf("failed to count reviews: %w", err)
		}

		minRequired := models.EffectiveMinApprovers(stage.MinApprovers)
		if count < int64(minRequired) {
			// Not enough approvals yet — return current state without completing stage
			return s.approvalRepo.GetByID(approvalID)
		}
	}

	// Approve the stage (enough approvals received)
	now := time.Now()
	stage.ReviewedBy = &reviewer.ID
	stage.Status = models.ApprovalStatusApproved
	stage.ReviewedAt = &now

	if err := s.approvalRepo.UpdateStage(stage); err != nil {
		return nil, err
	}

	// Check if ALL stages are now approved
	if s.allStagesApproved(approval, stageID) {
		approval.Status = models.ApprovalStatusApproved
		if err := s.approvalRepo.Update(approval); err != nil {
			return nil, err
		}
		// Handle attachment status change on full approval
		if err := s.OnApprovalComplete(approval); err != nil {
			return nil, err
		}
	}

	return s.approvalRepo.GetByID(approvalID)
}

// RejectStage rejects a specific stage in an approval
func (s *ClientAttachmentService) RejectStage(approvalID, stageID uuid.UUID, reviewer *models.User, comment string) (*models.Approval, error) {
	approval, err := s.approvalRepo.GetByID(approvalID)
	if err != nil {
		return nil, errors.New("approval not found")
	}

	if approval.Status != models.ApprovalStatusPending {
		return nil, errors.New("approval is not pending")
	}

	// Submitter cannot reject their own submission (unless project allows self-approval)
	if approval.SubmittedBy == reviewer.ID {
		allowed := false
		if s.projectRepo != nil {
			project, err := s.projectRepo.GetByID(approval.ProjectID)
			if err == nil && project.SelfApprovalAllowed {
				allowed = true
			}
		}
		if !allowed {
			return nil, errors.New("submitter cannot reject their own submission")
		}
	}

	// Find the target stage
	stage, err := s.findStage(approval, stageID)
	if err != nil {
		return nil, err
	}

	// Validate the reviewer can act on this stage
	if err := s.validateStageReviewer(approval, stage, reviewer); err != nil {
		return nil, err
	}

	// Record the rejection review for audit trail
	if s.stageReviewRepo != nil {
		review := &models.ApprovalStageReview{
			StageID:    stage.ID,
			ReviewerID: reviewer.ID,
			Decision:   "rejected",
		}
		s.stageReviewRepo.Create(review) // Best-effort for audit trail
	}

	// Reject the stage
	now := time.Now()
	stage.ReviewedBy = &reviewer.ID
	stage.Status = models.ApprovalStatusRejected
	stage.Comment = comment
	stage.ReviewedAt = &now

	if err := s.approvalRepo.UpdateStage(stage); err != nil {
		return nil, err
	}

	// Any rejection rejects the whole approval
	approval.Status = models.ApprovalStatusRejected
	if err := s.approvalRepo.Update(approval); err != nil {
		return nil, err
	}

	// Update attachment status to rejected
	attachment, err := s.attachmentRepo.GetByID(approval.EntityID)
	if err == nil {
		attachment.Status = models.AttachmentStatusRejected
		s.attachmentRepo.Update(attachment)
	}

	return s.approvalRepo.GetByID(approvalID)
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

// createApproval looks up the approval policy and creates a unified Approval with stages
func (s *ClientAttachmentService) createApproval(projectID uuid.UUID, entityID uuid.UUID, action models.ApprovalAction, submittedBy uuid.UUID) error {
	// Two-step policy lookup: try action-specific first, then fall back to default
	actionStr := string(action)
	policy, err := s.policyRepo.GetByProjectAndEntity(projectID, string(models.ApprovalEntityClientAttachment), &actionStr)
	if err != nil || policy == nil {
		policy, err = s.policyRepo.GetByProjectAndEntity(projectID, string(models.ApprovalEntityClientAttachment), nil)
	}
	if err != nil {
		return fmt.Errorf("no approval policy found for client_attachment: %w", err)
	}

	// Parse the policy stages JSON
	var templates []models.PolicyStageTemplate
	if err := json.Unmarshal(policy.Stages, &templates); err != nil {
		return fmt.Errorf("failed to parse approval policy stages: %w", err)
	}

	if len(templates) == 0 {
		return errors.New("approval policy has no stages defined")
	}

	// Build approval stages by resolving team_scope for each template
	stages := make([]models.ApprovalStage, 0, len(templates))
	for _, tmpl := range templates {
		resolvedTeamID, err := s.resolveTeamScope(tmpl.TeamScope, projectID, submittedBy)
		if err != nil {
			return fmt.Errorf("failed to resolve team scope %q for stage %d: %w", tmpl.TeamScope, tmpl.Order, err)
		}

		stages = append(stages, models.ApprovalStage{
			StageOrder:         tmpl.Order,
			RequiredPermission: tmpl.RequiredPermission,
			RequiredTeamID:     resolvedTeamID,
			MinApprovers:       models.EffectiveMinApprovers(tmpl.MinApprovers),
			Status:             models.ApprovalStatusPending,
		})
	}

	// Create the unified approval (GORM associations will create stages)
	approval := &models.Approval{
		ProjectID:   projectID,
		EntityType:  models.ApprovalEntityClientAttachment,
		EntityID:    entityID,
		Action:      action,
		SubmittedBy: submittedBy,
		Status:      models.ApprovalStatusPending,
		Stages:      stages,
	}

	return s.approvalRepo.Create(approval)
}

// resolveTeamScope resolves a team_scope string to a *uuid.UUID for the required team
func (s *ClientAttachmentService) resolveTeamScope(scope string, projectID uuid.UUID, submittedBy uuid.UUID) (*uuid.UUID, error) {
	switch scope {
	case "any":
		// No specific team required
		return nil, nil

	case "submitter_team":
		// Find one of submitter's teams in the project
		ptrs, err := s.teamRepo.GetUserTeamsInProject(projectID, submittedBy)
		if err != nil || len(ptrs) == 0 {
			return nil, errors.New("submitter is not a member of any team in this project")
		}
		teamID := ptrs[0].TeamID
		return &teamID, nil

	case "other_team":
		// Find a team that is different from submitter's team(s) in the project
		submitterPtrs, err := s.teamRepo.GetUserTeamsInProject(projectID, submittedBy)
		if err != nil {
			return nil, errors.New("failed to look up submitter teams")
		}

		// Build set of submitter team IDs
		submitterTeams := make(map[uuid.UUID]bool)
		for _, ptr := range submitterPtrs {
			submitterTeams[ptr.TeamID] = true
		}

		// Get all teams in the project
		allPtrs, err := s.teamRepo.ListProjectTeams(projectID)
		if err != nil {
			return nil, errors.New("failed to list project teams")
		}

		// Find one that is not a submitter team
		for _, ptr := range allPtrs {
			if !submitterTeams[ptr.TeamID] {
				teamID := ptr.TeamID
				return &teamID, nil
			}
		}

		// No other team found; leave as nil (anyone with the permission can approve)
		return nil, nil

	default:
		return nil, fmt.Errorf("unknown team_scope: %s", scope)
	}
}

// findStage locates a stage by ID within an approval's stages
func (s *ClientAttachmentService) findStage(approval *models.Approval, stageID uuid.UUID) (*models.ApprovalStage, error) {
	for i := range approval.Stages {
		if approval.Stages[i].ID == stageID {
			return &approval.Stages[i], nil
		}
	}
	return nil, errors.New("stage not found in this approval")
}

// validateStageReviewer checks that the reviewer is eligible to act on a stage:
// - Stage must be pending
// - All previous stages (lower order) must be approved
// - If stage has required_permission, reviewer must have it in the project
// - If stage has required_team_id, reviewer must be a member of that team
func (s *ClientAttachmentService) validateStageReviewer(approval *models.Approval, stage *models.ApprovalStage, reviewer *models.User) error {
	// Stage must be pending
	if stage.Status != models.ApprovalStatusPending {
		return errors.New("this stage has already been reviewed")
	}

	// All previous stages must be approved
	for _, st := range approval.Stages {
		if st.StageOrder < stage.StageOrder && st.Status != models.ApprovalStatusApproved {
			return errors.New("previous stages must be approved before this stage can be reviewed")
		}
	}

	// Check if reviewer is owner or project admin (bypass permission/team checks)
	isOwner := reviewer.Role == models.UserRoleOwner
	isProjectAdmin := false
	if !isOwner {
		isProjectAdmin, _ = s.projectRepo.IsAdmin(approval.ProjectID, reviewer.ID)
	}

	// Owner and project admin can approve any stage
	if isOwner || isProjectAdmin {
		return nil
	}

	// Check required_permission
	if stage.RequiredPermission != "" {
		hasPerm, err := s.teamRepo.HasPermissionInProject(approval.ProjectID, reviewer.ID, models.Permission(stage.RequiredPermission))
		if err != nil {
			return errors.New("failed to check reviewer permission")
		}
		if !hasPerm {
			return fmt.Errorf("reviewer does not have the required permission %q in this project", stage.RequiredPermission)
		}
	}

	// Check required_team_id
	if stage.RequiredTeamID != nil {
		isMember, err := s.teamRepo.IsMember(*stage.RequiredTeamID, reviewer.ID)
		if err != nil {
			return errors.New("failed to check team membership")
		}
		if !isMember {
			return errors.New("reviewer must be a member of the required team for this stage")
		}
	}

	return nil
}

// allStagesApproved checks if all stages in the approval are approved,
// treating the given stageID as already approved (since it was just updated in memory
// but the approval.Stages slice may not yet reflect the update)
func (s *ClientAttachmentService) allStagesApproved(approval *models.Approval, justApprovedStageID uuid.UUID) bool {
	for _, st := range approval.Stages {
		if st.ID == justApprovedStageID {
			// This one was just approved
			continue
		}
		if st.Status != models.ApprovalStatusApproved {
			return false
		}
	}
	return true
}

// OnApprovalComplete handles the completion of a fully-approved approval
func (s *ClientAttachmentService) OnApprovalComplete(approval *models.Approval) error {
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
		route.Status = models.RouteStatusPendingDeploy
		// Use raw DB update to change just the status
		return s.updateRouteStatus(route.ID, models.RouteStatusPendingDeploy)
	}

	return nil
}

// OnApprovalRejected handles the rejection of a client attachment approval
func (s *ClientAttachmentService) OnApprovalRejected(approval *models.Approval) error {
	attachment, err := s.attachmentRepo.GetByID(approval.EntityID)
	if err != nil {
		return err
	}

	attachment.Status = models.AttachmentStatusRejected
	return s.attachmentRepo.Update(attachment)
}

// updateRouteStatus updates only the status of a route
func (s *ClientAttachmentService) updateRouteStatus(routeID uuid.UUID, status models.RouteStatus) error {
	route, err := s.routeRepo.GetByID(routeID)
	if err != nil {
		return err
	}
	route.Status = status
	return s.routeRepo.Update(route)
}
