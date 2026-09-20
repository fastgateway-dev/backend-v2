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
	"github.com/fastgateway-dev/backend-v2/internal/services/clients"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// clientCertHandlerFixture bundles the mocks + handler for one test.
type clientCertHandlerFixture struct {
	certService *mocks.MockClientCertificateService
	clientSvc   *mocks.MockClientService
	audit       *mocks.MockAuditService
	teamRepo    *mocks.MockTeamRepository
	handler     *handlers.ClientCertificateHandler
}

func newClientCertHandlerFixture() *clientCertHandlerFixture {
	f := &clientCertHandlerFixture{
		certService: new(mocks.MockClientCertificateService),
		clientSvc:   new(mocks.MockClientService),
		audit:       new(mocks.MockAuditService),
		teamRepo:    new(mocks.MockTeamRepository),
	}
	f.handler = handlers.NewClientCertificateHandler(f.certService, f.clientSvc, f.audit, permsFor(f.teamRepo))
	return f
}

func attachBody(certID uuid.UUID) *bytes.Reader {
	b, _ := json.Marshal(map[string]interface{}{"certificateId": certID})
	return bytes.NewReader(b)
}

func TestClientCertificateHandler_AttachCertificate_Success(t *testing.T) {
	f := newClientCertHandlerFixture()
	user := testUser()
	clientID := uuid.New()
	certID := uuid.New()
	teamID := uuid.New()
	existingClient := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	attachedClient := &models.Client{ID: clientID, Name: "client1", TeamID: teamID, MTLSEnabled: true}

	f.clientSvc.On("GetByID", clientID).Return(existingClient, nil)
	f.teamRepo.On("IsMember", teamID, user.ID).Return(true, nil)
	f.certService.On("AttachCertificate", mock.Anything, clientID, certID, user.ID).Return(attachedClient, nil)
	f.audit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.PUT("/clients/:clientId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		f.handler.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/"+clientID.String()+"/certificate", attachBody(certID))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var got models.Client
	require := assert.New(t)
	require.NoError(json.Unmarshal(w.Body.Bytes(), &got))
	require.True(got.MTLSEnabled)

	f.certService.AssertExpectations(t)
}

