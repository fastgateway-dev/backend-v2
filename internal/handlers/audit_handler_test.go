package handlers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestAuditHandler_List_Success(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	projectID := uuid.New()
	logs := []models.AuditLog{
		{ID: uuid.New(), Username: "admin", Action: "create", ResourceType: "route", CreatedAt: time.Now()},
		{ID: uuid.New(), Username: "admin", Action: "update", ResourceType: "domain", CreatedAt: time.Now()},
	}
	mockAudit.On("ListByProjectID", projectID, 1, 20, "", "", (*uuid.UUID)(nil)).Return(logs, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/audit-logs", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
	mockAudit.AssertExpectations(t)
}

func TestAuditHandler_Export_Success(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	projectID := uuid.New()
	logs := []models.AuditLog{
		{ID: uuid.New(), Username: "admin", Action: "create", ResourceType: "route", CreatedAt: time.Now()},
	}
	mockAudit.On("ExportByProjectID", projectID, "", "", (*uuid.UUID)(nil)).Return(logs, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/audit-logs/export", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.Export(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/csv")
	mockAudit.AssertExpectations(t)
}

func TestAuditHandler_List_InvalidProjectID(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/bad-id/audit-logs", nil)
	c.Params = gin.Params{{Key: "projectId", Value: "bad-id"}}

	h.List(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuditHandler_List_WithFilters(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	projectID := uuid.New()
	userID := uuid.New()
	logs := []models.AuditLog{
		{ID: uuid.New(), Username: "admin", Action: "create", ResourceType: "route", CreatedAt: time.Now()},
	}
	mockAudit.On("ListByProjectID", projectID, 2, 10, "route", "create", mock.AnythingOfType("*uuid.UUID")).Return(logs, int64(1), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/audit-logs?page=2&limit=10&resourceType=route&action=create&userId="+userID.String(), nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAudit.AssertExpectations(t)
}

func TestAuditHandler_List_ServiceError(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	projectID := uuid.New()
	mockAudit.On("ListByProjectID", projectID, 1, 20, "", "", (*uuid.UUID)(nil)).Return([]models.AuditLog{}, int64(0), errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/audit-logs", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAudit.AssertExpectations(t)
}

func TestAuditHandler_Cleanup_Success(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	projectID := uuid.New()
	mockAudit.On("CleanupOlderThan", projectID, 30).Return(int64(15), nil)

	body, _ := json.Marshal(map[string]int{"days": 30})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/projects/"+projectID.String()+"/audit-logs/cleanup", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.Cleanup(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	assert.Equal(t, float64(15), result["deleted"])
	mockAudit.AssertExpectations(t)
}

func TestAuditHandler_Cleanup_InvalidProjectID(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	body, _ := json.Marshal(map[string]int{"days": 30})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/projects/bad-id/audit-logs/cleanup", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "projectId", Value: "bad-id"}}

	h.Cleanup(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuditHandler_Cleanup_BadBody(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	projectID := uuid.New()

	// Missing required days field
	body, _ := json.Marshal(map[string]string{})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/projects/"+projectID.String()+"/audit-logs/cleanup", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.Cleanup(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuditHandler_Cleanup_DaysTooLow(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	projectID := uuid.New()

	body, _ := json.Marshal(map[string]int{"days": 0})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/projects/"+projectID.String()+"/audit-logs/cleanup", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.Cleanup(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuditHandler_Cleanup_ServiceError(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	projectID := uuid.New()
	mockAudit.On("CleanupOlderThan", projectID, 30).Return(int64(0), errors.New("db error"))

	body, _ := json.Marshal(map[string]int{"days": 30})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/projects/"+projectID.String()+"/audit-logs/cleanup", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.Cleanup(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAudit.AssertExpectations(t)
}

func TestAuditHandler_Export_InvalidProjectID(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/bad-id/audit-logs/export", nil)
	c.Params = gin.Params{{Key: "projectId", Value: "bad-id"}}

	h.Export(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuditHandler_Export_ServiceError(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	projectID := uuid.New()
	mockAudit.On("ExportByProjectID", projectID, "", "", (*uuid.UUID)(nil)).Return([]models.AuditLog{}, errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/audit-logs/export", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.Export(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAudit.AssertExpectations(t)
}

func TestAuditHandler_Export_WithFilters(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	projectID := uuid.New()
	userID := uuid.New()
	resourceID := uuid.New()
	logs := []models.AuditLog{
		{
			ID:           uuid.New(),
			Username:     "admin",
			Action:       "create",
			ResourceType: "route",
			ResourceName: "my-route",
			ResourceID:   &resourceID,
			IPAddress:    "10.0.0.1",
			CreatedAt:    time.Now(),
		},
	}
	mockAudit.On("ExportByProjectID", projectID, "route", "create", mock.AnythingOfType("*uuid.UUID")).Return(logs, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/audit-logs/export?resourceType=route&action=create&userId="+userID.String(), nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.Export(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "audit-log.csv")
	assert.Contains(t, w.Body.String(), "admin")
	assert.Contains(t, w.Body.String(), "create")
	assert.Contains(t, w.Body.String(), "route")
	assert.Contains(t, w.Body.String(), resourceID.String())
	mockAudit.AssertExpectations(t)
}

func TestAuditHandler_Export_EmptyLogs(t *testing.T) {
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewAuditHandler(mockAudit)

	projectID := uuid.New()
	mockAudit.On("ExportByProjectID", projectID, "", "", (*uuid.UUID)(nil)).Return([]models.AuditLog{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/audit-logs/export", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.Export(c)

	assert.Equal(t, http.StatusOK, w.Code)
	// Should still have CSV header
	assert.Contains(t, w.Body.String(), "Timestamp")
	mockAudit.AssertExpectations(t)
}
