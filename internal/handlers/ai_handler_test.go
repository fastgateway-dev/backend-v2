package handlers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/ai"
	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestAIHandler_GetStatus(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	status := ai.AIStatus{Enabled: true, Provider: "openai"}
	mockAI.On("GetStatus").Return(status)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/ai/status", nil)

	h.GetStatus(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	assert.Equal(t, true, result["enabled"])
	assert.Equal(t, "openai", result["provider"])
	mockAI.AssertExpectations(t)
}

func TestAIHandler_Generate_NotEnabled(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	mockAI.On("IsEnabled").Return(false)

	body, _ := json.Marshal(map[string]string{
		"mode":  "natural_language",
		"input": "create a route for /api",
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/generate", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)
	c.Params = gin.Params{{Key: "domainId", Value: uuid.New().String()}}

	h.Generate(c)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	mockAI.AssertExpectations(t)
}

func TestAIHandler_Generate_InvalidDomainID(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	mockAI.On("IsEnabled").Return(true)

	body, _ := json.Marshal(map[string]string{
		"mode":  "natural_language",
		"input": "create a route for /api",
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/generate", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)
	c.Params = gin.Params{{Key: "domainId", Value: "bad-id"}}

	h.Generate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAIHandler_Generate_BadBody(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	domainID := uuid.New()
	mockAI.On("IsEnabled").Return(true)

	// Missing required mode field
	body, _ := json.Marshal(map[string]string{"input": "create a route"})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/generate", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)
	c.Params = gin.Params{{Key: "domainId", Value: domainID.String()}}

	h.Generate(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAIHandler_Generate_DomainNotFound(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	domainID := uuid.New()
	mockAI.On("IsEnabled").Return(true)
	mockDomain.On("GetByID", domainID).Return(nil, errors.New("not found"))

	body, _ := json.Marshal(map[string]string{
		"mode":  "natural_language",
		"input": "create a route for /api",
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/generate", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)
	c.Params = gin.Params{{Key: "domainId", Value: domainID.String()}}

	h.Generate(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestAIHandler_Generate_RateLimited(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	domainID := uuid.New()
	domain := &models.Domain{ID: domainID, Name: "test-domain", Hostname: "test.example.com"}
	mockAI.On("IsEnabled").Return(true)
	mockDomain.On("GetByID", domainID).Return(domain, nil)
	mockAI.On("Generate", mock.Anything, user.ID, mock.AnythingOfType("ai.GenerateRequest")).Return(nil, errors.New("rate limit exceeded, please try again later"))

	body, _ := json.Marshal(map[string]string{
		"mode":  "natural_language",
		"input": "create a route for /api",
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/generate", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)
	c.Params = gin.Params{{Key: "domainId", Value: domainID.String()}}

	h.Generate(c)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	mockAI.AssertExpectations(t)
}

func TestAIHandler_Generate_ServiceError(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	domainID := uuid.New()
	domain := &models.Domain{ID: domainID, Name: "test-domain", Hostname: "test.example.com"}
	mockAI.On("IsEnabled").Return(true)
	mockDomain.On("GetByID", domainID).Return(domain, nil)
	mockAI.On("Generate", mock.Anything, user.ID, mock.AnythingOfType("ai.GenerateRequest")).Return(nil, errors.New("internal error"))

	body, _ := json.Marshal(map[string]string{
		"mode":  "natural_language",
		"input": "create a route for /api",
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/generate", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)
	c.Params = gin.Params{{Key: "domainId", Value: domainID.String()}}

	h.Generate(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAI.AssertExpectations(t)
}

func TestAIHandler_Review_NotEnabled(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	mockAI.On("IsEnabled").Return(false)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/review", nil)

	h.Review(c)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	mockAI.AssertExpectations(t)
}

func TestAIHandler_Review_BadBody(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	mockAI.On("IsEnabled").Return(true)

	// Missing required action field
	body, _ := json.Marshal(map[string]string{})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/review", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Review(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAIHandler_Review_NoYaml(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	mockAI.On("IsEnabled").Return(true)

	// Has action but no proposedYaml or currentYaml
	body, _ := json.Marshal(map[string]interface{}{
		"action": "create",
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/review", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Review(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAIHandler_Review_Success(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	reviewResult := &ai.ReviewResult{Summary: "Looks good"}

	mockAI.On("IsEnabled").Return(true)
	mockAI.On("Review", mock.Anything, user.ID, mock.AnythingOfType("ai.ReviewRequest")).Return(reviewResult, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"action":       "create",
		"proposedYaml": map[string]string{"httpRoute": "apiVersion: v1"},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/review", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Review(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAI.AssertExpectations(t)
}

func TestAIHandler_Review_RateLimited(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	mockAI.On("IsEnabled").Return(true)
	mockAI.On("Review", mock.Anything, user.ID, mock.AnythingOfType("ai.ReviewRequest")).Return(nil, errors.New("rate limit exceeded, please try again later"))

	body, _ := json.Marshal(map[string]interface{}{
		"action":       "create",
		"proposedYaml": map[string]string{"httpRoute": "apiVersion: v1"},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/review", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Review(c)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	mockAI.AssertExpectations(t)
}

func TestAIHandler_Chat_NotEnabled(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	mockAI.On("IsEnabled").Return(false)

	body, _ := json.Marshal(map[string]string{"message": "help me configure a route"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/chat", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Chat(c)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	mockAI.AssertExpectations(t)
}

func TestAIHandler_Chat_BadBody(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	mockAI.On("IsEnabled").Return(true)

	// Missing required message field
	body, _ := json.Marshal(map[string]string{})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/chat", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Chat(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAIHandler_Chat_RateLimited(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	mockAI.On("IsEnabled").Return(true)
	mockAI.On("Chat", mock.Anything, user.ID, mock.AnythingOfType("ai.ChatRequest")).Return(nil, errors.New("rate limit exceeded, please try again later"))

	body, _ := json.Marshal(map[string]string{"message": "help"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/chat", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Chat(c)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	mockAI.AssertExpectations(t)
}

func TestAIHandler_Chat_ServiceError(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	mockAI.On("IsEnabled").Return(true)
	mockAI.On("Chat", mock.Anything, user.ID, mock.AnythingOfType("ai.ChatRequest")).Return(nil, errors.New("internal error"))

	body, _ := json.Marshal(map[string]string{"message": "help"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/chat", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Chat(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAI.AssertExpectations(t)
}

func TestAIHandler_ReviewApproval_NotEnabled(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	mockAI.On("IsEnabled").Return(false)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/review-approval", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: uuid.New().String()},
		{Key: "approvalId", Value: uuid.New().String()},
	}

	h.ReviewApproval(c)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestAIHandler_ReviewApproval_InvalidProjectID(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	mockAI.On("IsEnabled").Return(true)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/review-approval", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: "bad-id"},
		{Key: "approvalId", Value: uuid.New().String()},
	}

	h.ReviewApproval(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAIHandler_ReviewApproval_InvalidApprovalID(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	mockAI.On("IsEnabled").Return(true)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/review-approval", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: uuid.New().String()},
		{Key: "approvalId", Value: "bad-id"},
	}

	h.ReviewApproval(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAIHandler_ReviewApproval_NotFound(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	approvalID := uuid.New()
	mockAI.On("IsEnabled").Return(true)
	mockApproval.On("GetByID", approvalID).Return(nil, errors.New("not found"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/review-approval", nil)
	c.Set("user", testUser())
	c.Params = gin.Params{
		{Key: "projectId", Value: uuid.New().String()},
		{Key: "approvalId", Value: approvalID.String()},
	}

	h.ReviewApproval(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestAIHandler_ReviewApproval_WrongProject(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	projectID := uuid.New()
	approvalID := uuid.New()
	differentProjectID := uuid.New()
	approval := &models.Approval{ID: approvalID, ProjectID: differentProjectID, Status: "pending"}
	mockAI.On("IsEnabled").Return(true)
	mockApproval.On("GetByID", approvalID).Return(approval, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/review-approval", nil)
	c.Set("user", testUser())
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
	}

	h.ReviewApproval(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestAIHandler_ReviewApproval_AlreadyReviewed(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	projectID := uuid.New()
	approvalID := uuid.New()
	approval := &models.Approval{
		ID:        approvalID,
		ProjectID: projectID,
		Status:    "pending",
		AIReview:  json.RawMessage(`{"summary":"already reviewed"}`),
	}
	mockAI.On("IsEnabled").Return(true)
	mockApproval.On("GetByID", approvalID).Return(approval, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/ai/review-approval", nil)
	c.Set("user", testUser())
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
	}

	h.ReviewApproval(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestAIHandler_ReviewApproval_Success(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	approval := &models.Approval{
		ID:        approvalID,
		ProjectID: projectID,
		Status:    "pending",
	}
	diff := &services.ApprovalDiffResult{Action: "create", ProposedYAML: "yaml here"}
	reviewResult := &ai.ReviewResult{Summary: "Looks good"}

	mockAI.On("IsEnabled").Return(true)
	mockApproval.On("GetByID", approvalID).Return(approval, nil)
	mockApproval.On("GetDiff", approvalID).Return(diff, nil)
	mockAI.On("ReviewApproval", mock.Anything, user.ID, approval, diff).Return(reviewResult, nil)
	mockApproval.On("UpdateAIReview", approval).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/review-approval", nil)
	c.Set("user", user)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
	}

	h.ReviewApproval(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAI.AssertExpectations(t)
	mockApproval.AssertExpectations(t)
}

func TestAIHandler_ReviewApproval_GetDiffError(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	approval := &models.Approval{
		ID:        approvalID,
		ProjectID: projectID,
		Status:    "pending",
	}

	mockAI.On("IsEnabled").Return(true)
	mockApproval.On("GetByID", approvalID).Return(approval, nil)
	mockApproval.On("GetDiff", approvalID).Return(nil, errors.New("diff error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/review-approval", nil)
	c.Set("user", user)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
	}

	h.ReviewApproval(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestAIHandler_ReviewApproval_AIReviewError(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	approval := &models.Approval{
		ID:        approvalID,
		ProjectID: projectID,
		Status:    "pending",
	}
	diff := &services.ApprovalDiffResult{Action: "create", ProposedYAML: "yaml here"}

	mockAI.On("IsEnabled").Return(true)
	mockApproval.On("GetByID", approvalID).Return(approval, nil)
	mockApproval.On("GetDiff", approvalID).Return(diff, nil)
	mockAI.On("ReviewApproval", mock.Anything, user.ID, approval, diff).Return(nil, errors.New("ai error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/review-approval", nil)
	c.Set("user", user)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
	}

	h.ReviewApproval(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAI.AssertExpectations(t)
}

func TestAIHandler_ReviewApproval_RateLimited(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	approval := &models.Approval{
		ID:        approvalID,
		ProjectID: projectID,
		Status:    "pending",
	}
	diff := &services.ApprovalDiffResult{Action: "create", ProposedYAML: "yaml here"}

	mockAI.On("IsEnabled").Return(true)
	mockApproval.On("GetByID", approvalID).Return(approval, nil)
	mockApproval.On("GetDiff", approvalID).Return(diff, nil)
	mockAI.On("ReviewApproval", mock.Anything, user.ID, approval, diff).Return(nil, errors.New("rate limit exceeded, please try again later"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/review-approval", nil)
	c.Set("user", user)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
	}

	h.ReviewApproval(c)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	mockAI.AssertExpectations(t)
}

func TestAIHandler_ReviewApproval_UpdateAIReviewConflict(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	approval := &models.Approval{
		ID:        approvalID,
		ProjectID: projectID,
		Status:    "pending",
	}
	diff := &services.ApprovalDiffResult{Action: "create", ProposedYAML: "yaml here"}
	reviewResult := &ai.ReviewResult{Summary: "Looks good"}

	mockAI.On("IsEnabled").Return(true)
	mockApproval.On("GetByID", approvalID).Return(approval, nil)
	mockApproval.On("GetDiff", approvalID).Return(diff, nil)
	mockAI.On("ReviewApproval", mock.Anything, user.ID, approval, diff).Return(reviewResult, nil)
	mockApproval.On("UpdateAIReview", approval).Return(errors.New("AI review already exists for this approval"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/review-approval", nil)
	c.Set("user", user)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
	}

	h.ReviewApproval(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestAIHandler_ReviewApproval_UpdateAIReviewError(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	approval := &models.Approval{
		ID:        approvalID,
		ProjectID: projectID,
		Status:    "pending",
	}
	diff := &services.ApprovalDiffResult{Action: "create", ProposedYAML: "yaml here"}
	reviewResult := &ai.ReviewResult{Summary: "Looks good"}

	mockAI.On("IsEnabled").Return(true)
	mockApproval.On("GetByID", approvalID).Return(approval, nil)
	mockApproval.On("GetDiff", approvalID).Return(diff, nil)
	mockAI.On("ReviewApproval", mock.Anything, user.ID, approval, diff).Return(reviewResult, nil)
	mockApproval.On("UpdateAIReview", approval).Return(errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/review-approval", nil)
	c.Set("user", user)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "approvalId", Value: approvalID.String()},
	}

	h.ReviewApproval(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockApproval.AssertExpectations(t)
}

func TestAIHandler_Review_ServiceError(t *testing.T) {
	mockAI := new(mocks.MockAIService)
	mockApproval := new(mocks.MockApprovalService)
	mockDomain := new(mocks.MockDomainService)
	h := handlers.NewAIHandler(mockAI, mockApproval, mockDomain)

	user := testUser()
	mockAI.On("IsEnabled").Return(true)
	mockAI.On("Review", mock.Anything, user.ID, mock.AnythingOfType("ai.ReviewRequest")).Return(nil, errors.New("internal error"))

	body, _ := json.Marshal(map[string]interface{}{
		"action":       "create",
		"proposedYaml": map[string]string{"httpRoute": "apiVersion: v1"},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/ai/review", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Review(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAI.AssertExpectations(t)
}

var _ = mock.Anything // suppress unused import if needed
