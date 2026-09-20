package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
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

	user := testUser() // Owner role bypasses the certificate.view permission check
	projectID := uuid.New()
	issuerID := uuid.New()
	certAID := uuid.New()
	certBID := uuid.New()
	domainID := uuid.New()

	enriched := []services.EnrichedCertificate{
		{
			Certificate: models.ManagedCertificate{ID: certAID, ProjectID: projectID, Name: "cert1", IssuerID: issuerID},
			IssuerName:  "Issuer A",
			IssuerType:  "self_signed_ca",
			Distribution: &models.CertificateDistribution{
				ManagedCertificateID: certAID,
				Status:               models.CertDistStatusSynced,
			},
			Domains:         []models.Domain{{ID: domainID, Hostname: "a.example.com"}},
			ExportAvailable: true,
		},
		{
			Certificate: models.ManagedCertificate{ID: certBID, ProjectID: projectID, Name: "cert2", IssuerID: issuerID},
			IssuerName:  "Issuer A",
			IssuerType:  "self_signed_ca",
		},
	}
	mockCert.On("ListProjectCertificatesEnriched", projectID, 1, 20, repository.CertificateListFilter{}, user.ID).Return(enriched, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", user)
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

	first := data[0].(map[string]interface{})
	assert.Equal(t, "Issuer A", first["issuerName"])
	assert.Equal(t, "self_signed_ca", first["issuerType"])
	dist := first["distribution"].(map[string]interface{})
	assert.Equal(t, string(models.CertDistStatusSynced), dist["status"])
	domains := first["domains"].([]interface{})
	require.Len(domains, 1)
	firstDomain := domains[0].(map[string]interface{})
	assert.Equal(t, "a.example.com", firstDomain["hostname"])
	assert.Equal(t, domainID.String(), firstDomain["id"])
	// No secret material and no other domain fields leaked.
	assert.Len(t, firstDomain, 2)
	assert.Equal(t, true, first["exportAvailable"])

	second := data[1].(map[string]interface{})
	assert.Nil(t, second["distribution"])
	assert.Empty(t, second["domains"])
	assert.Equal(t, false, second["exportAvailable"])

	mockCert.AssertExpectations(t)
}

