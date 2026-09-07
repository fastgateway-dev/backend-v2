package services

import (
	"encoding/json"
	"fmt"

	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

func marshalRouteSnapshot(config *models.RouteConfig, sp *models.SecurityPolicyConfig, btp *models.BackendTrafficPolicyConfig, eep *models.EnvoyExtensionPolicyConfig, waf *models.WafPolicyConfig) json.RawMessage {
	out, _ := json.Marshal(models.RouteApprovalSnapshot{
		RouteConfig:          config,
		SecurityPolicy:       sp,
		BackendTrafficPolicy: btp,
		EnvoyExtensionPolicy: eep,
		WafPolicy:            waf,
	})
	return out
}

func (s *RouteService) submitCreateApproval(route *models.Route, domain *models.Domain, input *CreateRouteInput, createdBy uuid.UUID, snapshotSP *models.SecurityPolicyConfig, snapshotBTP *models.BackendTrafficPolicyConfig, snapshotEEP *models.EnvoyExtensionPolicyConfig, snapshotWaf *models.WafPolicyConfig) (*models.Approval, bool, error) {
	// Check if approvals are disabled for this project
	project, err := s.projectRepo.GetByID(domain.ProjectID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to check project approval settings: %w", err)
	}
	if !project.ApprovalEnabled {
		// Skip approval — set route directly to approved.
		// route was just persisted at pending_create (struct literal
		// above), so this is pending_create -> approved and To always
		// writes; nothing else has been mutated since routeRepo.Create.
		if err := s.state.To(models.SiteRouteCreateFastPath, route, models.RouteStatusApproved,
			"route created, project approvals disabled"); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	configSnapshot := marshalRouteSnapshot(&input.Config, snapshotSP, snapshotBTP, snapshotEEP, snapshotWaf)

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
		return nil, false, err
	}

	return approval, false, nil
}

// updateApprovalSnapshots carries the proposed and previous policy configs
// for submitUpdateApproval by name, so proposed/previous cannot be swapped
// at the call site without the compiler catching a field mismatch.
type updateApprovalSnapshots struct {
	ProposedSecurityPolicy       *models.SecurityPolicyConfig
	ProposedBackendTrafficPolicy *models.BackendTrafficPolicyConfig
	ProposedEnvoyExtensionPolicy *models.EnvoyExtensionPolicyConfig
	ProposedWafPolicy            *models.WafPolicyConfig

	PreviousConfig               models.RouteConfig
	PreviousSecurityPolicy       *models.SecurityPolicyConfig
	PreviousBackendTrafficPolicy *models.BackendTrafficPolicyConfig
	PreviousEnvoyExtensionPolicy *models.EnvoyExtensionPolicyConfig
	PreviousWafPolicy            *models.WafPolicyConfig
}

func (s *RouteService) submitUpdateApproval(route *models.Route, domain *models.Domain, input *UpdateRouteInput, submittedBy uuid.UUID, snaps updateApprovalSnapshots) (*models.Approval, bool, error) {
	// Check if approvals are disabled for this project
	project, err := s.projectRepo.GetByID(domain.ProjectID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to check project approval settings: %w", err)
	}
	if !project.ApprovalEnabled {
		// Skip approval — set route directly to pending_deploy.
		// route sits at pending_update, persisted above, and no field
		// other than Status has been touched since.
		if err := s.state.To(models.SiteRouteUpdateFastPath, route, models.RouteStatusPendingDeploy,
			"route update submitted, project approvals disabled"); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	configSnapshot := marshalRouteSnapshot(&input.Config, snaps.ProposedSecurityPolicy, snaps.ProposedBackendTrafficPolicy, snaps.ProposedEnvoyExtensionPolicy, snaps.ProposedWafPolicy)

	// Build previous config snapshot
	prevConfigSnapshot := marshalRouteSnapshot(&snaps.PreviousConfig, snaps.PreviousSecurityPolicy, snaps.PreviousBackendTrafficPolicy, snaps.PreviousEnvoyExtensionPolicy, snaps.PreviousWafPolicy)

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
		return nil, false, err
	}

	return approval, false, nil
}

func (s *RouteService) submitDeleteApproval(route *models.Route, domain *models.Domain, submittedBy uuid.UUID, deletePrevSP *models.SecurityPolicyConfig, deletePrevBTP *models.BackendTrafficPolicyConfig, deletePrevEEP *models.EnvoyExtensionPolicyConfig, deletePrevWaf *models.WafPolicyConfig) (*models.Approval, bool, error) {
	// Check if approvals are disabled for this project
	project, err := s.projectRepo.GetByID(domain.ProjectID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to check project approval settings: %w", err)
	}
	if !project.ApprovalEnabled {
		// Skip approval — set route directly to pending_deploy.
		// route sits at pending_delete, persisted above.
		if err := s.state.To(models.SiteRouteDeleteFastPath, route, models.RouteStatusPendingDeploy,
			"route deletion submitted, project approvals disabled"); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	// Build config snapshot (current config being deleted)
	configSnapshot := marshalRouteSnapshot(&route.Config, deletePrevSP, deletePrevBTP, deletePrevEEP, deletePrevWaf)

	approval, err := s.approvals.Submit(approvalpkg.Spec{
		ProjectID:      domain.ProjectID,
		EntityType:     models.ApprovalEntityRoute,
		EntityID:       route.ID,
		Action:         models.ApprovalActionDelete,
		ConfigSnapshot: configSnapshot,
		SubmittedBy:    submittedBy,
	})
	if err != nil {
		return nil, false, err
	}

	return approval, false, nil
}
