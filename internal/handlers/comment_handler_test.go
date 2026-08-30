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
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestCommentHandler_Create_Success(t *testing.T) {
	mockComment := new(mocks.MockCommentService)
	h := handlers.NewCommentHandler(mockComment)

	user := testUser()
	approvalID := uuid.New()
	comment := &models.ApprovalComment{
		ID:         uuid.New(),
		ApprovalID: approvalID,
		UserID:     user.ID,
		Body:       "Looks good!",
	}
	mockComment.On("Create", approvalID, mock.AnythingOfType("*models.User"), "Looks good!").Return(comment, nil)

	body, _ := json.Marshal(map[string]string{"body": "Looks good!"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/"+approvalID.String()+"/comments", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "approvalId", Value: approvalID.String()}}
	c.Set("user", user)

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockComment.AssertExpectations(t)
}

func TestCommentHandler_List_Success(t *testing.T) {
	mockComment := new(mocks.MockCommentService)
	h := handlers.NewCommentHandler(mockComment)

	approvalID := uuid.New()
	comments := []models.ApprovalComment{
		{ID: uuid.New(), ApprovalID: approvalID, Body: "Comment 1"},
		{ID: uuid.New(), ApprovalID: approvalID, Body: "Comment 2"},
	}
	mockComment.On("ListByApprovalID", approvalID).Return(comments, nil)
	mockComment.On("CountByApprovalID", approvalID).Return(int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/"+approvalID.String()+"/comments", nil)
	c.Params = gin.Params{{Key: "approvalId", Value: approvalID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
	assert.Equal(t, float64(2), resp["total"])
	mockComment.AssertExpectations(t)
}

func TestCommentHandler_Create_NoUser(t *testing.T) {
	mockComment := new(mocks.MockCommentService)
	h := handlers.NewCommentHandler(mockComment)

	approvalID := uuid.New()
	body, _ := json.Marshal(map[string]string{"body": "test"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/"+approvalID.String()+"/comments", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "approvalId", Value: approvalID.String()}}
	// No user set

	h.Create(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCommentHandler_Create_InvalidApprovalID(t *testing.T) {
	mockComment := new(mocks.MockCommentService)
	h := handlers.NewCommentHandler(mockComment)

	user := testUser()
	body, _ := json.Marshal(map[string]string{"body": "test"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/bad-id/comments", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "approvalId", Value: "bad-id"}}
	c.Set("user", user)

	h.Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCommentHandler_Create_BadBody(t *testing.T) {
	mockComment := new(mocks.MockCommentService)
	h := handlers.NewCommentHandler(mockComment)

	user := testUser()
	approvalID := uuid.New()
	// Missing required body field
	body, _ := json.Marshal(map[string]string{})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/"+approvalID.String()+"/comments", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "approvalId", Value: approvalID.String()}}
	c.Set("user", user)

	h.Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCommentHandler_Create_ApprovalNotFound(t *testing.T) {
	mockComment := new(mocks.MockCommentService)
	h := handlers.NewCommentHandler(mockComment)

	user := testUser()
	approvalID := uuid.New()
	mockComment.On("Create", approvalID, mock.AnythingOfType("*models.User"), "test comment").Return(nil, errors.New("approval not found"))

	body, _ := json.Marshal(map[string]string{"body": "test comment"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/"+approvalID.String()+"/comments", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "approvalId", Value: approvalID.String()}}
	c.Set("user", user)

	h.Create(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockComment.AssertExpectations(t)
}

func TestCommentHandler_Create_ServiceError(t *testing.T) {
	mockComment := new(mocks.MockCommentService)
	h := handlers.NewCommentHandler(mockComment)

	user := testUser()
	approvalID := uuid.New()
	mockComment.On("Create", approvalID, mock.AnythingOfType("*models.User"), "test comment").Return(nil, errors.New("db error"))

	body, _ := json.Marshal(map[string]string{"body": "test comment"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/approvals/"+approvalID.String()+"/comments", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "approvalId", Value: approvalID.String()}}
	c.Set("user", user)

	h.Create(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockComment.AssertExpectations(t)
}

func TestCommentHandler_List_InvalidApprovalID(t *testing.T) {
	mockComment := new(mocks.MockCommentService)
	h := handlers.NewCommentHandler(mockComment)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/bad-id/comments", nil)
	c.Params = gin.Params{{Key: "approvalId", Value: "bad-id"}}

	h.List(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCommentHandler_List_ServiceError(t *testing.T) {
	mockComment := new(mocks.MockCommentService)
	h := handlers.NewCommentHandler(mockComment)

	approvalID := uuid.New()
	mockComment.On("ListByApprovalID", approvalID).Return([]models.ApprovalComment{}, errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/"+approvalID.String()+"/comments", nil)
	c.Params = gin.Params{{Key: "approvalId", Value: approvalID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockComment.AssertExpectations(t)
}
