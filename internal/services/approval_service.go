package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// ApprovalService handles approval business logic using the unified approval system
type ApprovalService struct {
	approvalRepo             repository.UnifiedApprovalRepositoryInterface
	policyRepo               repository.ApprovalPolicyRepositoryInterface
	teamRepo                 repository.TeamRepositoryInterface
	routeRepo                repository.RouteRepositoryInterface
	projectRepo              repository.ProjectRepositoryInterface
	domainRepo               repository.DomainRepositoryInterface
	k8sService               KubernetesServiceInterface
	securityPolicyRepo       repository.SecurityPolicyRepositoryInterface
	backendTrafficPolicyRepo repository.BackendTrafficPolicyRepositoryInterface
	clientAttachmentService  *ClientAttachmentService
	stageReviewRepo          repository.ApprovalStageReviewRepositoryInterface
}

// NewApprovalService creates a new approval service
func NewApprovalService(
	approvalRepo repository.UnifiedApprovalRepositoryInterface,
	policyRepo repository.ApprovalPolicyRepositoryInterface,
	teamRepo repository.TeamRepositoryInterface,
	routeRepo repository.RouteRepositoryInterface,
	projectRepo repository.ProjectRepositoryInterface,
	domainRepo repository.DomainRepositoryInterface,
	k8sService KubernetesServiceInterface,
) *ApprovalService {
	return &ApprovalService{
		approvalRepo: approvalRepo,
		policyRepo:   policyRepo,
		teamRepo:     teamRepo,
		routeRepo:    routeRepo,
		projectRepo:  projectRepo,
		domainRepo:   domainRepo,
		k8sService:   k8sService,
	}
}

// SetSecurityPolicyRepository sets the security policy repository
func (s *ApprovalService) SetSecurityPolicyRepository(repo repository.SecurityPolicyRepositoryInterface) {
	s.securityPolicyRepo = repo
}

// SetBackendTrafficPolicyRepository sets the backend traffic policy repository
func (s *ApprovalService) SetBackendTrafficPolicyRepository(repo repository.BackendTrafficPolicyRepositoryInterface) {
	s.backendTrafficPolicyRepo = repo
}

// SetClientAttachmentService sets the client attachment service for approval callbacks
func (s *ApprovalService) SetClientAttachmentService(cas *ClientAttachmentService) {
	s.clientAttachmentService = cas
}

// SetStageReviewRepository sets the stage review repository for multi-approver support
func (s *ApprovalService) SetStageReviewRepository(repo repository.ApprovalStageReviewRepositoryInterface) {
	s.stageReviewRepo = repo
}

// GetByID gets an approval by ID
func (s *ApprovalService) GetByID(id uuid.UUID) (*models.Approval, error) {
	return s.approvalRepo.GetByID(id)
}

// ListByProjectID lists approvals for a project with an optional entity type filter
func (s *ApprovalService) ListByProjectID(projectID uuid.UUID, page, limit int, status string, entityType string) ([]models.Approval, int64, error) {
	approvals, total, err := s.approvalRepo.ListByProjectID(projectID, page, limit, status, entityType)
	if err != nil {
		return nil, 0, err
	}

	// Collect unique route entity IDs
	routeIDSet := make(map[uuid.UUID]struct{})
	for i := range approvals {
		if approvals[i].EntityType == models.ApprovalEntityRoute {
			routeIDSet[approvals[i].EntityID] = struct{}{}
		}
	}

	if len(routeIDSet) == 0 {
		return approvals, total, nil
	}

	routeIDs := make([]uuid.UUID, 0, len(routeIDSet))
	for id := range routeIDSet {
		routeIDs = append(routeIDs, id)
	}

	// Batch-fetch routes
	routes, err := s.routeRepo.GetByIDs(routeIDs)
	if err != nil {
		// Non-fatal: return approvals without enrichment
		return approvals, total, nil
	}

	routeMap := make(map[uuid.UUID]models.Route, len(routes))
	domainIDSet := make(map[uuid.UUID]struct{})
	for _, r := range routes {
		routeMap[r.ID] = r
		domainIDSet[r.DomainID] = struct{}{}
	}

	// Batch-fetch domains
	domainIDs := make([]uuid.UUID, 0, len(domainIDSet))
	for id := range domainIDSet {
		domainIDs = append(domainIDs, id)
	}

	domainMap := make(map[uuid.UUID]models.Domain)
	domains, err := s.domainRepo.GetByIDs(domainIDs)
	if err == nil {
		for _, d := range domains {
			domainMap[d.ID] = d
		}
	}

	// Enrich approvals
	for i := range approvals {
		if approvals[i].EntityType == models.ApprovalEntityRoute {
			if route, ok := routeMap[approvals[i].EntityID]; ok {
				approvals[i].EntityName = route.Name
				if domain, ok := domainMap[route.DomainID]; ok {
					approvals[i].DomainName = domain.Hostname
				}
			}
		}
	}

	return approvals, total, nil
}