func TestManagedCertificateHandler_List_DeniedWithoutCertificateView(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	mockProject := new(mocks.MockProjectRepository)
	mockTeam := new(mocks.MockTeamRepository)
	pc := middleware.NewPermissionChecker(mockProject, mockTeam)
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := &models.User{ID: uuid.New(), Username: "dev1", Role: models.UserRoleUser, IsActive: true}
	projectID := uuid.New()

	mockProject.On("IsAdmin", projectID, user.ID).Return(false, nil)
	mockTeam.On("HasPermissionInProject", projectID, user.ID, models.PermCertificateView).Return(false, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", user)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/certificates", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockCert.AssertNotCalled(t, "ListProjectCertificatesEnriched", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestManagedCertificateHandler_List_MalformedExpiresBefore_BadRequest(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser() // Owner role bypasses the certificate.view check, isolating filter parsing
	projectID := uuid.New()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", user)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/certificates?expiresBefore=not-a-date", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockCert.AssertNotCalled(t, "ListProjectCertificatesEnriched", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestManagedCertificateHandler_List_FiltersPassedThrough(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	issuerID := uuid.New()
	expiresBefore, err := time.Parse(time.RFC3339, "2026-12-31T00:00:00Z")
	require := assert.New(t)
	require.NoError(err)

	expected := repository.CertificateListFilter{
		Status:        "ready",
		IssuerID:      &issuerID,
		Usage:         "server",
		ExpiresBefore: &expiresBefore,
	}
	mockCert.On("ListProjectCertificatesEnriched", projectID, 1, 20, expected, user.ID).Return([]services.EnrichedCertificate{}, int64(0), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", user)
	url := "/projects/" + projectID.String() + "/certificates?status=ready&issuerId=" + issuerID.String() + "&usage=server&expiresBefore=2026-12-31T00:00:00Z"
	c.Request, _ = http.NewRequest("GET", url, nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
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

// TestManagedCertificateHandler_Delete_InUse_Conflict verifies the handler
// maps services.ErrCertificateInUse (the referential guard in
// ManagedCertificateService.Delete) to 409, distinct from the generic 500
// path for other errors.
func TestManagedCertificateHandler_Delete_InUse_Conflict(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser() // Owner role bypasses permission checks, isolating the Delete-error handling
	projectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Name: "in-use-cert"}

	mockCert.On("GetByID", certID).Return(cert, nil)
	mockCert.On("Delete", certID).Return(services.ErrCertificateInUse)

	router := gin.New()
	router.DELETE("/projects/:projectId/certificates/:certificateId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/certificates/"+certID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	mockCert.AssertExpectations(t)
	mockAudit.AssertNotCalled(t, "LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
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

func TestManagedCertificateHandler_Distribution_Success(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	projectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Name: "example"}
	dist := &models.CertificateDistribution{
		ManagedCertificateID:  certID,
		Status:                models.CertDistStatusSynced,
		LastPushedFingerprint: "sha256:abc",
	}

	mockCert.On("GetByID", certID).Return(cert, nil)
	mockCert.On("DistributionStatus", certID).Return(dist, nil)

	router := gin.New()
	router.GET("/projects/:projectId/certificates/:certificateId/distribution", h.Distribution)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/distribution", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(string(models.CertDistStatusSynced), resp["status"])
	require.Equal("sha256:abc", resp["lastPushedFingerprint"])
	require.NotContains(w.Body.String(), "secretName")
	mockCert.AssertExpectations(t)
}

func TestManagedCertificateHandler_Distribution_ProjectMismatch_NotFound(t *testing.T) {
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
	router.GET("/projects/:projectId/certificates/:certificateId/distribution", h.Distribution)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+urlProjectID.String()+"/certificates/"+certID.String()+"/distribution", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockCert.AssertNotCalled(t, "DistributionStatus")
	mockCert.AssertExpectations(t)
}

func TestManagedCertificateHandler_Distribution_CertNotFound(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	projectID := uuid.New()
	certID := uuid.New()

	mockCert.On("GetByID", certID).Return(nil, gorm.ErrRecordNotFound)

	router := gin.New()
	router.GET("/projects/:projectId/certificates/:certificateId/distribution", h.Distribution)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/distribution", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockCert.AssertNotCalled(t, "DistributionStatus")
	mockCert.AssertExpectations(t)
}

func TestManagedCertificateHandler_Resync_Success_Returns202(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser() // Owner role bypasses permission checks
	projectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Name: "example"}

	mockCert.On("GetByID", certID).Return(cert, nil)
	mockCert.On("Resync", certID).Return(nil)
	mockAudit.On("LogAction", &projectID, user, "resync", "certificate", &certID, cert.Name, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/projects/:projectId/certificates/:certificateId/resync", func(c *gin.Context) {
		c.Set("user", user)
		h.Resync(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/resync", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	mockCert.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

func TestManagedCertificateHandler_Resync_ProjectMismatch_NotFound(t *testing.T) {
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
	router.POST("/projects/:projectId/certificates/:certificateId/resync", func(c *gin.Context) {
		c.Set("user", user)
		h.Resync(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+urlProjectID.String()+"/certificates/"+certID.String()+"/resync", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockCert.AssertNotCalled(t, "Resync")
	mockCert.AssertExpectations(t)
}

func TestManagedCertificateHandler_Resync_DeniedWithoutPermission(t *testing.T) {
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
	mockTeam.On("HasPermissionInProject", projectID, user.ID, models.PermCertificateEdit).Return(false, nil)

	router := gin.New()
	router.POST("/projects/:projectId/certificates/:certificateId/resync", func(c *gin.Context) {
		c.Set("user", user)
		h.Resync(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/resync", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockCert.AssertNotCalled(t, "GetByID")
	mockCert.AssertNotCalled(t, "Resync")
}

func TestManagedCertificateHandler_Resync_AllowedWithEditPermission(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	mockProject := new(mocks.MockProjectRepository)
	mockTeam := new(mocks.MockTeamRepository)
	pc := middleware.NewPermissionChecker(mockProject, mockTeam)
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	// A non-owner, non-project-admin user with team-granted
	// certificate.edit is sufficient for Resync -- it is strictly less
	// dangerous than Delete (certificate.delete) and must not require
	// delete-level access.
	user := &models.User{ID: uuid.New(), Username: "editor1", Role: models.UserRoleUser, IsActive: true}
	projectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Name: "example"}

	mockProject.On("IsAdmin", projectID, user.ID).Return(false, nil)
	mockTeam.On("HasPermissionInProject", projectID, user.ID, models.PermCertificateEdit).Return(true, nil)
	mockCert.On("GetByID", certID).Return(cert, nil)
	mockCert.On("Resync", certID).Return(nil)
	mockAudit.On("LogAction", &projectID, user, "resync", "certificate", &certID, cert.Name, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/projects/:projectId/certificates/:certificateId/resync", func(c *gin.Context) {
		c.Set("user", user)
		h.Resync(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/resync", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	mockCert.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

// --- ListFleet tests ---
//
// ListFleet has no per-handler permission check -- the owner gate is
// enforced by the RequireRole("owner") route middleware in main.go, not by
// the handler -- so these tests exercise the handler directly (no
// permission-checker wiring needed) and focus on filter parsing and the
// enriched cross-project response shape.

func TestManagedCertificateHandler_ListFleet_Success_AcrossProjects(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser()
	projectA := uuid.New()
	projectB := uuid.New()
	issuerID := uuid.New()
	certAID := uuid.New()
	certBID := uuid.New()

	enriched := []services.EnrichedCertificate{
		{
			Certificate: models.ManagedCertificate{ID: certAID, ProjectID: projectA, Name: "cert1", IssuerID: issuerID},
			IssuerName:  "Issuer A",
			IssuerType:  "self_signed_ca",
		},
		{
			Certificate: models.ManagedCertificate{ID: certBID, ProjectID: projectB, Name: "cert2", IssuerID: issuerID},
			IssuerName:  "Issuer A",
			IssuerType:  "self_signed_ca",
		},
	}
	mockCert.On("ListFleetCertificates", 1, 20, repository.CertificateListFilter{}, user.ID).Return(enriched, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", user)
	c.Request, _ = http.NewRequest("GET", "/certificates", nil)

	h.ListFleet(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))
	data := resp["data"].([]interface{})
	require.Len(data, 2)
	require.NotNil(resp["pagination"])

	first := data[0].(map[string]interface{})
	assert.Equal(t, projectA.String(), first["projectId"])
	second := data[1].(map[string]interface{})
	assert.Equal(t, projectB.String(), second["projectId"])

	mockCert.AssertExpectations(t)
}

func TestManagedCertificateHandler_ListFleet_MalformedExpiresBefore_BadRequest(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/certificates?expiresBefore=not-a-date", nil)

	h.ListFleet(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockCert.AssertNotCalled(t, "ListFleetCertificates", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestManagedCertificateHandler_ListFleet_MalformedProjectID_BadRequest(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/certificates?projectId=not-a-uuid", nil)

	h.ListFleet(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockCert.AssertNotCalled(t, "ListFleetCertificates", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// --- RequestExport / ExportDownload tests (Task 7) ---

func TestManagedCertificateHandler_RequestExport_Success_Returns202(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser() // Owner role bypasses permission checks
	projectID := uuid.New()
	certID := uuid.New()
	approvalID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Name: "example"}
	approval := &models.Approval{ID: approvalID}

	mockCert.On("GetByID", certID).Return(cert, nil)
	mockCert.On("RequestExport", certID, user.ID).Return(approval, nil)
	mockAudit.On("LogAction", &projectID, user, "export_request", "certificate", &certID, cert.Name, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/projects/:projectId/certificates/:certificateId/export", func(c *gin.Context) {
		c.Set("user", user)
		h.RequestExport(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/export", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(approvalID.String(), resp["approvalId"])
	mockCert.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

// TestManagedCertificateHandler_RequestExport_FastPath_NilApproval verifies
// the response tolerates RequestExport returning a nil approval (the
// service's fast path when the project has approvals disabled) -- the
// response must still be 202 with a null approvalId, not a crash.
func TestManagedCertificateHandler_RequestExport_FastPath_NilApproval(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Name: "example"}

	mockCert.On("GetByID", certID).Return(cert, nil)
	mockCert.On("RequestExport", certID, user.ID).Return((*models.Approval)(nil), nil)
	mockAudit.On("LogAction", &projectID, user, "export_request", "certificate", &certID, cert.Name, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/projects/:projectId/certificates/:certificateId/export", func(c *gin.Context) {
		c.Set("user", user)
		h.RequestExport(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/export", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))
	require.Nil(resp["approvalId"])
	mockCert.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

// TestManagedCertificateHandler_RequestExport_CSRMode_Conflict verifies
// services.ErrExportNotApplicable maps to 409, distinct from the generic
// 500 path -- a csr-mode certificate can never be exported.
func TestManagedCertificateHandler_RequestExport_CSRMode_Conflict(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Name: "csr-cert"}

	mockCert.On("GetByID", certID).Return(cert, nil)
	mockCert.On("RequestExport", certID, user.ID).Return((*models.Approval)(nil), services.ErrExportNotApplicable)

	router := gin.New()
	router.POST("/projects/:projectId/certificates/:certificateId/export", func(c *gin.Context) {
		c.Set("user", user)
		h.RequestExport(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/export", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	mockCert.AssertExpectations(t)
	mockAudit.AssertNotCalled(t, "LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestManagedCertificateHandler_RequestExport_NotReady_UnprocessableEntity
// verifies services.ErrCertificateNotReadyForExport maps to 422, distinct
// from the csr-mode 409 above -- this is a semantic precondition failure
// (the cert just hasn't finished issuing yet), consistent with how the
// attach-not-ready case is mapped elsewhere.
func TestManagedCertificateHandler_RequestExport_NotReady_UnprocessableEntity(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Name: "issuing-cert"}

	mockCert.On("GetByID", certID).Return(cert, nil)
	mockCert.On("RequestExport", certID, user.ID).Return((*models.Approval)(nil), services.ErrCertificateNotReadyForExport)

	router := gin.New()
	router.POST("/projects/:projectId/certificates/:certificateId/export", func(c *gin.Context) {
		c.Set("user", user)
		h.RequestExport(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/export", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	mockCert.AssertExpectations(t)
	mockAudit.AssertNotCalled(t, "LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestManagedCertificateHandler_RequestExport_ProjectMismatch_NotFound(t *testing.T) {
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
	router.POST("/projects/:projectId/certificates/:certificateId/export", func(c *gin.Context) {
		c.Set("user", user)
		h.RequestExport(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+urlProjectID.String()+"/certificates/"+certID.String()+"/export", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockCert.AssertNotCalled(t, "RequestExport")
	mockCert.AssertExpectations(t)
}

func TestManagedCertificateHandler_RequestExport_DeniedWithoutPermission(t *testing.T) {
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
	mockTeam.On("HasPermissionInProject", projectID, user.ID, models.PermCertificateEdit).Return(false, nil)

	router := gin.New()
	router.POST("/projects/:projectId/certificates/:certificateId/export", func(c *gin.Context) {
		c.Set("user", user)
		h.RequestExport(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/export", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockCert.AssertNotCalled(t, "GetByID")
	mockCert.AssertNotCalled(t, "RequestExport")
}

// TestManagedCertificateHandler_ExportDownload_SuccessThenReDownload_Gone
// exercises the single-use grant end to end from the handler's side: the
// first download succeeds with a PRIVATE KEY block in the body, and the
// second download (grant already consumed) 410s -- simulated via the mock
// returning repository.ErrExportGrantUnavailable on the second call, since
// the mock doesn't model the repository's real consume-once behavior.
func TestManagedCertificateHandler_ExportDownload_SuccessThenReDownload_Gone(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Name: "example"}

	leafPEM := []byte("-----BEGIN CERTIFICATE-----\nleaf\n-----END CERTIFICATE-----\n")
	keyPEM := []byte("-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----\n")
	caPEM := []byte("-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----\n")

	mockCert.On("GetByID", certID).Return(cert, nil).Twice()
	mockCert.On("ExportBundle", certID, user.ID).Return(leafPEM, keyPEM, caPEM, nil).Once()
	mockAudit.On("LogAction", &projectID, user, "export_download", "certificate", &certID, cert.Name, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	router := gin.New()
	router.GET("/projects/:projectId/certificates/:certificateId/export/download", func(c *gin.Context) {
		c.Set("user", user)
		h.ExportDownload(c)
	})

	// First download: succeeds, body carries a PRIVATE KEY block.
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/export/download", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "BEGIN PRIVATE KEY")
	assert.Equal(t, "application/x-pem-file", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Header().Get("Content-Disposition"), "example.pem")

	// Second download: grant already consumed -> 410 Gone.
	mockCert.On("ExportBundle", certID, user.ID).Return(([]byte)(nil), ([]byte)(nil), ([]byte)(nil), repository.ErrExportGrantUnavailable).Once()

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/export/download", nil)
	router.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusGone, w2.Code)
	assert.NotContains(t, w2.Body.String(), "BEGIN PRIVATE KEY")

	mockCert.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

func TestManagedCertificateHandler_ExportDownload_ProjectMismatch_NotFound(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser()
	urlProjectID := uuid.New()
	otherProjectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: otherProjectID, Name: "other-project-cert"}

	mockCert.On("GetByID", certID).Return(cert, nil)

	router := gin.New()
	router.GET("/projects/:projectId/certificates/:certificateId/export/download", func(c *gin.Context) {
		c.Set("user", user)
		h.ExportDownload(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+urlProjectID.String()+"/certificates/"+certID.String()+"/export/download", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockCert.AssertNotCalled(t, "ExportBundle")
	mockCert.AssertExpectations(t)
}

// TestManagedCertificateHandler_ExportDownload_NoTokenQueryParamAccepted
// documents that the download endpoint is user-bound, not token-bound: it
// must not accept (or need) a `?token=` query parameter -- ExportBundle is
// only ever called with (certID, the authenticated user's ID), never
// anything parsed from the query string.
func TestManagedCertificateHandler_ExportDownload_NoTokenQueryParamAccepted(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{ID: certID, ProjectID: projectID, Name: "example"}

	mockCert.On("GetByID", certID).Return(cert, nil)
	mockCert.On("ExportBundle", certID, user.ID).Return([]byte("leaf"), []byte("key"), []byte("ca"), nil)
	mockAudit.On("LogAction", &projectID, user, "export_download", "certificate", &certID, cert.Name, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.GET("/projects/:projectId/certificates/:certificateId/export/download", func(c *gin.Context) {
		c.Set("user", user)
		h.ExportDownload(c)
	})

	// A stray ?token= must be ignored entirely -- ExportBundle's mock
	// expectation above only matches (certID, user.ID), with no token
	// argument to smuggle it through.
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/certificates/"+certID.String()+"/export/download?token=some-leaked-value", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockCert.AssertExpectations(t)
}

func TestManagedCertificateHandler_ListFleet_FiltersPassedThrough(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	issuerID := uuid.New()
	expiresBefore, err := time.Parse(time.RFC3339, "2026-12-31T00:00:00Z")
	require := assert.New(t)
	require.NoError(err)

	expected := repository.CertificateListFilter{
		Status:        "ready",
		IssuerID:      &issuerID,
		Usage:         "server",
		ExpiresBefore: &expiresBefore,
		ProjectID:     &projectID,
	}
	mockCert.On("ListFleetCertificates", 1, 20, expected, user.ID).Return([]services.EnrichedCertificate{}, int64(0), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user", user)
	url := "/certificates?status=ready&issuerId=" + issuerID.String() + "&usage=server&expiresBefore=2026-12-31T00:00:00Z&projectId=" + projectID.String()
	c.Request, _ = http.NewRequest("GET", url, nil)

	h.ListFleet(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockCert.AssertExpectations(t)
}

// TestManagedCertificateHandler_Get_IncludesKeyModeSubjectAndURISANs verifies
// that toManagedCertificateResponse populates keyMode, subject, and uriSans
// from the certificate's Config, and that these fields appear in Get responses.
func TestManagedCertificateHandler_Get_IncludesKeyModeSubjectAndURISANs(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	projectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:        certID,
		ProjectID: projectID,
		Name:      "example-cert",
		IssuerID:  uuid.New(),
		Usage:     models.ManagedCertUsageServer,
		Status:    models.ManagedCertStatusReady,
		CreatedAt: time.Now(),
		Config: models.ManagedCertConfig{
			DNSNames: []string{"example.com", "www.example.com"},
			KeyMode:  models.ManagedCertKeyModeManaged,
			Subject:  "CN=example.com",
			URISANs:  []string{"https://example.com", "https://www.example.com"},
		},
	}

	mockCert.On("GetByID", certID).Return(cert, nil)

	router := gin.New()
	router.GET("/projects/:projectId/certificates/:certificateId", h.Get)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/certificates/"+certID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))

	// Verify keyMode is present and correct
	require.Equal(string(models.ManagedCertKeyModeManaged), resp["keyMode"])

	// Verify subject is present and correct
	require.Equal("CN=example.com", resp["subject"])

	// Verify uriSans is present and correct
	uriSans, ok := resp["uriSans"].([]interface{})
	require.True(ok, "uriSans should be an array")
	require.Len(uriSans, 2)
	require.Equal("https://example.com", uriSans[0])
	require.Equal("https://www.example.com", uriSans[1])

	mockCert.AssertExpectations(t)
}

// TestManagedCertificateHandler_Get_ExportAvailable_FromHasUsableExportGrant
// verifies Get calls HasUsableExportGrant with the current user and the
// requested certificate, and surfaces its result on the response's
// exportAvailable field.
func TestManagedCertificateHandler_Get_ExportAvailable_FromHasUsableExportGrant(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:        certID,
		ProjectID: projectID,
		Name:      "example-cert",
		IssuerID:  uuid.New(),
		Usage:     models.ManagedCertUsageServer,
		Status:    models.ManagedCertStatusReady,
		CreatedAt: time.Now(),
	}

	mockCert.On("GetByID", certID).Return(cert, nil)
	mockCert.On("HasUsableExportGrant", certID, user.ID).Return(true, nil)

	router := gin.New()
	router.GET("/projects/:projectId/certificates/:certificateId", func(c *gin.Context) {
		c.Set("user", user)
		h.Get(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/certificates/"+certID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(true, resp["exportAvailable"])

	mockCert.AssertExpectations(t)
}

// TestManagedCertificateHandler_Get_ExportAvailable_False verifies the
// converse: when the service reports no usable grant, exportAvailable is
// false in the response.
func TestManagedCertificateHandler_Get_ExportAvailable_False(t *testing.T) {
	mockCert := new(mocks.MockManagedCertificateService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewManagedCertificateHandler(mockCert, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	certID := uuid.New()
	cert := &models.ManagedCertificate{
		ID:        certID,
		ProjectID: projectID,
		Name:      "example-cert",
		IssuerID:  uuid.New(),
		Usage:     models.ManagedCertUsageServer,
		Status:    models.ManagedCertStatusReady,
		CreatedAt: time.Now(),
	}

	mockCert.On("GetByID", certID).Return(cert, nil)
	mockCert.On("HasUsableExportGrant", certID, user.ID).Return(false, nil)

	router := gin.New()
	router.GET("/projects/:projectId/certificates/:certificateId", func(c *gin.Context) {
		c.Set("user", user)
		h.Get(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/certificates/"+certID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(false, resp["exportAvailable"])

	mockCert.AssertExpectations(t)
}
