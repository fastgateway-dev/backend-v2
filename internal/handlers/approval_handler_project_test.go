package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Approval IDs are global, but the approval routes are mounted under
// /projects/:projectId and only check membership of that project. An
// approval of another project must read as not found, so an approver in
// project A cannot read project B's approval or its YAML diff (which, for an
// L4 route, carries B's backend services, namespaces and ports).

func TestApprovalHandler_Get_OtherProjectIsNotFound(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	h := handlers.NewApprovalHandler(mockApproval, new(mocks.MockAuditService))
	approvalID, projectA, projectB := uuid.New(), uuid.New(), uuid.New()
	mockApproval.On("GetByID", approvalID).Return(&models.Approval{ID: approvalID, ProjectID: projectB}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectA.String()}, {Key: "approvalId", Value: approvalID.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}

func TestApprovalHandler_GetDiff_OtherProjectIsNotFound(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	h := handlers.NewApprovalHandler(mockApproval, new(mocks.MockAuditService))
	approvalID, projectA, projectB := uuid.New(), uuid.New(), uuid.New()
	mockApproval.On("GetByID", approvalID).Return(&models.Approval{ID: approvalID, ProjectID: projectB}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectA.String()}, {Key: "approvalId", Value: approvalID.String()}}

	h.GetDiff(c)

	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	mockApproval.AssertNotCalled(t, "GetDiff", mock.Anything)
}