// CountPendingByProjectID counts pending approvals for a project
func (s *ApprovalService) CountPendingByProjectID(projectID uuid.UUID) (int64, error) {
	return s.approvalRepo.CountPendingByProjectID(projectID)
}

// ApproveStage approves a specific stage of a multi-stage approval
func (s *ApprovalService) ApproveStage(approvalID, stageID uuid.UUID, reviewer *models.User) (*models.Approval, error) {
	approval, err := s.approvalRepo.GetByID(approvalID)
	if err != nil {
		return nil, err
	}

	if approval.Status != models.ApprovalStatusPending {
		return nil, errors.New("approval is not pending")
	}

	// Find the target stage
	stage, stageIndex := s.findStage(approval, stageID)
	if stage == nil {
		return nil, errors.New("stage not found in this approval")
	}

	if stage.Status != models.ApprovalStatusPending {
		return nil, errors.New("stage is not pending")
	}

	// Validate submitter cannot approve their own submission (unless project allows self-approval)
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

	// Validate all previous stages are approved (sequential approval)
	for i := 0; i < stageIndex; i++ {
		if approval.Stages[i].Status != models.ApprovalStatusApproved {
			return nil, errors.New("previous stages must be approved first")
		}
	}

	// Check if reviewer is owner or project admin (bypass permission/team checks)
	isOwner := reviewer.Role == models.UserRoleOwner
	isProjectAdmin := false
	if !isOwner {
		isProjectAdmin, _ = s.projectRepo.IsAdmin(approval.ProjectID, reviewer.ID)
	}

	// Owner and project admin can approve any stage (except their own submission, already checked)
	if !isOwner && !isProjectAdmin {
		// Validate reviewer has the required permission
		hasPerm, err := s.teamRepo.HasPermissionInProject(approval.ProjectID, reviewer.ID, models.Permission(stage.RequiredPermission))
		if err != nil {
			return nil, err
		}
		if !hasPerm {
			return nil, errors.New("reviewer does not have the required permission")
		}

		// If stage has required_team_id, validate reviewer is a member of that team
		if stage.RequiredTeamID != nil {
			isMember, err := s.teamRepo.IsMember(*stage.RequiredTeamID, reviewer.ID)
			if err != nil {
				return nil, err
			}
			if !isMember {
				return nil, errors.New("reviewer is not a member of the required team")
			}
		}
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

	// Stage is approved (enough approvals received)
	now := time.Now()
	stage.Status = models.ApprovalStatusApproved
	stage.ReviewedBy = &reviewer.ID
	stage.ReviewedAt = &now

	if err := s.approvalRepo.UpdateStage(stage); err != nil {
		return nil, err
	}

	// Check if ALL stages are now approved
	allApproved := true
	for i, st := range approval.Stages {
		if i == stageIndex {
			// We just approved this one
			continue
		}
		if st.Status != models.ApprovalStatusApproved {
			allApproved = false
			break
		}
	}

	if allApproved {
		approval.Status = models.ApprovalStatusApproved
		if err := s.approvalRepo.Update(approval); err != nil {
			return nil, err
		}

		if err := s.onApprovalComplete(approval); err != nil {
			return nil, err
		}
	}

	// Re-fetch to get updated relationships
	return s.approvalRepo.GetByID(approvalID)
}

// RejectStage rejects a specific stage of a multi-stage approval
func (s *ApprovalService) RejectStage(approvalID, stageID uuid.UUID, reviewer *models.User, comment string) (*models.Approval, error) {
	if comment == "" {
		return nil, errors.New("rejection comment is required")
	}

	approval, err := s.approvalRepo.GetByID(approvalID)
	if err != nil {
		return nil, err
	}

	if approval.Status != models.ApprovalStatusPending {
		return nil, errors.New("approval is not pending")
	}

	// Find the target stage
	stage, stageIndex := s.findStage(approval, stageID)
	if stage == nil {
		return nil, errors.New("stage not found in this approval")
	}

	if stage.Status != models.ApprovalStatusPending {
		return nil, errors.New("stage is not pending")
	}

	// Validate submitter cannot reject their own submission (unless project allows self-approval)
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

	// Validate all previous stages are approved (sequential)
	for i := 0; i < stageIndex; i++ {
		if approval.Stages[i].Status != models.ApprovalStatusApproved {
			return nil, errors.New("previous stages must be approved first")
		}
	}

	// Check if reviewer is owner or project admin (bypass permission/team checks)
	isOwner := reviewer.Role == models.UserRoleOwner
	isProjectAdmin := false
	if !isOwner {
		isProjectAdmin, _ = s.projectRepo.IsAdmin(approval.ProjectID, reviewer.ID)
	}

	// Owner and project admin can reject any stage (except their own submission, already checked)
	if !isOwner && !isProjectAdmin {
		// Validate reviewer has the required permission
		hasPerm, err := s.teamRepo.HasPermissionInProject(approval.ProjectID, reviewer.ID, models.Permission(stage.RequiredPermission))
		if err != nil {
			return nil, err
		}
		if !hasPerm {
			return nil, errors.New("reviewer does not have the required permission")
		}

		// If stage has required_team_id, validate reviewer is a member of that team
		if stage.RequiredTeamID != nil {
			isMember, err := s.teamRepo.IsMember(*stage.RequiredTeamID, reviewer.ID)
			if err != nil {
				return nil, err
			}
			if !isMember {
				return nil, errors.New("reviewer is not a member of the required team")
			}
		}
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

	// Mark stage as rejected
	now := time.Now()
	stage.Status = models.ApprovalStatusRejected
	stage.ReviewedBy = &reviewer.ID
	stage.ReviewedAt = &now
	stage.Comment = comment

	if err := s.approvalRepo.UpdateStage(stage); err != nil {
		return nil, err
	}

	// Mark overall approval as rejected
	approval.Status = models.ApprovalStatusRejected
	if err := s.approvalRepo.Update(approval); err != nil {
		return nil, err
	}

	if err := s.onApprovalRejected(approval); err != nil {
		return nil, err
	}

	// Re-fetch to get updated relationships
	return s.approvalRepo.GetByID(approvalID)
}

// findStage locates a stage within an approval by its ID.
// Returns the stage pointer and its index, or nil/-1 if not found.
// Stages are sorted by StageOrder before searching.
func (s *ApprovalService) findStage(approval *models.Approval, stageID uuid.UUID) (*models.ApprovalStage, int) {
	// Ensure stages are sorted by order
	sort.Slice(approval.Stages, func(i, j int) bool {
		return approval.Stages[i].StageOrder < approval.Stages[j].StageOrder
	})

	for i := range approval.Stages {
		if approval.Stages[i].ID == stageID {
			return &approval.Stages[i], i
		}
	}
	return nil, -1
}

// onApprovalComplete handles post-approval logic when all stages are approved
func (s *ApprovalService) onApprovalComplete(approval *models.Approval) error {
	switch approval.EntityType {
	case models.ApprovalEntityRoute:
		return s.onRouteApprovalComplete(approval)
	case models.ApprovalEntityClientAttachment:
		if s.clientAttachmentService != nil {
			return s.clientAttachmentService.OnApprovalComplete(approval)
		}
		return nil
	default:
		return nil
	}
}

// onApprovalRejected handles post-rejection logic
func (s *ApprovalService) onApprovalRejected(approval *models.Approval) error {
	switch approval.EntityType {
	case models.ApprovalEntityRoute:
		return s.onRouteApprovalRejected(approval)
	case models.ApprovalEntityClientAttachment:
		if s.clientAttachmentService != nil {
			return s.clientAttachmentService.OnApprovalRejected(approval)
		}
		return nil
	default:
		return nil
	}
}

// onRouteApprovalComplete updates route status when a route approval is fully approved
func (s *ApprovalService) onRouteApprovalComplete(approval *models.Approval) error {
	route, err := s.routeRepo.GetByID(approval.EntityID)
	if err != nil {
		return err
	}

	switch approval.Action {
	case models.ApprovalActionCreate:
		// Parse the config snapshot and apply to route
		if approval.ConfigSnapshot != nil {
			var snapshot models.RouteApprovalSnapshot
			if err := json.Unmarshal(approval.ConfigSnapshot, &snapshot); err != nil {
				return err
			}
			if snapshot.RouteConfig != nil {
				route.Config = *snapshot.RouteConfig
			}
		}
		route.Status = models.RouteStatusApproved

	case models.ApprovalActionUpdate:
		if approval.ConfigSnapshot != nil {
			var snapshot models.RouteApprovalSnapshot
			if err := json.Unmarshal(approval.ConfigSnapshot, &snapshot); err != nil {
				return err
			}
			if snapshot.RouteConfig != nil {
				route.Config = *snapshot.RouteConfig
			}
		}
		route.Status = models.RouteStatusPendingDeploy

	case models.ApprovalActionDelete:
		route.Status = models.RouteStatusPendingDeploy
	}

	return s.routeRepo.Update(route)
}

// onRouteApprovalRejected updates route status when a route approval is rejected
func (s *ApprovalService) onRouteApprovalRejected(approval *models.Approval) error {
	route, err := s.routeRepo.GetByID(approval.EntityID)
	if err != nil {
		return err
	}

	switch approval.Action {
	case models.ApprovalActionCreate:
		route.Status = models.RouteStatusRejected
	case models.ApprovalActionUpdate:
		route.Status = models.RouteStatusActive
	case models.ApprovalActionDelete:
		route.Status = models.RouteStatusActive
	}

	return s.routeRepo.Update(route)
}

// CancelApproval cancels an approval request (pending or approved but not yet deployed).
// Permitted by: original submitter, project admin/owner, route owner team members.
func (s *ApprovalService) CancelApproval(approvalID uuid.UUID, user *models.User) (*models.Approval, error) {
	approval, err := s.approvalRepo.GetByID(approvalID)
	if err != nil {
		return nil, err
	}

	// Only pending or approved approvals can be cancelled
	if approval.Status != models.ApprovalStatusPending && approval.Status != models.ApprovalStatusApproved {
		return nil, errors.New("approval cannot be cancelled in its current state")
	}

	// Permission check: submitter, owner, project admin, or route owner team member
	isSubmitter := approval.SubmittedBy == user.ID
	isOwner := user.Role == models.UserRoleOwner
	isProjectAdmin := false
	if !isOwner {
		isProjectAdmin, _ = s.projectRepo.IsAdmin(approval.ProjectID, user.ID)
	}

	isRouteTeamMember := false
	if !isSubmitter && !isOwner && !isProjectAdmin {
		if approval.EntityType == models.ApprovalEntityRoute {
			route, err := s.routeRepo.GetByID(approval.EntityID)
			if err == nil {
				isRouteTeamMember, _ = s.teamRepo.IsMember(route.TeamID, user.ID)
			}
		}
	}

	if !isSubmitter && !isOwner && !isProjectAdmin && !isRouteTeamMember {
		return nil, errors.New("you do not have permission to cancel this approval")
	}

	// Mark approval as cancelled
	approval.Status = models.ApprovalStatusCancelled
	if err := s.approvalRepo.Update(approval); err != nil {
		return nil, err
	}

	// Revert entity state
	if err := s.onApprovalCancelled(approval); err != nil {
		return nil, err
	}

	return s.approvalRepo.GetByID(approvalID)
}

// onApprovalCancelled handles post-cancellation logic
func (s *ApprovalService) onApprovalCancelled(approval *models.Approval) error {
	switch approval.EntityType {
	case models.ApprovalEntityRoute:
		return s.onRouteApprovalCancelled(approval)
	case models.ApprovalEntityClientAttachment:
		if s.clientAttachmentService != nil {
			return s.clientAttachmentService.OnApprovalRejected(approval)
		}
		return nil
	default:
		return nil
	}
}

// onRouteApprovalCancelled updates route state when a route approval is cancelled
func (s *ApprovalService) onRouteApprovalCancelled(approval *models.Approval) error {
	switch approval.Action {
	case models.ApprovalActionCreate:
		// Route was never deployed, delete it entirely
		return s.routeRepo.Delete(approval.EntityID)
	case models.ApprovalActionUpdate, models.ApprovalActionDelete:
		// Route is already deployed, revert to active
		route, err := s.routeRepo.GetByID(approval.EntityID)
		if err != nil {
			return err
		}
		route.Status = models.RouteStatusActive
		return s.routeRepo.Update(route)
	}
	return nil
}

// ListPolicies lists all approval policies for a project
func (s *ApprovalService) ListPolicies(projectID uuid.UUID) ([]models.ApprovalPolicy, error) {
	return s.policyRepo.ListByProjectID(projectID)
}

// UpsertPolicy creates or updates an approval policy
func (s *ApprovalService) UpsertPolicy(policy *models.ApprovalPolicy) error {
	return s.policyRepo.Upsert(policy)
}

// ApprovalDiffResult represents the diff for an approval
type ApprovalDiffResult struct {
	Action                           string          `json:"action"`
	CurrentYAML                      string          `json:"currentYaml,omitempty"`
	ProposedYAML                     string          `json:"proposedYaml,omitempty"`
	CurrentSecurityPolicyYAML        string          `json:"currentSecurityPolicyYaml,omitempty"`
	ProposedSecurityPolicyYAML       string          `json:"proposedSecurityPolicyYaml,omitempty"`
	CurrentBackendTrafficPolicyYAML  string          `json:"currentBackendTrafficPolicyYaml,omitempty"`
	ProposedBackendTrafficPolicyYAML string          `json:"proposedBackendTrafficPolicyYaml,omitempty"`
	CurrentEnvoyExtensionPolicyYAML  string          `json:"currentEnvoyExtensionPolicyYaml,omitempty"`
	ProposedEnvoyExtensionPolicyYAML string          `json:"proposedEnvoyExtensionPolicyYaml,omitempty"`
	CurrentBackendYAML               string          `json:"currentBackendYaml,omitempty"`
	ProposedBackendYAML              string          `json:"proposedBackendYaml,omitempty"`
	ChangeDescription                string          `json:"changeDescription,omitempty"`
	AIReview                         json.RawMessage `json:"aiReview,omitempty"`
}

// GetDiff generates YAML diff for an approval request
func (s *ApprovalService) GetDiff(id uuid.UUID) (*ApprovalDiffResult, error) {
	approval, err := s.approvalRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// GetDiff is only applicable to route approvals
	if approval.EntityType != models.ApprovalEntityRoute {
		return nil, errors.New("diff is only available for route approvals")
	}

	route, err := s.routeRepo.GetByID(approval.EntityID)
	if err != nil {
		return nil, err
	}

	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, err
	}

	result := &ApprovalDiffResult{
		Action: string(approval.Action),
	}

	// Helper to build a temporary route for YAML generation
	buildTempRoute := func(config models.RouteConfig) *models.Route {
		return &models.Route{
			ID:           route.ID,
			DomainID:     route.DomainID,
			TeamID:       route.TeamID,
			Name:         route.Name,
			Protocol:     route.Protocol,
			Config:       config,
			K8sRouteName: route.K8sRouteName,
		}
	}

	// Helper to generate SecurityPolicy YAML from a config snapshot
	generateSPYaml := func(tempRoute *models.Route, spConfig *models.SecurityPolicyConfig) string {
		if spConfig == nil || !spConfig.HasAnyConfig() {
			return ""
		}
		tempPolicy := &models.SecurityPolicy{
			RouteID:   tempRoute.ID,
			ProjectID: domain.ProjectID,
			Config:    *spConfig,
		}
		return generateSecurityPolicyYAMLFromDB(tempRoute, domain, tempPolicy)
	}

	// Helper to generate BackendTrafficPolicy YAML from a config snapshot
	generateBTPYaml := func(tempRoute *models.Route, btpConfig *models.BackendTrafficPolicyConfig) string {
		if btpConfig == nil || btpConfig.IsEmpty() {
			return ""
		}
		routeID := tempRoute.ID
		tempPolicy := &models.BackendTrafficPolicy{
			RouteID:   &routeID,
			ProjectID: domain.ProjectID,
			Config:    *btpConfig,
		}
		return generateBackendTrafficPolicyYAMLFromDB(tempRoute, domain, tempPolicy)
	}

	// Helper to generate EnvoyExtensionPolicy YAML from config snapshots (extensions + WAF)
	generateEEPYaml := func(tempRoute *models.Route, extConfig *models.EnvoyExtensionPolicyConfig, wafConfig *models.WafPolicyConfig) string {
		hasExtensions := extConfig != nil && !extConfig.IsEmpty()
		hasWaf := wafConfig != nil && !wafConfig.IsEmpty()

		if !hasExtensions && !hasWaf {
			return ""
		}

		var extPolicy *models.EnvoyExtensionPolicy
		if hasExtensions {
			extPolicy = &models.EnvoyExtensionPolicy{
				RouteID:   &tempRoute.ID,
				ProjectID: domain.ProjectID,
				Config:    *extConfig,
			}
		}
		var wafPolicy *models.WafPolicy
		if hasWaf {
			wafPolicy = &models.WafPolicy{
				RouteID:   tempRoute.ID,
				ProjectID: domain.ProjectID,
				Config:    *wafConfig,
			}
		}

		return generateEnvoyExtensionPolicyYAMLFromSnapshot(tempRoute, domain, extPolicy, wafPolicy)
	}

	// Parse config snapshot and previous config from json.RawMessage
	var snapshot models.RouteApprovalSnapshot
	if approval.ConfigSnapshot != nil {
		if err := json.Unmarshal(approval.ConfigSnapshot, &snapshot); err != nil {
			return nil, err
		}
	}

	var previousSnapshot models.RouteApprovalSnapshot
	if approval.PreviousConfig != nil {
		if err := json.Unmarshal(approval.PreviousConfig, &previousSnapshot); err != nil {
			return nil, err
		}
	}

	switch approval.Action {
	case models.ApprovalActionCreate:
		// For create, show only the proposed YAML
		if snapshot.RouteConfig != nil {
			tempRoute := buildTempRoute(*snapshot.RouteConfig)
			result.ProposedYAML = generateHTTPRouteYAML(tempRoute, domain)
			result.ProposedSecurityPolicyYAML = generateSPYaml(tempRoute, snapshot.SecurityPolicy)
			result.ProposedBackendTrafficPolicyYAML = generateBTPYaml(tempRoute, snapshot.BackendTrafficPolicy)
			result.ProposedEnvoyExtensionPolicyYAML = generateEEPYaml(tempRoute, snapshot.EnvoyExtensionPolicy, snapshot.WafPolicy)
			result.ProposedBackendYAML = generateBackendYAMLs(tempRoute, domain)
		}

	case models.ApprovalActionUpdate:
		// For update, show current (previousConfig) and proposed (configSnapshot)
		if previousSnapshot.RouteConfig != nil {
			currentRoute := buildTempRoute(*previousSnapshot.RouteConfig)
			result.CurrentYAML = generateHTTPRouteYAML(currentRoute, domain)
			result.CurrentSecurityPolicyYAML = generateSPYaml(currentRoute, previousSnapshot.SecurityPolicy)
			result.CurrentBackendTrafficPolicyYAML = generateBTPYaml(currentRoute, previousSnapshot.BackendTrafficPolicy)
			result.CurrentEnvoyExtensionPolicyYAML = generateEEPYaml(currentRoute, previousSnapshot.EnvoyExtensionPolicy, previousSnapshot.WafPolicy)
			result.CurrentBackendYAML = generateBackendYAMLs(currentRoute, domain)
		}
		if snapshot.RouteConfig != nil {
			proposedRoute := buildTempRoute(*snapshot.RouteConfig)
			result.ProposedYAML = generateHTTPRouteYAML(proposedRoute, domain)
			result.ProposedSecurityPolicyYAML = generateSPYaml(proposedRoute, snapshot.SecurityPolicy)
			result.ProposedBackendTrafficPolicyYAML = generateBTPYaml(proposedRoute, snapshot.BackendTrafficPolicy)
			result.ProposedEnvoyExtensionPolicyYAML = generateEEPYaml(proposedRoute, snapshot.EnvoyExtensionPolicy, snapshot.WafPolicy)
			result.ProposedBackendYAML = generateBackendYAMLs(proposedRoute, domain)
		}

	case models.ApprovalActionDelete:
		// For delete, show current YAML (what will be deleted)
		result.CurrentYAML = generateHTTPRouteYAML(route, domain)
		result.CurrentSecurityPolicyYAML = generateSPYaml(route, previousSnapshot.SecurityPolicy)
		result.CurrentBackendTrafficPolicyYAML = generateBTPYaml(route, previousSnapshot.BackendTrafficPolicy)
		result.CurrentEnvoyExtensionPolicyYAML = generateEEPYaml(route, previousSnapshot.EnvoyExtensionPolicy, previousSnapshot.WafPolicy)
		result.CurrentBackendYAML = generateBackendYAMLs(route, domain)
	}

	result.ChangeDescription = approval.ChangeDescription
	result.AIReview = approval.AIReview

	return result, nil
}

// UpdateAIReview atomically sets the AI review on an approval, only if none exists yet.
func (s *ApprovalService) UpdateAIReview(approval *models.Approval) error {
	return s.approvalRepo.SetAIReview(approval.ID, approval.AIReview)
}
