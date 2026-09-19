package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

func TestManagedCertificateHandler_Create_DeniedWithoutPermission(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	mockProject := new(mocks.MockProjectRepository)
	mockTeam := new(mocks.MockTeamRepository)
	pc := middleware.NewPermissionChecker(mockProject, mockTeam)
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := &models.User{ID: uuid.New(), Username: "dev1", Role: models.UserRoleUser, IsActive: true}
	projectID := uuid.New()

	// Not owner, not project admin, no team-granted certificate.create perm.
	mockProject.On("IsAdmin", projectID, user.ID).Return(false, nil)
	mockTeam.On("HasPermissionInProject", projectID, user.ID, models.PermCertificateCreate).Return(false, nil)

	router := gin.New()
	router.POST("/projects/:projectId/certificates", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	body, _ := json.Marshal(services.CreateCertificateInput{
		Name:     "example",
		IssuerID: uuid.New(),
		Usage:    models.ManagedCertUsageServer,
		DNSNames: []string{"example.com"},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/certificates", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockCert.AssertNotCalled(t, "Create")
	mockProject.AssertExpectations(t)
	mockTeam.AssertExpectations(t)
}

func TestManagedCertificateHandler_Create_Success_FastPath_NoApproval(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser() // Owner role bypasses permission checks
	projectID := uuid.New()
	certID := uuid.New()

	input := services.CreateCertificateInput{
		Name:     "example",
		IssuerID: uuid.New(),
		Usage:    models.ManagedCertUsageServer,
		DNSNames: []string{"example.com"},
	}

	created := &models.ManagedCertificate{
		ID:        certID,
		ProjectID: projectID,
		Name:      input.Name,
		IssuerID:  input.IssuerID,
		Usage:     input.Usage,
		Status:    models.ManagedCertStatusIssuing,
		Config:    models.ManagedCertConfig{DNSNames: input.DNSNames},
	}

	mockCert.On("Create", projectID, mock.AnythingOfType("*services.CreateCertificateInput"), user.ID).Return(created, (*models.Approval)(nil), nil)
	mockAudit.On("LogAction", &projectID, user, "create", "certificate", &certID, created.Name, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/projects/:projectId/certificates", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	body, _ := json.Marshal(input)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/certificates", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))
	require.Nil(resp["approvalId"])

	certBody, _ := json.Marshal(resp["certificate"])
	var certResp map[string]interface{}
	require.NoError(json.Unmarshal(certBody, &certResp))
	require.Equal(certID.String(), certResp["id"])
	require.NotContains(certResp, "config")
	require.NotContains(certResp, "createdBy")
	require.NotContains(w.Body.String(), "secretName")
	require.NotContains(w.Body.String(), "certificateName")

	mockCert.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

func TestManagedCertificateHandler_Create_Success_PendingApproval(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	certID := uuid.New()
	approvalID := uuid.New()

	input := services.CreateCertificateInput{
		Name:     "example",
		IssuerID: uuid.New(),
		Usage:    models.ManagedCertUsageServer,
		DNSNames: []string{"example.com"},
	}

	created := &models.ManagedCertificate{
		ID:        certID,
		ProjectID: projectID,
		Name:      input.Name,
		IssuerID:  input.IssuerID,
		Usage:     input.Usage,
		Status:    models.ManagedCertStatusPending,
		Config:    models.ManagedCertConfig{DNSNames: input.DNSNames},
	}
	approval := &models.Approval{ID: approvalID}

	mockCert.On("Create", projectID, mock.AnythingOfType("*services.CreateCertificateInput"), user.ID).Return(created, approval, nil)
	mockAudit.On("LogAction", &projectID, user, "create", "certificate", &certID, created.Name, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/projects/:projectId/certificates", func(c *gin.Context) {
		c.Set("user", user)
		h.Create(c)
	})

	body, _ := json.Marshal(input)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/certificates", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)

	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(approvalID.String(), resp["approvalId"])

	mockCert.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

func TestManagedCertificateHandler_List_Success(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	projectID := uuid.New()
	certs := []models.ManagedCertificate{
		{ID: uuid.New(), ProjectID: projectID, Name: "cert1"},
		{ID: uuid.New(), ProjectID: projectID, Name: "cert2"},
	}
	mockCert.On("ListByProject", projectID, 1, 20, "").Return(certs, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/certificates", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].([]interface{})
	require.Len(data, 2)
	require.NotNil(resp["pagination"])
	mockCert.AssertExpectations(t)
}

func TestManagedCertificateHandler_Get_ProjectMismatch_NotFound(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	urlProjectID := uuid.New()
	otherProjectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: otherProjectID, Name: "other-project-cert"}

	mockCert.On("GetByID", certID).Return(cert, nil)

	router := gin.New()
	router.GET("/projects/:projectId/certificates/:certificateId", h.Get)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+urlProjectID.String()+"/certificates/"+certID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockCert.AssertExpectations(t)
}

func TestManagedCertificateHandler_Delete_ProjectMismatch_NotFound(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser() // Owner role bypasses permission checks, isolating the project-mismatch check
	urlProjectID := uuid.New()
	otherProjectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: otherProjectID, Name: "other-project-cert"}

	mockCert.On("GetByID", certID).Return(cert, nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/certificates/:certificateId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+urlProjectID.String()+"/certificates/"+certID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockCert.AssertNotCalled(t, "Delete")
	mockCert.AssertExpectations(t)
}

func TestManagedCertificateHandler_Status_ProjectMismatch_NotFound(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	urlProjectID := uuid.New()
	otherProjectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: otherProjectID, Name: "other-project-cert"}

	mockCert.On("GetByID", certID).Return(cert, nil)

	router := gin.New()
	router.GET("/projects/:projectId/certificates/:certificateId/status", h.Status)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+urlProjectID.String()+"/certificates/"+certID.String()+"/status", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockCert.AssertNotCalled(t, "Status")
	mockCert.AssertExpectations(t)
}

func TestManagedCertificateHandler_Delete_DeniedWithoutPermission(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	mockProject := new(mocks.MockProjectRepository)
	mockTeam := new(mocks.MockTeamRepository)
	pc := middleware.NewPermissionChecker(mockProject, mockTeam)
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := &models.User{ID: uuid.New(), Username: "dev1", Role: models.UserRoleUser, IsActive: true}
	projectID := uuid.New()
	certID := uuid.New()

	mockProject.On("IsAdmin", projectID, user.ID).Return(false, nil)
	mockTeam.On("HasPermissionInProject", projectID, user.ID, models.PermCertificateDelete).Return(false, nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/certificates/:certificateId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/certificates/"+certID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockCert.AssertNotCalled(t, "Delete")
}