func TestClientCertificateHandler_AttachCertificate_InvalidClientID(t *testing.T) {
	f := newClientCertHandlerFixture()
	user := testUser()

	router := gin.New()
	router.PUT("/clients/:clientId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		f.handler.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/bad-id/certificate", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientCertificateHandler_AttachCertificate_ClientNotFound(t *testing.T) {
	f := newClientCertHandlerFixture()
	user := testUser()
	clientID := uuid.New()

	f.clientSvc.On("GetByID", clientID).Return(nil, gorm.ErrRecordNotFound)

	router := gin.New()
	router.PUT("/clients/:clientId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		f.handler.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/"+clientID.String()+"/certificate", attachBody(uuid.New()))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestClientCertificateHandler_AttachCertificate_NotTeamMember(t *testing.T) {
	f := newClientCertHandlerFixture()
	user := &models.User{ID: uuid.New(), Role: models.UserRoleUser}
	clientID := uuid.New()
	teamID := uuid.New()
	existingClient := &models.Client{ID: clientID, TeamID: teamID}

	f.clientSvc.On("GetByID", clientID).Return(existingClient, nil)
	f.teamRepo.On("IsMember", teamID, user.ID).Return(false, nil)

	router := gin.New()
	router.PUT("/clients/:clientId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		f.handler.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/"+clientID.String()+"/certificate", attachBody(uuid.New()))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func testAttachSentinelMapping(t *testing.T, svcErr error, wantStatus int) {
	f := newClientCertHandlerFixture()
	user := testUser()
	clientID := uuid.New()
	certID := uuid.New()
	teamID := uuid.New()
	existingClient := &models.Client{ID: clientID, TeamID: teamID}

	f.clientSvc.On("GetByID", clientID).Return(existingClient, nil)
	f.teamRepo.On("IsMember", teamID, user.ID).Return(true, nil)
	f.certService.On("AttachCertificate", mock.Anything, clientID, certID, user.ID).Return(nil, svcErr)

	router := gin.New()
	router.PUT("/clients/:clientId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		f.handler.AttachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/"+clientID.String()+"/certificate", attachBody(certID))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, wantStatus, w.Code)
}

func TestClientCertificateHandler_AttachCertificate_SentinelMappings(t *testing.T) {
	testAttachSentinelMapping(t, gorm.ErrRecordNotFound, http.StatusNotFound)
	testAttachSentinelMapping(t, clients.ErrNotClientCert, http.StatusUnprocessableEntity)
	testAttachSentinelMapping(t, clients.ErrCertNotReadyForAttach, http.StatusUnprocessableEntity)
	testAttachSentinelMapping(t, clients.ErrCertProjectNotAccessible, http.StatusForbidden)
	testAttachSentinelMapping(t, clients.ErrCertAlreadyAttached, http.StatusConflict)
	testAttachSentinelMapping(t, clients.ErrClientHasManagedCert, http.StatusConflict)
}

func TestClientCertificateHandler_DetachCertificate_Success(t *testing.T) {
	f := newClientCertHandlerFixture()
	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	existingClient := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	detachedClient := &models.Client{ID: clientID, Name: "client1", TeamID: teamID, MTLSEnabled: false}

	f.clientSvc.On("GetByID", clientID).Return(existingClient, nil)
	f.teamRepo.On("IsMember", teamID, user.ID).Return(true, nil)
	f.certService.On("DetachCertificate", mock.Anything, clientID, user.ID).Return(detachedClient, nil)
	f.audit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/clients/:clientId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		f.handler.DetachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/certificate", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	f.certService.AssertExpectations(t)
}

func TestClientCertificateHandler_DetachCertificate_NotTeamMember(t *testing.T) {
	f := newClientCertHandlerFixture()
	user := &models.User{ID: uuid.New(), Role: models.UserRoleUser}
	clientID := uuid.New()
	teamID := uuid.New()
	existingClient := &models.Client{ID: clientID, TeamID: teamID}

	f.clientSvc.On("GetByID", clientID).Return(existingClient, nil)
	f.teamRepo.On("IsMember", teamID, user.ID).Return(false, nil)

	router := gin.New()
	router.DELETE("/clients/:clientId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		f.handler.DetachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/certificate", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	f.certService.AssertNotCalled(t, "DetachCertificate", mock.Anything, mock.Anything, mock.Anything)
}

func TestClientCertificateHandler_AttachableCertificates_Success(t *testing.T) {
	f := newClientCertHandlerFixture()
	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	existingClient := &models.Client{ID: clientID, TeamID: teamID}
	certs := []models.ManagedCertificate{
		{ID: uuid.New(), Name: "cert-1", Usage: models.ManagedCertUsageClient, Status: models.ManagedCertStatusReady},
		{ID: uuid.New(), Name: "cert-2", Usage: models.ManagedCertUsageClient, Status: models.ManagedCertStatusReady},
	}

	f.clientSvc.On("GetByID", clientID).Return(existingClient, nil)
	f.teamRepo.On("IsMember", teamID, user.ID).Return(true, nil)
	f.certService.On("AttachableCertificates", clientID).Return(certs, nil)

	router := gin.New()
	router.GET("/clients/:clientId/attachable-certificates", func(c *gin.Context) {
		c.Set("user", user)
		f.handler.AttachableCertificates(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/clients/"+clientID.String()+"/attachable-certificates", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Data []map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data, 2)
	assert.Equal(t, "cert-1", body.Data[0]["name"])
	assert.Equal(t, "cert-2", body.Data[1]["name"])

	f.certService.AssertExpectations(t)
}

func TestClientCertificateHandler_AttachableCertificates_NotTeamMember(t *testing.T) {
	f := newClientCertHandlerFixture()
	user := &models.User{ID: uuid.New(), Role: models.UserRoleUser}
	clientID := uuid.New()
	teamID := uuid.New()
	existingClient := &models.Client{ID: clientID, TeamID: teamID}

	f.clientSvc.On("GetByID", clientID).Return(existingClient, nil)
	f.teamRepo.On("IsMember", teamID, user.ID).Return(false, nil)

	router := gin.New()
	router.GET("/clients/:clientId/attachable-certificates", func(c *gin.Context) {
		c.Set("user", user)
		f.handler.AttachableCertificates(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/clients/"+clientID.String()+"/attachable-certificates", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	f.certService.AssertNotCalled(t, "AttachableCertificates", mock.Anything)
}

func TestClientCertificateHandler_AttachableCertificates_ClientNotFound(t *testing.T) {
	f := newClientCertHandlerFixture()
	user := testUser()
	clientID := uuid.New()

	f.clientSvc.On("GetByID", clientID).Return(nil, gorm.ErrRecordNotFound)

	router := gin.New()
	router.GET("/clients/:clientId/attachable-certificates", func(c *gin.Context) {
		c.Set("user", user)
		f.handler.AttachableCertificates(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/clients/"+clientID.String()+"/attachable-certificates", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestClientCertificateHandler_DetachCertificate_ClientNotFound(t *testing.T) {
	f := newClientCertHandlerFixture()
	user := testUser()
	clientID := uuid.New()

	f.clientSvc.On("GetByID", clientID).Return(nil, gorm.ErrRecordNotFound)

	router := gin.New()
	router.DELETE("/clients/:clientId/certificate", func(c *gin.Context) {
		c.Set("user", user)
		f.handler.DetachCertificate(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/certificate", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
