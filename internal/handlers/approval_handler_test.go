package handlers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestApprovalHandler_List_Success(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	projectID := uuid.New()
	approvals := []models.Approval{
		{ID: uuid.New(), ProjectID: projectID, Status: "pending"},
		{ID: uuid.New(), ProjectID: projectID, Status: "pending"},
	}
	mockApproval.On("ListByProjectID", projectID, 1, 20, "pending", "").Return(approvals, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/approvals", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
	mockApproval.AssertExpectations(t)
}

func TestApprovalHandler_List_InvalidProjectID(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/bad-id/approvals", nil)
	c.Params = gin.Params{{Key: "projectId", Value: "bad-id"}}

	h.List(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApprovalHandler_Get_Success(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	approvalID := uuid.New()
	approval := &models.Approval{ID: approvalID, Status: "pending"}
	mockApproval.On("GetByID", approvalID).Return(approval, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/"+approvalID.String(), nil)
	c.Params = gin.Params{{Key: "approvalId", Value: approvalID.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestApprovalHandler_Get_NotFound(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	approvalID := uuid.New()
	mockApproval.On("GetByID", approvalID).Return(nil, errors.New("not found"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/"+approvalID.String(), nil)
	c.Params = gin.Params{{Key: "approvalId", Value: approvalID.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestApprovalHandler_Get_InvalidID(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/bad-id", nil)
	c.Params = gin.Params{{Key: "approvalId", Value: "bad-id"}}

	h.Get(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApprovalHandler_Approve_Success(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()
	entityID := uuid.New()
	approval := &models.Approval{ID: approvalID, ProjectID: projectID, EntityID: entityID, Status: "approved", Action: "create"}
	mockApproval.On("ApproveStage", approvalID, stageID, mock.AnythingOfType("*models.User")).Return(approval, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/"+approvalID.String()+"/stages/"+stageID.String()+"/approve", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
		{Key: "stageId", Value: stageID.String()},
	}
	c.Set("user", user)

	h.Approve(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestApprovalHandler_Approve_InvalidApprovalID(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()
	projectID := uuid.New()
	stageID := uuid.New()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/bad-id/stages/"+stageID.String()+"/approve", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: "bad-id"},
		{Key: "stageId", Value: stageID.String()},
	}
	c.Set("user", user)

	h.Approve(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApprovalHandler_Approve_InvalidStageID(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/"+approvalID.String()+"/stages/bad-id/approve", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
		{Key: "stageId", Value: "bad-id"},
	}
	c.Set("user", user)

	h.Approve(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApprovalHandler_Approve_ServiceError(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()
	mockApproval.On("ApproveStage", approvalID, stageID, mock.AnythingOfType("*models.User")).Return(nil, errors.New("cannot approve own submission"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/"+approvalID.String()+"/stages/"+stageID.String()+"/approve", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
		{Key: "stageId", Value: stageID.String()},
	}
	c.Set("user", user)

	h.Approve(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestApprovalHandler_GetDiff_Success(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	approvalID := uuid.New()
	diff := &services.ApprovalDiffResult{
		Action:       "create",
		ProposedYAML: "apiVersion: v1\nkind: HTTPRoute",
	}
	mockApproval.On("GetDiff", approvalID).Return(diff, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/"+approvalID.String()+"/diff", nil)
	c.Params = gin.Params{{Key: "approvalId", Value: approvalID.String()}}

	h.GetDiff(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestApprovalHandler_GetDiff_InvalidID(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/bad-id/diff", nil)
	c.Params = gin.Params{{Key: "approvalId", Value: "bad-id"}}

	h.GetDiff(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApprovalHandler_GetDiff_ServiceError(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	approvalID := uuid.New()
	mockApproval.On("GetDiff", approvalID).Return(nil, errors.New("internal error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/"+approvalID.String()+"/diff", nil)
	c.Params = gin.Params{{Key: "approvalId", Value: approvalID.String()}}

	h.GetDiff(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestApprovalHandler_Cancel_Success(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	entityID := uuid.New()
	approval := &models.Approval{ID: approvalID, ProjectID: projectID, EntityID: entityID, Status: "cancelled", Action: "create"}
	mockApproval.On("CancelApproval", approvalID, mock.AnythingOfType("*models.User")).Return(approval, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/"+approvalID.String()+"/cancel", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
	}
	c.Set("user", user)

	h.Cancel(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestApprovalHandler_Cancel_InvalidApprovalID(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()
	projectID := uuid.New()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/bad-id/cancel", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: "bad-id"},
	}
	c.Set("user", user)

	h.Cancel(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApprovalHandler_Reject_Success(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()
	entityID := uuid.New()
	approval := &models.Approval{ID: approvalID, ProjectID: projectID, EntityID: entityID, Status: "rejected", Action: "create"}
	mockApproval.On("RejectStage", approvalID, stageID, mock.AnythingOfType("*models.User"), "Not ready").Return(approval, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"comment": "Not ready"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/"+approvalID.String()+"/stages/"+stageID.String()+"/reject", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
		{Key: "stageId", Value: stageID.String()},
	}
	c.Set("user", user)

	h.Reject(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestApprovalHandler_Reject_MissingComment(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()

	// Empty body - missing required comment
	body, _ := json.Marshal(map[string]string{})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/"+approvalID.String()+"/stages/"+stageID.String()+"/reject", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
		{Key: "stageId", Value: stageID.String()},
	}
	c.Set("user", user)

	h.Reject(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApprovalHandler_Reject_InvalidProjectID(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/reject", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: "bad-id"},
		{Key: "approvalId", Value: uuid.New().String()},
		{Key: "stageId", Value: uuid.New().String()},
	}
	c.Set("user", user)

	h.Reject(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApprovalHandler_Reject_InvalidApprovalID(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()
	projectID := uuid.New()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/reject", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: "bad-id"},
		{Key: "stageId", Value: uuid.New().String()},
	}
	c.Set("user", user)

	h.Reject(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApprovalHandler_Reject_InvalidStageID(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/reject", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
		{Key: "stageId", Value: "bad-id"},
	}
	c.Set("user", user)

	h.Reject(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApprovalHandler_Reject_ServiceError(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()
	mockApproval.On("RejectStage", approvalID, stageID, mock.AnythingOfType("*models.User"), "bad route").Return(nil, errors.New("cannot reject"))

	body, _ := json.Marshal(map[string]string{"comment": "bad route"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/reject", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
		{Key: "stageId", Value: stageID.String()},
	}
	c.Set("user", user)

	h.Reject(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestApprovalHandler_Cancel_InvalidProjectID(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/cancel", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: "bad-id"},
		{Key: "approvalId", Value: uuid.New().String()},
	}
	c.Set("user", user)

	h.Cancel(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestApprovalHandler_Cancel_ServiceError(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	mockApproval.On("CancelApproval", approvalID, mock.AnythingOfType("*models.User")).Return(nil, errors.New("already approved"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/cancel", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
	}
	c.Set("user", user)

	h.Cancel(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestApprovalHandler_List_ServiceError(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	projectID := uuid.New()
	mockApproval.On("ListByProjectID", projectID, 1, 20, "pending", "").Return([]models.Approval{}, int64(0), errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/approvals", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestApprovalHandler_Approve_InvalidProjectID(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewApprovalHandler(mockApproval, mockAudit)

	user := testUser()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/approve", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: "bad-id"},
		{Key: "approvalId", Value: uuid.New().String()},
		{Key: "stageId", Value: uuid.New().String()},
	}
	c.Set("user", user)

	h.Approve(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
