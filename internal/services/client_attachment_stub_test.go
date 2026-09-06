package services_test

import (
	approvalpkg "github.com/fastgateway-dev/backend-v2/internal/approval"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services/clients"
	"github.com/stretchr/testify/mock"
)

// newCASApprovalEngine builds the approval engine that a
// ClientAttachmentService's AttachFrom*/RequestDetach (Submit) and
// ApproveStage/RejectStage paths delegate to. Phase 2D Task 8: those methods
// are pure delegation, so without an engine they nil-panic. Phase 2E Task 6:
// the engine is a required ClientAttachmentServiceDeps field, so it is built
// before the service and the completer is registered afterwards, instead of
// both arriving through SetApprovalEngine.
//
// approvalpkg.New panics on a nil dependency, so every slot the caller
// leaves nil is filled with a fresh mock. The stage-review store is always
// stubbed: the engine records every review (the pre-2D nil guard silently
// downgraded MinApprovers>1 to 1), and a single approval satisfies the
// default MinApprovers of 1.
func newCASApprovalEngine(
	approvalRepo *mocks.MockUnifiedApprovalRepository,
	policyRepo *mocks.MockApprovalPolicyRepository,
	teamRepo *mocks.MockTeamRepository,
	projectRepo *mocks.MockProjectRepository,
) *approvalpkg.Engine {
	if approvalRepo == nil {
		approvalRepo = new(mocks.MockUnifiedApprovalRepository)
	}
	if policyRepo == nil {
		policyRepo = new(mocks.MockApprovalPolicyRepository)
	}
	if teamRepo == nil {
		teamRepo = new(mocks.MockTeamRepository)
	}
	if projectRepo == nil {
		projectRepo = new(mocks.MockProjectRepository)
	}

	stageReviewRepo := new(mocks.MockApprovalStageReviewRepository)
	stageReviewRepo.On("ListByStageID", mock.Anything).
		Return([]models.ApprovalStageReview{}, nil).Maybe()
	stageReviewRepo.On("Create", mock.AnythingOfType("*models.ApprovalStageReview")).
		Return(nil).Maybe()
	stageReviewRepo.On("CountByStageAndDecision", mock.Anything, mock.Anything).
		Return(int64(1), nil).Maybe()

	return approvalpkg.New(approvalRepo, stageReviewRepo, policyRepo, teamRepo, projectRepo)
}

// newTestClientAttachmentServiceWithEngine is newTestClientAttachmentService
// for the tests that need to hold the engine themselves. Phase 2E Task 6 made
// the engine a required constructor parameter, so it can no longer be
// attached after the service is built; a nil engine here means "build one
// from this service's own repositories", which is what every call site that
// used to follow the constructor with wireCASApprovalEngine did.
//
// The service is registered as the client_attachment completer, exactly as
// wireCASApprovalEngine used to do.
func newTestClientAttachmentServiceWithEngine(
	engine *approvalpkg.Engine,
	attachmentRepo *mocks.MockClientAttachmentRepository,
	approvalRepo *mocks.MockUnifiedApprovalRepository,
	policyRepo *mocks.MockApprovalPolicyRepository,
	clientRepo *mocks.MockClientRepository,
	routeRepo *mocks.MockRouteRepository,
	domainRepo *mocks.MockDomainRepository,
	teamRepo *mocks.MockTeamRepository,
	projectRepo *mocks.MockProjectRepository,
) *clients.ClientAttachmentService {
	if attachmentRepo == nil {
		attachmentRepo = new(mocks.MockClientAttachmentRepository)
	}
	if approvalRepo == nil {
		approvalRepo = new(mocks.MockUnifiedApprovalRepository)
	}
	if policyRepo == nil {
		policyRepo = new(mocks.MockApprovalPolicyRepository)
	}
	if clientRepo == nil {
		clientRepo = new(mocks.MockClientRepository)
	}
	if routeRepo == nil {
		routeRepo = new(mocks.MockRouteRepository)
	}
	if domainRepo == nil {
		domainRepo = new(mocks.MockDomainRepository)
	}
	if teamRepo == nil {
		teamRepo = new(mocks.MockTeamRepository)
	}
	if projectRepo == nil {
		projectRepo = new(mocks.MockProjectRepository)
		projectRepo.On("GetByID", mock.Anything).
			Return(&models.Project{ApprovalEnabled: true}, nil).Maybe()
	}
	if engine == nil {
		engine = newCASApprovalEngine(approvalRepo, policyRepo, teamRepo, projectRepo)
	}
	svc := clients.NewClientAttachmentService(clients.ClientAttachmentServiceDeps{
		AttachmentRepo:     attachmentRepo,
		ApprovalRepo:       approvalRepo,
		ClientRepo:         clientRepo,
		RouteRepo:          routeRepo,
		DomainRepo:         domainRepo,
		ProjectRepo:        projectRepo,
		DomainSettingsRepo: new(mocks.MockDomainSettingsRepository),
		Approvals:          engine,
	})
	engine.Register(models.ApprovalEntityClientAttachment, svc)
	return svc
}
