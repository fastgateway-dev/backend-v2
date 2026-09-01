package services

import (
	"encoding/json"
	"errors"
	"strings"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
)

// ApprovalService handles approval business logic using the unified approval system
type ApprovalService struct {
	approvalRepo repository.UnifiedApprovalRepositoryInterface
	policyRepo   repository.ApprovalPolicyRepositoryInterface
	routeRepo    repository.RouteRepositoryInterface
	domainRepo   repository.DomainRepositoryInterface
	wafConfig    routeplan.WAFConfig

	// engine owns approval planning and traversal for every entity type.
	// A required constructor dependency since Phase 2E Task 6.
	// See internal/approval.
	engine *approvalpkg.Engine
}

// ApprovalServiceDeps carries everything ApprovalService needs. Every field
// is required.
//
// Phase 2E Task 7 deleted six of them -- K8sService, TeamRepo, ProjectRepo,
// SecurityPolicyRepo, BackendTrafficPolicyRepo and StageReviewRepo. Each was
// stored in a field that nothing in this file ever read (K8sService held a
// full 58-method Kubernetes interface and called none of it). A dead field
// the constructor *requires* is worse than the setter it replaced: main.go
// has to wire something nothing reads, and the RequiresEveryDependency test
// asserts a requirement that does not exist.
type ApprovalServiceDeps struct {
	ApprovalRepo repository.UnifiedApprovalRepositoryInterface
	PolicyRepo   repository.ApprovalPolicyRepositoryInterface
	RouteRepo    repository.RouteRepositoryInterface
	DomainRepo   repository.DomainRepositoryInterface
	WafConfig    routeplan.WAFConfig

	// Approvals owns approval planning and traversal. The engine calls back
	// into the entity services as Completers, so main.go builds the engine
	// first and registers the completers afterwards.
	Approvals *approvalpkg.Engine
}

