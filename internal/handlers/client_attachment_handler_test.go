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

func TestClientAttachmentHandler_ListRouteClients_Success(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	routeID := uuid.New()
	attachments := []models.ClientRouteAttachment{
		{ID: uuid.New(), ClientID: uuid.New(), RouteID: routeID, Status: "active"},
	}
	mockAttachment.On("ListByRouteID", routeID).Return(attachments, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/clients", nil)
	c.Params = gin.Params{{Key: "routeId", Value: routeID.String()}}

	h.ListRouteClients(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_ListClientRoutes_Success(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser() // owner role, bypasses team member check
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, TeamID: teamID, Name: "test-client"}
	mockClient.On("GetByID", clientID).Return(client, nil)

	attachments := []models.ClientRouteAttachment{
		{ID: uuid.New(), ClientID: clientID, RouteID: uuid.New(), Status: "active"},
	}
	mockAttachment.On("ListByClientID", clientID).Return(attachments, nil)

	router := gin.New()
	router.GET("/clients/:clientId/routes", func(c *gin.Context) {
		c.Set("user", user)
		h.ListClientRoutes(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/clients/"+clientID.String()+"/routes", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAttachment.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestClientAttachmentHandler_ListRouteClients_InvalidID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/invalid/clients", nil)
	c.Params = gin.Params{{Key: "routeId", Value: "invalid"}}

	h.ListRouteClients(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_ListClientApprovals_Success(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	projectID := uuid.New()
	approvals := []models.Approval{
		{ID: uuid.New(), ProjectID: projectID, Status: "pending"},
	}
	mockAttachment.On("ListApprovalsByProjectID", projectID, 1, 20, "pending").Return(approvals, int64(1), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/client-approvals", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.ListClientApprovals(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_AttachFromRoute_Success(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser() // owner role
	projectID := uuid.New()
	routeID := uuid.New()
	clientID := uuid.New()
	attachment := &models.ClientRouteAttachment{ID: uuid.New(), ClientID: clientID, RouteID: routeID, Status: "pending_approval"}
	mockAttachment.On("AttachFromRoute", routeID, mock.AnythingOfType("*services.AttachFromRouteInput"), user.ID).Return(attachment, nil)
	mockRoute.On("GetDomainName", mock.AnythingOfType("uuid.UUID")).Return("test-domain", nil)
	mockRoute.On("GetApprovalIDForEntity", models.ApprovalEntityClientAttachment, attachment.ID).Return(nil, errors.New("not found"))
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{
		"clientId": clientID.String(),
	})

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/clients/attach", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachFromRoute(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String()+"/clients/attach", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_AttachFromRoute_InvalidRouteID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/clients/attach", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachFromRoute(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/bad-id/clients/attach", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_GetClientApproval_Success(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	approvalID := uuid.New()
	projectID := uuid.New()
	approval := &models.Approval{ID: approvalID, ProjectID: projectID, Status: "pending"}
	mockAttachment.On("GetApproval", approvalID).Return(approval, nil)

	router := gin.New()
	router.GET("/projects/:projectId/client-approvals/:approvalId", func(c *gin.Context) {
		h.GetClientApproval(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/client-approvals/"+approvalID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_GetClientApproval_NotFound(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	approvalID := uuid.New()
	projectID := uuid.New()
	mockAttachment.On("GetApproval", approvalID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.GET("/projects/:projectId/client-approvals/:approvalId", func(c *gin.Context) {
		h.GetClientApproval(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/client-approvals/"+approvalID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_ApproveStage_Success(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()
	entityID := uuid.New()
	approval := &models.Approval{ID: approvalID, ProjectID: projectID, EntityID: entityID, Status: "approved", Action: "attach"}
	mockAttachment.On("ApproveStage", approvalID, stageID, mock.AnythingOfType("*models.User")).Return(approval, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/approve", func(c *gin.Context) {
		c.Set("user", user)
		h.ApproveStage(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/client-approvals/"+approvalID.String()+"/stages/"+stageID.String()+"/approve", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_ApproveStage_InvalidApprovalID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()
	stageID := uuid.New()

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/approve", func(c *gin.Context) {
		c.Set("user", user)
		h.ApproveStage(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/client-approvals/bad-id/stages/"+stageID.String()+"/approve", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_AttachFromClient_Success(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	routeID := uuid.New()
	client := &models.Client{ID: clientID, TeamID: teamID, Name: "test-client"}
	attachment := &models.ClientRouteAttachment{ID: uuid.New(), ClientID: clientID, RouteID: routeID, Status: "pending_approval"}

	mockClient.On("GetByID", clientID).Return(client, nil)
	mockAttachment.On("AttachFromClient", clientID, mock.AnythingOfType("*services.AttachFromClientInput"), user.ID).Return(attachment, nil)
	mockRoute.On("GetApprovalIDForEntity", models.ApprovalEntityClientAttachment, attachment.ID).Return(nil, errors.New("not found"))
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{
		"routeId":   routeID.String(),
		"projectId": uuid.New().String(),
	})

	router := gin.New()
	router.POST("/clients/:clientId/routes/attach", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachFromClient(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/routes/attach", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockAttachment.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestClientAttachmentHandler_AttachFromClient_NoUser(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	clientID := uuid.New()

	router := gin.New()
	router.POST("/clients/:clientId/routes/attach", func(c *gin.Context) {
		// No user set
		h.AttachFromClient(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/routes/attach", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientAttachmentHandler_AttachFromClient_InvalidClientID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()

	router := gin.New()
	router.POST("/clients/:clientId/routes/attach", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachFromClient(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/bad-id/routes/attach", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_AttachFromClient_ClientNotFound(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	clientID := uuid.New()
	mockClient.On("GetByID", clientID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.POST("/clients/:clientId/routes/attach", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachFromClient(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/routes/attach", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientAttachmentHandler_RequestDetachFromRoute_Success(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()
	attachmentID := uuid.New()
	routeID := uuid.New()
	clientID := uuid.New()
	attachment := &models.ClientRouteAttachment{ID: attachmentID, ClientID: clientID, RouteID: routeID, Status: "detaching"}
	mockAttachment.On("RequestDetach", attachmentID, user.ID).Return(attachment, nil)
	mockRoute.On("GetDomainName", mock.AnythingOfType("uuid.UUID")).Return("test-domain", nil)
	mockRoute.On("GetApprovalIDForEntity", models.ApprovalEntityClientAttachment, attachment.ID).Return(nil, errors.New("not found"))
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/clients/:attachmentId/detach", func(c *gin.Context) {
		c.Set("user", user)
		h.RequestDetachFromRoute(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String()+"/clients/"+attachmentID.String()+"/detach", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_RequestDetachFromRoute_NoUser(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	projectID := uuid.New()
	attachmentID := uuid.New()

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/clients/:attachmentId/detach", func(c *gin.Context) {
		// No user set
		h.RequestDetachFromRoute(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+uuid.New().String()+"/clients/"+attachmentID.String()+"/detach", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientAttachmentHandler_RequestDetachFromRoute_InvalidAttachmentID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/clients/:attachmentId/detach", func(c *gin.Context) {
		c.Set("user", user)
		h.RequestDetachFromRoute(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+uuid.New().String()+"/clients/bad-id/detach", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_RejectClientApproval_Success(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()
	entityID := uuid.New()
	approval := &models.Approval{ID: approvalID, ProjectID: projectID, EntityID: entityID, Status: "rejected", Action: "attach"}
	mockAttachment.On("RejectStage", approvalID, stageID, mock.AnythingOfType("*models.User"), "not approved").Return(approval, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"comment": "not approved"})

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/reject", func(c *gin.Context) {
		c.Set("user", user)
		h.RejectClientApproval(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/client-approvals/"+approvalID.String()+"/stages/"+stageID.String()+"/reject", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_RejectClientApproval_NoComment(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()

	// Missing required comment field
	body, _ := json.Marshal(map[string]string{})

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/reject", func(c *gin.Context) {
		c.Set("user", user)
		h.RejectClientApproval(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/client-approvals/"+approvalID.String()+"/stages/"+stageID.String()+"/reject", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_RejectClientApproval_NoUser(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	projectID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/reject", func(c *gin.Context) {
		// No user set
		h.RejectClientApproval(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/client-approvals/"+approvalID.String()+"/stages/"+stageID.String()+"/reject", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientAttachmentHandler_RejectClientApproval_InvalidStageID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/reject", func(c *gin.Context) {
		c.Set("user", user)
		h.RejectClientApproval(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/client-approvals/"+approvalID.String()+"/stages/bad-id/reject", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_AttachFromRoute_NoUser(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	projectID := uuid.New()
	routeID := uuid.New()

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/clients/attach", func(c *gin.Context) {
		// No user set
		h.AttachFromRoute(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String()+"/clients/attach", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientAttachmentHandler_AttachFromRoute_ServiceError(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()
	routeID := uuid.New()
	clientID := uuid.New()
	mockAttachment.On("AttachFromRoute", routeID, mock.AnythingOfType("*services.AttachFromRouteInput"), user.ID).Return(nil, errors.New("already attached"))

	body, _ := json.Marshal(map[string]interface{}{
		"clientId": clientID.String(),
	})

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/clients/attach", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachFromRoute(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String()+"/clients/attach", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_ListRouteClients_ServiceError(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	routeID := uuid.New()
	mockAttachment.On("ListByRouteID", routeID).Return([]models.ClientRouteAttachment{}, errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/routes/"+routeID.String()+"/clients", nil)
	c.Params = gin.Params{{Key: "routeId", Value: routeID.String()}}

	h.ListRouteClients(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_ListClientRoutes_NoUser(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	clientID := uuid.New()

	router := gin.New()
	router.GET("/clients/:clientId/routes", func(c *gin.Context) {
		// No user set
		h.ListClientRoutes(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/clients/"+clientID.String()+"/routes", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientAttachmentHandler_ListClientApprovals_InvalidProjectID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/bad-id/client-approvals", nil)
	c.Params = gin.Params{{Key: "projectId", Value: "bad-id"}}

	h.ListClientApprovals(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_ApproveStage_NoUser(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	projectID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/approve", func(c *gin.Context) {
		// No user set
		h.ApproveStage(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/client-approvals/"+approvalID.String()+"/stages/"+stageID.String()+"/approve", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientAttachmentHandler_ApproveStage_InvalidStageID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/approve", func(c *gin.Context) {
		c.Set("user", user)
		h.ApproveStage(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/client-approvals/"+approvalID.String()+"/stages/bad-id/approve", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_RequestDetachFromRoute_InvalidProjectID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/clients/:attachmentId/detach", func(c *gin.Context) {
		c.Set("user", user)
		h.RequestDetachFromRoute(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/bad-id/domains/"+uuid.New().String()+"/routes/"+uuid.New().String()+"/clients/"+uuid.New().String()+"/detach", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_RequestDetachFromRoute_ServiceError(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()
	attachmentID := uuid.New()
	mockAttachment.On("RequestDetach", attachmentID, user.ID).Return(nil, errors.New("not active"))

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/clients/:attachmentId/detach", func(c *gin.Context) {
		c.Set("user", user)
		h.RequestDetachFromRoute(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+uuid.New().String()+"/clients/"+attachmentID.String()+"/detach", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_AttachFromClient_BadBody(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, TeamID: teamID, Name: "test-client"}
	mockClient.On("GetByID", clientID).Return(client, nil)

	// Empty body - missing required fields
	body, _ := json.Marshal(map[string]string{})

	router := gin.New()
	router.POST("/clients/:clientId/routes/attach", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachFromClient(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/routes/attach", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_AttachFromClient_ServiceError(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	routeID := uuid.New()
	client := &models.Client{ID: clientID, TeamID: teamID, Name: "test-client"}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockAttachment.On("AttachFromClient", clientID, mock.AnythingOfType("*services.AttachFromClientInput"), user.ID).Return(nil, errors.New("already attached"))

	body, _ := json.Marshal(map[string]interface{}{
		"routeId":   routeID.String(),
		"projectId": uuid.New().String(),
	})

	router := gin.New()
	router.POST("/clients/:clientId/routes/attach", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachFromClient(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/routes/attach", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_RejectClientApproval_InvalidProjectID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/reject", func(c *gin.Context) {
		c.Set("user", user)
		h.RejectClientApproval(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/bad-id/client-approvals/"+uuid.New().String()+"/stages/"+uuid.New().String()+"/reject", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_RejectClientApproval_InvalidApprovalID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/reject", func(c *gin.Context) {
		c.Set("user", user)
		h.RejectClientApproval(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/client-approvals/bad-id/stages/"+uuid.New().String()+"/reject", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_RejectClientApproval_ServiceError(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()
	mockAttachment.On("RejectStage", approvalID, stageID, mock.AnythingOfType("*models.User"), "bad").Return(nil, errors.New("cannot reject"))

	body, _ := json.Marshal(map[string]string{"comment": "bad"})

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/reject", func(c *gin.Context) {
		c.Set("user", user)
		h.RejectClientApproval(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/client-approvals/"+approvalID.String()+"/stages/"+stageID.String()+"/reject", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_ApproveStage_ServiceError(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()
	approvalID := uuid.New()
	stageID := uuid.New()
	mockAttachment.On("ApproveStage", approvalID, stageID, mock.AnythingOfType("*models.User")).Return(nil, errors.New("cannot approve own"))

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/approve", func(c *gin.Context) {
		c.Set("user", user)
		h.ApproveStage(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/client-approvals/"+approvalID.String()+"/stages/"+stageID.String()+"/approve", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_ApproveStage_InvalidProjectID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/client-approvals/:approvalId/stages/:stageId/approve", func(c *gin.Context) {
		c.Set("user", user)
		h.ApproveStage(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/bad-id/client-approvals/"+uuid.New().String()+"/stages/"+uuid.New().String()+"/approve", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_GetClientApproval_InvalidApprovalID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	router := gin.New()
	router.GET("/projects/:projectId/client-approvals/:approvalId", func(c *gin.Context) {
		h.GetClientApproval(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+uuid.New().String()+"/client-approvals/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_ListClientApprovals_ServiceError(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	projectID := uuid.New()
	mockAttachment.On("ListApprovalsByProjectID", projectID, 1, 20, "pending").Return([]models.Approval{}, int64(0), errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/client-approvals", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.ListClientApprovals(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_ListClientRoutes_InvalidClientID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()

	router := gin.New()
	router.GET("/clients/:clientId/routes", func(c *gin.Context) {
		c.Set("user", user)
		h.ListClientRoutes(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/clients/bad-id/routes", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_ListClientRoutes_ClientNotFound(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	clientID := uuid.New()
	mockClient.On("GetByID", clientID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.GET("/clients/:clientId/routes", func(c *gin.Context) {
		c.Set("user", user)
		h.ListClientRoutes(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/clients/"+clientID.String()+"/routes", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientAttachmentHandler_ListClientRoutes_ServiceError(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, TeamID: teamID, Name: "test-client"}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockAttachment.On("ListByClientID", clientID).Return([]models.ClientRouteAttachment{}, errors.New("db error"))

	router := gin.New()
	router.GET("/clients/:clientId/routes", func(c *gin.Context) {
		c.Set("user", user)
		h.ListClientRoutes(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/clients/"+clientID.String()+"/routes", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockClient.AssertExpectations(t)
	mockAttachment.AssertExpectations(t)
}

func TestClientAttachmentHandler_AttachFromRoute_InvalidProjectID(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/clients/attach", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachFromRoute(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/bad-id/domains/"+uuid.New().String()+"/routes/"+uuid.New().String()+"/clients/attach", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientAttachmentHandler_AttachFromRoute_BadBody(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := testUser()
	projectID := uuid.New()
	routeID := uuid.New()

	// Empty body - missing required fields
	body, _ := json.Marshal(map[string]string{})

	router := gin.New()
	router.POST("/projects/:projectId/domains/:domainId/routes/:routeId/clients/attach", func(c *gin.Context) {
		c.Set("user", user)
		h.AttachFromRoute(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/domains/"+uuid.New().String()+"/routes/"+routeID.String()+"/clients/attach", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

var _ = mock.Anything // suppress unused import if needed

// --- Phase 2F Task 4: denial coverage for the four checks moved to middleware ---
//
// These four call sites had no handler-level denial test before the move.
// Each asserts 403 for a non-owner who fails the check, so a later change to
// middleware.HasTeamPermission or middleware.CanAccessTeamResource that
// widened access would fail here as well as in the middleware's own tests.

func attachmentNonOwner() *models.User {
	return &models.User{
		ID:       uuid.New(),
		Username: "member",
		Email:    "member@test.com",
		Role:     models.UserRoleUser,
		IsActive: true,
	}
}

func TestClientAttachmentHandler_AttachFromRoute_NoAttachPermission_Forbidden(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := attachmentNonOwner()
	projectID, routeID := uuid.New(), uuid.New()
	mockTeamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermClientAttach).Return(false, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/attach", bytes.NewBufferString(`{}`))
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}, {Key: "routeId", Value: routeID.String()}}
	c.Set("user", user)

	h.AttachFromRoute(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockAttachment.AssertNotCalled(t, "AttachFromRoute", mock.Anything, mock.Anything, mock.Anything)
	mockTeamRepo.AssertExpectations(t)
}

func TestClientAttachmentHandler_RequestDetachFromRoute_NoDetachPermission_Forbidden(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := attachmentNonOwner()
	projectID, attachmentID := uuid.New(), uuid.New()
	mockTeamRepo.On("HasPermissionInProject", projectID, user.ID, models.PermClientDetach).Return(false, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/detach", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}, {Key: "attachmentId", Value: attachmentID.String()}}
	c.Set("user", user)

	h.RequestDetachFromRoute(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockAttachment.AssertNotCalled(t, "RequestDetach", mock.Anything, mock.Anything)
	mockTeamRepo.AssertExpectations(t)
}

func TestClientAttachmentHandler_ListClientRoutes_NotTeamMember_Forbidden(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := attachmentNonOwner()
	clientID, teamID := uuid.New(), uuid.New()
	mockClient.On("GetByID", clientID).Return(&models.Client{ID: clientID, TeamID: teamID}, nil)
	mockTeamRepo.On("IsMember", teamID, user.ID).Return(false, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/clients/"+clientID.String()+"/routes", nil)
	c.Params = gin.Params{{Key: "clientId", Value: clientID.String()}}
	c.Set("user", user)

	h.ListClientRoutes(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockAttachment.AssertNotCalled(t, "ListByClientID", mock.Anything)
	mockTeamRepo.AssertExpectations(t)
}

func TestClientAttachmentHandler_AttachFromClient_NotTeamMember_Forbidden(t *testing.T) {
	mockAttachment := new(mocks.MockClientAttachmentService)
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockRoute := new(mocks.MockRouteService)
	h := handlers.NewClientAttachmentHandler(mockAttachment, mockClient, mockAudit, mockRoute, permsFor(mockTeamRepo))

	user := attachmentNonOwner()
	clientID, teamID := uuid.New(), uuid.New()
	mockClient.On("GetByID", clientID).Return(&models.Client{ID: clientID, TeamID: teamID}, nil)
	mockTeamRepo.On("IsMember", teamID, user.ID).Return(false, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/clients/"+clientID.String()+"/routes/attach", bytes.NewBufferString(`{}`))
	c.Params = gin.Params{{Key: "clientId", Value: clientID.String()}}
	c.Set("user", user)

	h.AttachFromClient(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockAttachment.AssertNotCalled(t, "AttachFromClient", mock.Anything, mock.Anything, mock.Anything)
	mockTeamRepo.AssertExpectations(t)
}
