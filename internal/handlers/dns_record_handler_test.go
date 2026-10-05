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

	mockSvc.On("Get", domainID, projectID).Return((*models.DomainDNSRecord)(nil), gorm.ErrRecordNotFound)

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
	zoneID := uuid.New()
	rec := &models.DomainDNSRecord{
		ID:           uuid.New(),
		DomainID:     domainID,
		HostedZoneID: zoneID,
		RecordType:   models.DNSRecordTypeA,
		Status:       models.DNSRecordStatusReady,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	mockSvc.On("Get", domainID, projectID).Return(rec, nil)

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
	require.Equal(zoneID.String(), resp["hostedZoneId"])
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

// TestDNSRecordHandler_Enable_NoHostedZone_BadRequest verifies the handler
// maps services.ErrNoHostedZone (returned when the caller doesn't supply a
// hostedZoneId) to 400, not the generic 500 path.
func TestDNSRecordHandler_Enable_NoHostedZone_BadRequest(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()

	mockSvc.On("Enable", domainID, projectID, user.ID, mock.AnythingOfType("services.DNSRecordInput")).
		Return((*models.DomainDNSRecord)(nil), services.ErrNoHostedZone)

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

// TestDNSRecordHandler_Enable_InvalidRecordType_BadRequest verifies the
// handler maps services.ErrInvalidRecordType (an unrecognized recordType
// like "TXT" must be rejected, never persisted) to 400, not the generic 500
// path.
func TestDNSRecordHandler_Enable_InvalidRecordType_BadRequest(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()

	mockSvc.On("Enable", domainID, projectID, user.ID, mock.AnythingOfType("services.DNSRecordInput")).
		Return((*models.DomainDNSRecord)(nil), services.ErrInvalidRecordType)

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Enable(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"recordType": "TXT"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSvc.AssertExpectations(t)
	mockAudit.AssertNotCalled(t, "LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestDNSRecordHandler_Update_InvalidRecordType_BadRequest is Update's
// counterpart to the Enable test above.
func TestDNSRecordHandler_Update_InvalidRecordType_BadRequest(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()

	mockSvc.On("Update", domainID, projectID, mock.AnythingOfType("services.DNSRecordInput")).
		Return((*models.DomainDNSRecord)(nil), services.ErrInvalidRecordType)

	router := gin.New()
	router.PUT("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"recordType": "foo"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/dns-record", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSvc.AssertExpectations(t)
	mockAudit.AssertNotCalled(t, "LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestDNSRecordHandler_Enable_AlreadyExists_Conflict(t *testing.T) {
	mockSvc := new(mocks.MockDNSRecordService)
	mockAudit := new(mocks.MockAuditService)
	pc := middleware.NewPermissionChecker(new(mocks.MockProjectRepository), new(mocks.MockTeamRepository))
	h := handlers.NewDNSRecordHandler(mockSvc, pc, mockAudit)

	user := testUser()
	projectID := uuid.New()
	domainID := uuid.New()

	mockSvc.On("Enable", domainID, projectID, user.ID, mock.AnythingOfType("services.DNSRecordInput")).
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
	zoneID := uuid.New()
	rec := &models.DomainDNSRecord{
		ID:           uuid.New(),
		DomainID:     domainID,
		HostedZoneID: zoneID,
		RecordType:   models.DNSRecordTypeAuto,
		Status:       models.DNSRecordStatusPending,
	}

	mockSvc.On("Enable", domainID, projectID, user.ID, mock.AnythingOfType("services.DNSRecordInput")).Return(rec, nil)
	mockAudit.On("LogAction", &projectID, user, "create", "dns_record", &rec.ID, domainID.String(), mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/dns-record", func(c *gin.Context) {
		c.Set("user", user)
		h.Enable(c)
	})

	body, _ := json.Marshal(map[string]interface{}{
		"hostedZoneId": zoneID.String(),
		"proxied":      true,
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

	mockSvc.On("Update", domainID, projectID, mock.AnythingOfType("services.DNSRecordInput")).Return(rec, nil)
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

	mockSvc.On("Update", domainID, projectID, mock.AnythingOfType("services.DNSRecordInput")).Return((*models.DomainDNSRecord)(nil), gorm.ErrRecordNotFound)

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

	mockSvc.On("Delete", domainID, projectID).Return(nil)
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
	rec := &models.DomainDNSRecord{ID: uuid.New(), DomainID: domainID, Status: models.DNSRecordStatusPending}

	mockSvc.On("Refresh", domainID, projectID).Return(rec, nil)

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

	mockSvc.On("Refresh", domainID, projectID).Return((*models.DomainDNSRecord)(nil), gorm.ErrRecordNotFound)

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
