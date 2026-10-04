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
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

func TestDNSRecordHandler_Get_NotFound(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser() // Owner role bypasses the canManageDomains check, isolating the not-found mapping
	projectID := uuid.New()
	domainID := uuid.New()

	mockSvc.On("Get", domainID).Return((*models.DomainDNSRecord)(nil), gorm.ErrRecordNotFound)

	router := gin.New()
	router.GET("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Get(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockSvc.AssertExpectations(t)
}

func TestDNSRecordHandler_Get_Success(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	credID := uuid.New()
	rec := &models.DomainDNSRecord{
		ID:                   uuid.New(),
		DomainID:             domainID,
		ProviderCredentialID: credID,
		RecordType:           models.DNSRecordTypeA,
		Status:               models.DNSRecordStatusReady,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}

	mockSvc.On("Get", domainID).Return(rec, nil)

	router := gin.New()
	router.GET("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Get(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(domainID.String(), resp["domainId"])
	require.Equal(credID.String(), resp["providerCredentialId"])
	require.Equal(string(models.DNSRecordStatusReady), resp["status"])
	mockSvc.AssertExpectations(t)
}

func TestDNSRecordHandler_Enable_DeniedWithoutPermission(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	mockProject := new(mocks.MockProjectRepository)
	mockTeam := new(mocks.MockTeamRepository)
	pc := middleware.NewPermissionChecker(mockProject, mockTeam)
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := &models.User{ID: uuid.New(), Username: "dev1", Role: models.UserRoleUser, IsActive: true}
	projectID := uuid.New()
	domainID := uuid.New()

	mockProject.On("IsAdmin", projectID, user.ID).Return(false, nil)
	mockTeam.On("HasPermissionInProject", projectID, user.ID, models.PermDomainDelete).Return(false, nil)

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Enable(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"recordType": "A"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockSvc.AssertNotCalled(t, "Enable")
	mockProject.AssertExpectations(t)
	mockTeam.AssertExpectations(t)
}

// TestDNSRecordHandler_Enable_CredentialMismatch_BadRequest verifies the
// handler maps services.ErrCredentialNotActive (returned when the caller
// supplies a providerCredentialId that doesn't match the system-wide active
// DNS credential) to 400, not the generic 500 path.
func TestDNSRecordHandler_Enable_CredentialMismatch_BadRequest(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser() // Owner role bypasses permission checks, isolating the error mapping
	projectID := uuid.New()
	domainID := uuid.New()
	otherCred := uuid.New()

	mockSvc.On("Enable", domainID, user.ID, mock.AnythingOfType("services.DNSRecordInput")).
		Return((*models.DomainDNSRecord)(nil), services.ErrCredentialNotActive)

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Enable(c)
	})

	body, _ := json.Marshal(map[string]interface{}{
		"providerCredentialId": otherCred.String(),
		"recordType":           "A",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSvc.AssertExpectations(t)
	mockAudit.AssertNotCalled(t, "LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestDNSRecordHandler_Enable_NoActiveCredential_BadRequest(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()

	mockSvc.On("Enable", domainID, user.ID, mock.AnythingOfType("services.DNSRecordInput")).
		Return((*models.DomainDNSRecord)(nil), services.ErrNoActiveDNSCredential)

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Enable(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"recordType": "A"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSvc.AssertExpectations(t)
}

func TestDNSRecordHandler_Enable_AlreadyExists_Conflict(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()

	mockSvc.On("Enable", domainID, user.ID, mock.AnythingOfType("services.DNSRecordInput")).
		Return((*models.DomainDNSRecord)(nil), services.ErrDNSRecordExists)

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Enable(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"recordType": "A"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	mockSvc.AssertExpectations(t)
}

func TestDNSRecordHandler_Enable_Success_Returns201(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	credID := uuid.New()
	rec := &models.DomainDNSRecord{
		ID:                   uuid.New(),
		DomainID:             domainID,
		ProviderCredentialID: credID,
		RecordType:           models.DNSRecordTypeAuto,
		Status:               models.DNSRecordStatusPending,
	}

	mockSvc.On("Enable", domainID, user.ID, mock.AnythingOfType("services.DNSRecordInput")).Return(rec, nil)
	mockAudit.On("LogAction", &projectID, user, "create", "dns_record", &rec.ID, domainID.String(), mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Enable(c)
	})

	body, _ := json.Marshal(map[string]interface{}{
		"providerCredentialId": credID.String(),
		"proxied":              true,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockSvc.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

func TestDNSRecordHandler_Update_Success(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	rec := &models.DomainDNSRecord{ID: uuid.New(), DomainID: domainID, RecordType: models.DNSRecordTypeCNAME}

	mockSvc.On("Update", domainID, mock.AnythingOfType("services.DNSRecordInput")).Return(rec, nil)
	mockAudit.On("LogAction", &projectID, user, "update", "dns_record", &rec.ID, domainID.String(), mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"recordType": "CNAME"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockSvc.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

func TestDNSRecordHandler_Update_NotFound(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()

	mockSvc.On("Update", domainID, mock.AnythingOfType("services.DNSRecordInput")).Return((*models.DomainDNSRecord)(nil), gorm.ErrRecordNotFound)

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"recordType": "CNAME"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockSvc.AssertExpectations(t)
	mockAudit.AssertNotCalled(t, "LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestDNSRecordHandler_Delete_Success_Returns204(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser() // Owner role bypasses permission checks
	projectID := uuid.New()
	domainID := uuid.New()

	mockSvc.On("Delete", domainID).Return(nil)
	mockAudit.On("LogAction", &projectID, user, "delete", "dns_record", (*uuid.UUID)(nil), domainID.String(), mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockSvc.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

func TestDNSRecordHandler_Delete_DeniedWithoutPermission(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	mockProject := new(mocks.MockProjectRepository)
	mockTeam := new(mocks.MockTeamRepository)
	pc := middleware.NewPermissionChecker(mockProject, mockTeam)
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := &models.User{ID: uuid.New(), Username: "dev1", Role: models.UserRoleUser, IsActive: true}
	projectID := uuid.New()
	domainID := uuid.New()

	mockProject.On("IsAdmin", projectID, user.ID).Return(false, nil)
	mockTeam.On("HasPermissionInProject", projectID, user.ID, models.PermDomainDelete).Return(false, nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockSvc.AssertNotCalled(t, "Delete")
}

func TestDNSRecordHandler_Refresh_Success(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()
	rec := &models.DomainDNSRecord{ID: uuid.New(), DomainID: domainID, Status: models.DNSRecordStatusSyncing}

	mockSvc.On("Refresh", domainID).Return(rec, nil)

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/dns-record/refresh", func(c *gin.Context) {
		c.Set("user", user)
		h.Refresh(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record/refresh", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockSvc.AssertExpectations(t)
}

func TestDNSRecordHandler_Refresh_NotFound(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()

	mockSvc.On("Refresh", domainID).Return((*models.DomainDNSRecord)(nil), gorm.ErrRecordNotFound)

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/dns-record/refresh", func(c *gin.Context) {
		c.Set("user", user)
		h.Refresh(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record/refresh", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockSvc.AssertExpectations(t)
}

// --- DNSActiveCredentialHandler ---

func TestDNSActiveCredentialHandler_Get_NilWhenNoneActive(t *testing.T) {
	mockSvc := new(mocks.MockDNSActiveCredentialService)
	h := handlers.NewDNSActiveCredentialHandler(mockSvc, new(mocks.MockAuditService))

	mockSvc.On("GetActiveCredentialID").Return((*uuid.UUID)(nil), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/dns/settings/active-credential", nil)

	h.Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))
	require.Nil(resp["credentialId"])
	mockSvc.AssertExpectations(t)
}

func TestDNSActiveCredentialHandler_Get_ReturnsActiveID(t *testing.T) {
	mockSvc := new(mocks.MockDNSActiveCredentialService)
	h := handlers.NewDNSActiveCredentialHandler(mockSvc, new(mocks.MockAuditService))

	credID := uuid.New()
	mockSvc.On("GetActiveCredentialID").Return(&credID, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/dns/settings/active-credential", nil)

	h.Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(credID.String(), resp["credentialId"])
	mockSvc.AssertExpectations(t)
}

func TestDNSActiveCredentialHandler_Set_Success(t *testing.T) {
	mockSvc := new(mocks.MockDNSActiveCredentialService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewDNSActiveCredentialHandler(mockSvc, mockAudit)

	credID := uuid.New()
	mockSvc.On("SetActiveCredential", credID).Return(nil)
	mockAudit.On("LogAction", (*uuid.UUID)(nil), (*models.User)(nil), "update", "dns_active_credential", &credID, credID.String(), mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.PUT("/dns/settings/active-credential", h.Set)

	body, _ := json.Marshal(map[string]string{"credentialId": credID.String()})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/dns/settings/active-credential", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockSvc.AssertExpectations(t)
	mockAudit.AssertExpectations(t)
}

func TestDNSActiveCredentialHandler_Set_ServiceError_BadRequest(t *testing.T) {
	mockSvc := new(mocks.MockDNSActiveCredentialService)
	h := handlers.NewDNSActiveCredentialHandler(mockSvc, new(mocks.MockAuditService))

	credID := uuid.New()
	mockSvc.On("SetActiveCredential", credID).Return(assertAnError())

	router := gin.New()
	router.PUT("/dns/settings/active-credential", h.Set)

	body, _ := json.Marshal(map[string]string{"credentialId": credID.String()})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/dns/settings/active-credential", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSvc.AssertExpectations(t)
}

func assertAnError() error {
	return gorm.ErrRecordNotFound
}