// NewApprovalService builds a fully-wired ApprovalService. It panics if a
// required dependency is missing: before Phase 2E these arrived through
// setters after construction, so a forgotten wiring line degraded silently
// at runtime instead of failing at start-up. Master design section 6.6.
func NewApprovalService(deps ApprovalServiceDeps) *ApprovalService {
	var missing []string
	if deps.ApprovalRepo == nil {
		missing = append(missing, "ApprovalRepo")
	}
	if deps.PolicyRepo == nil {
		missing = append(missing, "PolicyRepo")
	}
	if deps.RouteRepo == nil {
		missing = append(missing, "RouteRepo")
	}
	if deps.DomainRepo == nil {
		missing = append(missing, "DomainRepo")
	}
	if deps.Approvals == nil {
		missing = append(missing, "Approvals")
	}
	if len(missing) > 0 {
		panic("services.NewApprovalService: missing required dependency: " + strings.Join(missing, ", "))
	}

	return &ApprovalService{
		approvalRepo: deps.ApprovalRepo,
		policyRepo:   deps.PolicyRepo,
		routeRepo:    deps.RouteRepo,
		domainRepo:   deps.DomainRepo,
		wafConfig:    deps.WafConfig,
		engine:       deps.Approvals,
	}
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

// ApproveStage approves a specific stage of a multi-stage approval.
//
// Traversal now lives in internal/approval: the engine owns the stage
// lookup, the authorization checks, multi-approver counting and the
// completion callback, for every entity type. This method is the HTTP-facing
// name for it.
func (s *ApprovalService) ApproveStage(approvalID, stageID uuid.UUID, reviewer *models.User) (*models.Approval, error) {
	return s.engine.ApproveStage(approvalID, stageID, reviewer)
}

// RejectStage rejects a specific stage of a multi-stage approval, which
// rejects the whole approval. Delegates to internal/approval.
func (s *ApprovalService) RejectStage(approvalID, stageID uuid.UUID, reviewer *models.User, comment string) (*models.Approval, error) {
	return s.engine.RejectStage(approvalID, stageID, reviewer, comment)
}

// CancelApproval cancels an approval request (pending or approved but not
// yet deployed). Delegates to internal/approval, which permits the
// submitter, an instance owner and a project admin, plus whatever extra
// right the entity's completer grants through approval.CancelAuthorizer --
// for routes, membership of the route's owning team
// (RouteService.CanCancel).
func (s *ApprovalService) CancelApproval(approvalID uuid.UUID, user *models.User) (*models.Approval, error) {
	return s.engine.Cancel(approvalID, user)
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
		return routeplan.GenerateSecurityPolicyYAMLFromDB(tempRoute, domain, tempPolicy)
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
		return routeplan.GenerateBackendTrafficPolicyYAMLFromDB(tempRoute, domain, tempPolicy)
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

		return routeplan.GenerateEnvoyExtensionPolicyYAMLFromSnapshot(tempRoute, domain, extPolicy, wafPolicy, s.wafConfig)
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
			result.ProposedYAML = routeplan.GenerateHTTPRouteYAML(tempRoute, domain)
			result.ProposedSecurityPolicyYAML = generateSPYaml(tempRoute, snapshot.SecurityPolicy)
			result.ProposedBackendTrafficPolicyYAML = generateBTPYaml(tempRoute, snapshot.BackendTrafficPolicy)
			result.ProposedEnvoyExtensionPolicyYAML = generateEEPYaml(tempRoute, snapshot.EnvoyExtensionPolicy, snapshot.WafPolicy)
			result.ProposedBackendYAML = routeplan.GenerateBackendYAMLs(tempRoute, domain)
		}

	case models.ApprovalActionUpdate:
		// For update, show current (previousConfig) and proposed (configSnapshot)
		if previousSnapshot.RouteConfig != nil {
			currentRoute := buildTempRoute(*previousSnapshot.RouteConfig)
			result.CurrentYAML = routeplan.GenerateHTTPRouteYAML(currentRoute, domain)
			result.CurrentSecurityPolicyYAML = generateSPYaml(currentRoute, previousSnapshot.SecurityPolicy)
			result.CurrentBackendTrafficPolicyYAML = generateBTPYaml(currentRoute, previousSnapshot.BackendTrafficPolicy)
			result.CurrentEnvoyExtensionPolicyYAML = generateEEPYaml(currentRoute, previousSnapshot.EnvoyExtensionPolicy, previousSnapshot.WafPolicy)
			result.CurrentBackendYAML = routeplan.GenerateBackendYAMLs(currentRoute, domain)
		}
		if snapshot.RouteConfig != nil {
			proposedRoute := buildTempRoute(*snapshot.RouteConfig)
			result.ProposedYAML = routeplan.GenerateHTTPRouteYAML(proposedRoute, domain)
			result.ProposedSecurityPolicyYAML = generateSPYaml(proposedRoute, snapshot.SecurityPolicy)
			result.ProposedBackendTrafficPolicyYAML = generateBTPYaml(proposedRoute, snapshot.BackendTrafficPolicy)
			result.ProposedEnvoyExtensionPolicyYAML = generateEEPYaml(proposedRoute, snapshot.EnvoyExtensionPolicy, snapshot.WafPolicy)
			result.ProposedBackendYAML = routeplan.GenerateBackendYAMLs(proposedRoute, domain)
		}

	case models.ApprovalActionDelete:
		// For delete, show current YAML (what will be deleted)
		result.CurrentYAML = routeplan.GenerateHTTPRouteYAML(route, domain)
		result.CurrentSecurityPolicyYAML = generateSPYaml(route, previousSnapshot.SecurityPolicy)
		result.CurrentBackendTrafficPolicyYAML = generateBTPYaml(route, previousSnapshot.BackendTrafficPolicy)
		result.CurrentEnvoyExtensionPolicyYAML = generateEEPYaml(route, previousSnapshot.EnvoyExtensionPolicy, previousSnapshot.WafPolicy)
		result.CurrentBackendYAML = routeplan.GenerateBackendYAMLs(route, domain)
	}

	result.ChangeDescription = approval.ChangeDescription
	result.AIReview = approval.AIReview

	return result, nil
}

// UpdateAIReview atomically sets the AI review on an approval, only if none exists yet.
func (s *ApprovalService) UpdateAIReview(approval *models.Approval) error {
	return s.approvalRepo.SetAIReview(approval.ID, approval.AIReview)
}
