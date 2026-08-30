package handlers_test

import (
	"bytes"
	"encoding/json"
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

func TestDomainTemplateHandler_List_Success(t *testing.T) {
	mockDT := new(mocks.MockDomainTemplateService)
	mockAudit := new(mocks.MockAuditService)
	mockDomainRepo := new(mocks.MockDomainRepository)
	h := handlers.NewDomainTemplateHandler(mockDT, mockAudit, mockDomainRepo)

	projectID := uuid.New()
	templates := []models.DomainTemplate{
		{ID: uuid.New(), ProjectID: projectID, Name: "template1"},
		{ID: uuid.New(), ProjectID: projectID, Name: "template2"},
	}
	mockDT.On("ListByProjectID", projectID, 1, 20).Return(templates, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/domain-templates", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
	mockDT.AssertExpectations(t)
}

func TestDomainTemplateHandler_Get_Success(t *testing.T) {
	mockDT := new(mocks.MockDomainTemplateService)
	mockAudit := new(mocks.MockAuditService)
	mockDomainRepo := new(mocks.MockDomainRepository)
	h := handlers.NewDomainTemplateHandler(mockDT, mockAudit, mockDomainRepo)

	dtID := uuid.New()
	dt := &models.DomainTemplate{ID: dtID, Name: "template1"}
	mockDT.On("GetByID", dtID).Return(dt, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domain-templates/"+dtID.String(), nil)
	c.Params = gin.Params{{Key: "domainTemplateId", Value: dtID.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDT.AssertExpectations(t)
}

func TestDomainTemplateHandler_Get_InvalidID(t *testing.T) {
	mockDT := new(mocks.MockDomainTemplateService)
	mockAudit := new(mocks.MockAuditService)
	mockDomainRepo := new(mocks.MockDomainRepository)
	h := handlers.NewDomainTemplateHandler(mockDT, mockAudit, mockDomainRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domain-templates/invalid", nil)
	c.Params = gin.Params{{Key: "domainTemplateId", Value: "invalid"}}

	h.Get(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainTemplateHandler_Create_Success(t *testing.T) {
	mockDT := new(mocks.MockDomainTemplateService)
	mockAudit := new(mocks.MockAuditService)
	mockDomainRepo := new(mocks.MockDomainRepository)
	h := handlers.NewDomainTemplateHandler(mockDT, mockAudit, mockDomainRepo)

	user := testUser()
	projectID := uuid.New()
	dt := &models.DomainTemplate{ID: uuid.New(), ProjectID: projectID, Name: "new-template"}
	mockDT.On("Create", projectID, mock.AnythingOfType("*services.CreateDomainTemplateInput"), user.ID).Return(dt, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{
		"name":         "new-template",
		"exposureType": "public",
		"tlsMode":      "tls_only",
	})
	router := gin.New()
	router.POST("/projects/:projectId/domain-templates", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domain-templates", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockDT.AssertExpectations(t)
}

func TestDomainTemplateHandler_Delete_Success(t *testing.T) {
	mockDT := new(mocks.MockDomainTemplateService)
	mockAudit := new(mocks.MockAuditService)
	mockDomainRepo := new(mocks.MockDomainRepository)
	h := handlers.NewDomainTemplateHandler(mockDT, mockAudit, mockDomainRepo)

	user := testUser()
	projectID := uuid.New()
	dtID := uuid.New()
	dt := &models.DomainTemplate{ID: dtID, Name: "to-delete"}
	mockDT.On("GetByID", dtID).Return(dt, nil)
	mockDT.On("Delete", dtID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/domain-templates/:domainTemplateId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/domain-templates/"+dtID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockDT.AssertExpectations(t)
}

func TestDomainTemplateHandler_Update_Success(t *testing.T) {
	mockDT := new(mocks.MockDomainTemplateService)
	mockAudit := new(mocks.MockAuditService)
	mockDomainRepo := new(mocks.MockDomainRepository)
	h := handlers.NewDomainTemplateHandler(mockDT, mockAudit, mockDomainRepo)

	user := testUser()
	projectID := uuid.New()
	dtID := uuid.New()
	dt := &models.DomainTemplate{ID: dtID, Name: "updated-template"}
	mockDT.On("Update", dtID, mock.AnythingOfType("*services.UpdateDomainTemplateInput")).Return(dt, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"description": "updated description"})

	router := gin.New()
	router.PUT("/projects/:projectId/domain-templates/:domainTemplateId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domain-templates/"+dtID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDT.AssertExpectations(t)
}

func TestDomainTemplateHandler_GetManifests_Success(t *testing.T) {
	mockDT := new(mocks.MockDomainTemplateService)
	mockAudit := new(mocks.MockAuditService)
	mockDomainRepo := new(mocks.MockDomainRepository)
	h := handlers.NewDomainTemplateHandler(mockDT, mockAudit, mockDomainRepo)

	dtID := uuid.New()
	manifests := &services.DomainTemplateManifests{
		GatewayClassYaml: "apiVersion: gateway.networking.k8s.io/v1",
		EnvoyProxyYaml:   "apiVersion: gateway.envoyproxy.io/v1alpha1",
	}
	mockDT.On("GetManifests", dtID).Return(manifests, nil)

	router := gin.New()
	router.GET("/domain-templates/:domainTemplateId/manifests", func(c *gin.Context) {
		h.GetManifests(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/domain-templates/"+dtID.String()+"/manifests", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDT.AssertExpectations(t)
}

func TestDomainTemplateHandler_GetManifests_InvalidID(t *testing.T) {
	mockDT := new(mocks.MockDomainTemplateService)
	mockAudit := new(mocks.MockAuditService)
	mockDomainRepo := new(mocks.MockDomainRepository)
	h := handlers.NewDomainTemplateHandler(mockDT, mockAudit, mockDomainRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/domain-templates/bad-id/manifests", nil)
	c.Params = gin.Params{{Key: "domainTemplateId", Value: "bad-id"}}

	h.GetManifests(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDomainTemplateHandler_PreviewChanges_Success(t *testing.T) {
	mockDT := new(mocks.MockDomainTemplateService)
	mockAudit := new(mocks.MockAuditService)
	mockDomainRepo := new(mocks.MockDomainRepository)
	h := handlers.NewDomainTemplateHandler(mockDT, mockAudit, mockDomainRepo)

	user := testUser()
	dtID := uuid.New()
	result := &services.DomainTemplatePreviewResult{
		CurrentEnvoyProxyYaml:  "current-yaml",
		ProposedEnvoyProxyYaml: "proposed-yaml",
	}
	mockDT.On("PreviewChanges", dtID, mock.AnythingOfType("*services.UpdateDomainTemplateInput"), user.ID, mock.AnythingOfType("*services.PreviewChangesOptions")).Return(result, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"description": "new description",
	})

	router := gin.New()
	router.POST("/domain-templates/:domainTemplateId/preview", func(c *gin.Context) {
		c.Set("user", user)
		h.PreviewChanges(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/domain-templates/"+dtID.String()+"/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDT.AssertExpectations(t)
}
