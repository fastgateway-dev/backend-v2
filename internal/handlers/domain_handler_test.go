package handlers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func domainPermChecker() *middleware.PermissionChecker {
	return middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
}

func TestDomainHandler_List_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	projectID := uuid.New()
	domains := []models.Domain{
		{ID: uuid.New(), Hostname: "example.com"},
		{ID: uuid.New(), Hostname: "api.example.com"},
	}
	mockDomain.On("ListByProjectID", projectID, 1, 20, "", "", map[string]string(nil)).Return(domains, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/domains", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_List_InvalidProjectID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/bad-id/domains", nil)
	c.Params = gin.Params{{Key: "projectId", Value: "bad-id"}}

	h.List(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_Get_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	domainID := uuid.New()
	domain := &models.Domain{ID: domainID, Hostname: "example.com"}
	mockDomain.On("GetByID", domainID).Return(domain, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domains/"+domainID.String(), nil)
	c.Params = gin.Params{{Key: "domainId", Value: domainID.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_Get_InvalidID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domains/bad-id", nil)
	c.Params = gin.Params{{Key: "domainId", Value: "bad-id"}}

	h.Get(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_Get_NotFound(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	domainID := uuid.New()
	mockDomain.On("GetByID", domainID).Return(nil, errors.New("not found"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domains/"+domainID.String(), nil)
	c.Params = gin.Params{{Key: "domainId", Value: domainID.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_Create_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domain := &models.Domain{ID: uuid.New(), Hostname: "new.example.com"}
	mockDomain.On("Create", projectID, mock.AnythingOfType("*services.CreateDomainInput"), user.ID).Return(domain, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{
		"name":             "new-domain",
		"hostname":         "new.example.com",
		"domainTemplateId": uuid.New().String(),
	})

	router := gin.New()
	router.POST("/projects/:projectId/domains", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_Update_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	domain := &models.Domain{ID: domainID, Hostname: "updated.example.com"}
	mockDomain.On("Update", domainID, mock.AnythingOfType("*services.UpdateDomainInput")).Return(domain, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"name": "updated-domain"})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_Delete_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	domain := &models.Domain{ID: domainID, Hostname: "to-delete.example.com"}
	mockDomain.On("GetByID", domainID).Return(domain, nil)
	mockDomain.On("Delete", domainID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/domains/"+domainID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_GetDomainSettings_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	domainID := uuid.New()
	settings := &models.DomainSettings{ID: uuid.New(), DomainID: domainID}
	mockDomain.On("GetDomainSettings", domainID).Return(settings, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domains/"+domainID.String()+"/settings", nil)
	c.Params = gin.Params{{Key: "domainId", Value: domainID.String()}}

	h.GetDomainSettings(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_UpdateDomainSettings_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	settings := &models.DomainSettings{ID: uuid.New(), DomainID: domainID}
	mockDomain.On("UpdateDomainSettings", domainID, mock.AnythingOfType("*services.UpdateDomainSettingsInput")).Return(settings, []string(nil), nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(services.UpdateDomainSettingsInput{})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/settings", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateDomainSettings(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_GetYAMLs_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	domainID := uuid.New()
	yamls := &services.DomainYAMLs{GatewayYaml: "apiVersion: v1"}
	mockDomain.On("GenerateYAMLs", domainID).Return(yamls, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domains/"+domainID.String()+"/yamls", nil)
	c.Params = gin.Params{{Key: "domainId", Value: domainID.String()}}

	h.GetYAMLs(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_GetYAMLs_InvalidID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domains/bad-id/yamls", nil)
	c.Params = gin.Params{{Key: "domainId", Value: "bad-id"}}

	h.GetYAMLs(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_List_ServiceError(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	projectID := uuid.New()
	mockDomain.On("ListByProjectID", projectID, 1, 20, "", "", map[string]string(nil)).Return([]models.Domain{}, int64(0), errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/domains", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_Create_InvalidProjectID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/domains", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/bad-id/domains", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_Create_ServiceError(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	mockDomain.On("Create", projectID, mock.AnythingOfType("*services.CreateDomainInput"), user.ID).Return(nil, errors.New("duplicate hostname"))

	body, _ := json.Marshal(map[string]string{
		"name":             "new-domain",
		"hostname":         "dup.example.com",
		"domainTemplateId": uuid.New().String(),
	})

	router := gin.New()
	router.POST("/projects/:projectId/domains", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_Update_InvalidProjectID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/bad-id/domains/"+uuid.New().String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_Update_InvalidDomainID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+uuid.New().String()+"/domains/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_Delete_InvalidProjectID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/bad-id/domains/"+uuid.New().String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_Delete_InvalidDomainID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+uuid.New().String()+"/domains/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_Delete_NotFound(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	mockDomain.On("GetByID", domainID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/domains/"+domainID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_GetDomainSettings_InvalidID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domains/bad-id/settings", nil)
	c.Params = gin.Params{{Key: "domainId", Value: "bad-id"}}

	h.GetDomainSettings(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_GetYAMLs_ServiceError(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	domainID := uuid.New()
	mockDomain.On("GenerateYAMLs", domainID).Return(nil, errors.New("domain not found"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domains/"+domainID.String()+"/yamls", nil)
	c.Params = gin.Params{{Key: "domainId", Value: domainID.String()}}

	h.GetYAMLs(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_PreviewCreate_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	user := testUser()
	projectID := uuid.New()
	result := &services.DomainCreatePreviewResult{ProposedGatewayYaml: "apiVersion: v1"}
	mockDomain.On("PreviewCreate", projectID, mock.AnythingOfType("*services.DomainCreatePreviewInput"), user.ID).Return(result, nil)

	body, _ := json.Marshal(map[string]string{
		"name":             "preview-domain",
		"hostname":         "preview.example.com",
		"domainTemplateId": uuid.New().String(),
	})

	router := gin.New()
	router.POST("/projects/:projectId/domains/preview", func(c *gin.Context) {
		c.Set("user", user)
		h.PreviewCreate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_PreviewCreate_InvalidProjectID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/domains/preview", func(c *gin.Context) {
		c.Set("user", user)
		h.PreviewCreate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/bad-id/domains/preview", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_PreviewSettingsChanges_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	user := testUser()
	domainID := uuid.New()
	result := &services.DomainSettingsPreviewResult{CurrentGatewayYaml: "old"}
	mockDomain.On("PreviewSettingsChanges", domainID, mock.AnythingOfType("*services.DomainSettingsPreviewInput"), user.ID).Return(result, nil)

	body, _ := json.Marshal(services.DomainSettingsPreviewInput{})

	router := gin.New()
	router.POST("/domains/:domainId/settings/preview", func(c *gin.Context) {
		c.Set("user", user)
		h.PreviewSettingsChanges(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/domains/"+domainID.String()+"/settings/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_PreviewSettingsChanges_InvalidDomainID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	user := testUser()

	router := gin.New()
	router.POST("/domains/:domainId/settings/preview", func(c *gin.Context) {
		c.Set("user", user)
		h.PreviewSettingsChanges(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/domains/bad-id/settings/preview", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_Update_ServiceError(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	mockDomain.On("Update", domainID, mock.AnythingOfType("*services.UpdateDomainInput")).Return(nil, errors.New("validation error"))

	body, _ := json.Marshal(map[string]string{"name": "bad-domain"})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockDomain.AssertExpectations(t)
}

// TestDomainHandler_Update_GatewayApplyError pins the Phase 3b fix: when
// domainService.Update fails because applying the Gateway change to the
// cluster failed (services.ErrGatewayApply), that is a server-side/infra
// failure -- not a client input error -- so it must map to 500, not 400.
func TestDomainHandler_Update_GatewayApplyError(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	wrapped := fmt.Errorf("%w: %v", services.ErrGatewayApply, errors.New("cluster unreachable"))
	mockDomain.On("Update", domainID, mock.AnythingOfType("*services.UpdateDomainInput")).Return(nil, wrapped)

	body, _ := json.Marshal(map[string]string{"name": "some-domain"})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_Delete_ServiceError(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	domain := &models.Domain{ID: domainID, Hostname: "example.com"}
	mockDomain.On("GetByID", domainID).Return(domain, nil)
	mockDomain.On("Delete", domainID).Return(errors.New("has routes"))

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/domains/"+domainID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_GetDomainSettings_ServiceError(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	domainID := uuid.New()
	mockDomain.On("GetDomainSettings", domainID).Return(nil, errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domains/"+domainID.String()+"/settings", nil)
	c.Params = gin.Params{{Key: "domainId", Value: domainID.String()}}

	h.GetDomainSettings(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_UpdateDomainSettings_InvalidProjectID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/settings", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateDomainSettings(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/bad-id/domains/"+uuid.New().String()+"/settings", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_UpdateDomainSettings_InvalidDomainID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/settings", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateDomainSettings(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+uuid.New().String()+"/domains/bad-id/settings", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_UpdateDomainSettings_ServiceError(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	mockDomain.On("UpdateDomainSettings", domainID, mock.AnythingOfType("*services.UpdateDomainSettingsInput")).Return(nil, []string(nil), errors.New("invalid settings"))

	body, _ := json.Marshal(services.UpdateDomainSettingsInput{})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/settings", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateDomainSettings(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_AddDomainMTLSCA_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	settings := &models.DomainSettings{ID: uuid.New(), DomainID: domainID}
	mockDomain.On("AddDomainMTLSCA", mock.Anything, domainID, mock.AnythingOfType("*services.AddDomainMTLSCAInput")).Return(settings, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(services.AddDomainMTLSCAInput{
		Name:  "test-ca",
		CAPem: "-----BEGIN CERTIFICATE-----\nMIIBPQ...\n-----END CERTIFICATE-----",
	})

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/settings/mtls/ca", func(c *gin.Context) {
		c.Set("user", user)
		h.AddDomainMTLSCA(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/settings/mtls/ca", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_AddDomainMTLSCA_InvalidProjectID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/settings/mtls/ca", func(c *gin.Context) {
		c.Set("user", user)
		h.AddDomainMTLSCA(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/bad-id/domains/"+uuid.New().String()+"/settings/mtls/ca", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_AddDomainMTLSCA_InvalidDomainID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/settings/mtls/ca", func(c *gin.Context) {
		c.Set("user", user)
		h.AddDomainMTLSCA(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+uuid.New().String()+"/domains/bad-id/settings/mtls/ca", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_RemoveDomainMTLSCA_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	settings := &models.DomainSettings{ID: uuid.New(), DomainID: domainID}
	mockDomain.On("RemoveDomainMTLSCA", mock.Anything, domainID, "ca-123").Return(settings, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId/settings/mtls/ca/:caId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveDomainMTLSCA(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/settings/mtls/ca/ca-123", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_RemoveDomainMTLSCA_InvalidProjectID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId/settings/mtls/ca/:caId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveDomainMTLSCA(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/bad-id/domains/"+uuid.New().String()+"/settings/mtls/ca/ca-123", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_RemoveDomainMTLSCA_InvalidDomainID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId/settings/mtls/ca/:caId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveDomainMTLSCA(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+uuid.New().String()+"/domains/bad-id/settings/mtls/ca/ca-123", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_PreviewCreate_ServiceError(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	user := testUser()
	projectID := uuid.New()
	mockDomain.On("PreviewCreate", projectID, mock.AnythingOfType("*services.DomainCreatePreviewInput"), user.ID).Return(nil, errors.New("preview failed"))

	body, _ := json.Marshal(map[string]string{
		"name":             "preview-domain",
		"hostname":         "preview.example.com",
		"domainTemplateId": uuid.New().String(),
	})

	router := gin.New()
	router.POST("/projects/:projectId/domains/preview", func(c *gin.Context) {
		c.Set("user", user)
		h.PreviewCreate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_PreviewSettingsChanges_ServiceError(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, nil, nil)

	user := testUser()
	domainID := uuid.New()
	mockDomain.On("PreviewSettingsChanges", domainID, mock.AnythingOfType("*services.DomainSettingsPreviewInput"), user.ID).Return(nil, errors.New("preview failed"))

	body, _ := json.Marshal(services.DomainSettingsPreviewInput{})

	router := gin.New()
	router.POST("/domains/:domainId/settings/preview", func(c *gin.Context) {
		c.Set("user", user)
		h.PreviewSettingsChanges(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/domains/"+domainID.String()+"/settings/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockDomain.AssertExpectations(t)
}

// --- AttachCertificate / DetachCertificate ---

func TestDomainHandler_AttachCertificate_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	certID := uuid.New()
	domain := &models.Domain{ID: domainID, Hostname: "example.com", ManagedCertificateID: &certID}
	mockDomain.On("AttachCertificate", domainID, certID, projectID).Return(domain, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, "attach_certificate", "domain", &domainID, "example.com", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"certificateId": certID.String()})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/certificate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.Domain
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.ManagedCertificateID)
	assert.Equal(t, certID, *resp.ManagedCertificateID)
	mockDomain.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

func TestDomainHandler_AttachCertificate_DomainNotFound(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	certID := uuid.New()
	mockDomain.On("AttachCertificate", domainID, certID, projectID).Return(nil, services.ErrDomainNotFound)

	body, _ := json.Marshal(map[string]string{"certificateId": certID.String()})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/certificate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockDomain.AssertExpectations(t)
	mockAudit.AssertNotCalled(t, "LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestDomainHandler_AttachCertificate_CertificateNotFound(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	certID := uuid.New()
	mockDomain.On("AttachCertificate", domainID, certID, projectID).Return(nil, services.ErrCertificateNotFound)

	body, _ := json.Marshal(map[string]string{"certificateId": certID.String()})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/certificate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Certificate not found", resp["error"])
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_AttachCertificate_WrongUsage(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	certID := uuid.New()
	mockDomain.On("AttachCertificate", domainID, certID, projectID).Return(nil, services.ErrCertificateWrongUsage)

	body, _ := json.Marshal(map[string]string{"certificateId": certID.String()})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/certificate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	mockDomain.AssertExpectations(t)
}

func TestDomainHandler_AttachCertificate_NotReady(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	certID := uuid.New()
	mockDomain.On("AttachCertificate", domainID, certID, projectID).Return(nil, services.ErrCertificateNotReady)

	body, _ := json.Marshal(map[string]string{"certificateId": certID.String()})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/certificate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	mockDomain.AssertExpectations(t)
}

// TestDomainHandler_AttachCertificate_GenericError exercises the error-shape
// gotcha: AttachCertificate can return a non-nil domain alongside a non-nil
// error (the applyGateway failure path in DomainService.AttachCertificate).
// The handler must check err first and never serialize that domain.
func TestDomainHandler_AttachCertificate_GenericError(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	certID := uuid.New()
	domainWithLeakedSecret := &models.Domain{ID: domainID, Hostname: "leaked.example.com"}
	mockDomain.On("AttachCertificate", domainID, certID, projectID).Return(domainWithLeakedSecret, errors.New("apply gateway failed"))

	body, _ := json.Marshal(map[string]string{"certificateId": certID.String()})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/certificate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "leaked.example.com")
	mockDomain.AssertExpectations(t)
	mockAudit.AssertNotCalled(t, "LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestDomainHandler_AttachCertificate_Forbidden(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	mockTeam := new(mocks.MockTeamRepository)
	mockProject := new(mocks.MockProjectRepository)
	pc := middleware.NewPermissionChecker(mockProject, mockTeam)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	nonAdmin := &models.User{ID: uuid.New(), Username: "dev1", Role: models.UserRoleUser, IsActive: true}
	projectID := uuid.New()
	domainID := uuid.New()
	certID := uuid.New()
	mockProject.On("IsAdmin", projectID, nonAdmin.ID).Return(false, nil)
	mockTeam.On("HasPermissionInProject", projectID, nonAdmin.ID, models.PermDomainDelete).Return(false, nil)

	body, _ := json.Marshal(map[string]string{"certificateId": certID.String()})

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", nonAdmin)
		h.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/certificate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockDomain.AssertNotCalled(t, "AttachCertificate", mock.Anything, mock.Anything, mock.Anything)
	mockProject.AssertExpectations(t)
	mockTeam.AssertExpectations(t)
}

func TestDomainHandler_AttachCertificate_InvalidBody(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/certificate", bytes.NewReader([]byte(`{"certificateId":`)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockDomain.AssertNotCalled(t, "AttachCertificate", mock.Anything, mock.Anything, mock.Anything)
}

func TestDomainHandler_AttachCertificate_MissingCertificateID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/certificate", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockDomain.AssertNotCalled(t, "AttachCertificate", mock.Anything, mock.Anything, mock.Anything)
}

func TestDomainHandler_AttachCertificate_InvalidProjectID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/bad-id/domains/"+uuid.New().String()+"/certificate", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_AttachCertificate_InvalidDomainID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/bad-id/certificate", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_DetachCertificate_Success(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	domain := &models.Domain{ID: domainID, Hostname: "example.com"}
	mockDomain.On("DetachCertificate", domainID, projectID).Return(domain, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, "detach_certificate", "domain", &domainID, "example.com", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.DetachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/certificate", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.Domain
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Nil(t, resp.ManagedCertificateID)
	mockDomain.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

func TestDomainHandler_DetachCertificate_DomainNotFound(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	mockDomain.On("DetachCertificate", domainID, projectID).Return(nil, services.ErrDomainNotFound)

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.DetachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/certificate", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockDomain.AssertExpectations(t)
	mockAudit.AssertNotCalled(t, "LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestDomainHandler_DetachCertificate_InvalidProjectID(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	pc := domainPermChecker()
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	user := testUser()

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		h.DetachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/bad-id/domains/"+uuid.New().String()+"/certificate", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainHandler_DetachCertificate_Forbidden(t *testing.T) {
	mockDomain := new(mocks.MockDomainService)
	mockAudit := new(mocks.MockAuditService)
	mockTeam := new(mocks.MockTeamRepository)
	mockProject := new(mocks.MockProjectRepository)
	pc := middleware.NewPermissionChecker(mockProject, mockTeam)
	h := handlers.NewDomainHandler(mockDomain, mockAudit, pc, nil)

	nonAdmin := &models.User{ID: uuid.New(), Username: "dev1", Role: models.UserRoleUser, IsActive: true}
	projectID := uuid.New()
	domainID := uuid.New()
	mockProject.On("IsAdmin", projectID, nonAdmin.ID).Return(false, nil)
	mockTeam.On("HasPermissionInProject", projectID, nonAdmin.ID, models.PermDomainDelete).Return(false, nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId/certificate", func(c *gin.Context) {
		c.Set("user", nonAdmin)
		h.DetachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/certificate", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockDomain.AssertNotCalled(t, "DetachCertificate", mock.Anything, mock.Anything)
	mockProject.AssertExpectations(t)
	mockTeam.AssertExpectations(t)
}
