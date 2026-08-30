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
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestClientHandler_List_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clients := []models.Client{
		{ID: uuid.New(), Name: "client1", TeamID: uuid.New()},
		{ID: uuid.New(), Name: "client2", TeamID: uuid.New()},
	}
	mockClient.On("List", 1, 20, (*uuid.UUID)(nil)).Return(clients, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/clients", nil)
	c.Set("user", user)

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_Create_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser() // owner role
	teamID := uuid.New()
	client := &models.Client{ID: uuid.New(), Name: "new-client", TeamID: teamID}
	mockClient.On("Create", mock.AnythingOfType("*services.CreateClientInput"), user.ID).Return(client, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{"name": "new-client", "teamId": teamID.String()})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/clients", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_Get_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	clientID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: uuid.New()}
	mockClient.On("GetByID", clientID).Return(client, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/clients/"+clientID.String(), nil)
	c.Params = gin.Params{{Key: "clientId", Value: clientID.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_Get_InvalidID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/clients/bad-id", nil)
	c.Params = gin.Params{{Key: "clientId", Value: "bad-id"}}

	h.Get(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_Get_NotFound(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	clientID := uuid.New()
	mockClient.On("GetByID", clientID).Return(nil, errors.New("not found"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/clients/"+clientID.String(), nil)
	c.Params = gin.Params{{Key: "clientId", Value: clientID.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_Delete_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser() // owner role
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("Delete", mock.Anything, clientID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/clients/:clientId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_Update_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser() // owner role
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	updatedClient := &models.Client{ID: clientID, Name: "updated-client", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("Update", clientID, mock.AnythingOfType("*services.UpdateClientInput")).Return(updatedClient, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"name": "updated-client"})

	router := gin.New()
	router.PUT("/clients/:clientId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/"+clientID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_ListIPs_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	ips := []models.ClientIPAddress{
		{ID: uuid.New(), ClientID: clientID, CIDR: "10.0.0.0/8"},
	}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("ListIPs", clientID).Return(ips, nil)

	router := gin.New()
	router.GET("/clients/:clientId/ips", func(c *gin.Context) {
		c.Set("user", user)
		h.ListIPs(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/clients/"+clientID.String()+"/ips", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_AddIP_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	ip := &models.ClientIPAddress{ID: uuid.New(), ClientID: clientID, CIDR: "192.168.1.0/24"}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("AddIP", clientID, mock.AnythingOfType("*services.CreateClientIPInput"), user.ID).Return(ip, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"cidr": "192.168.1.0/24"})

	router := gin.New()
	router.POST("/clients/:clientId/ips", func(c *gin.Context) {
		c.Set("user", user)
		h.AddIP(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/ips", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_RemoveIP_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	ipID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("RemoveIP", clientID, ipID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/clients/:clientId/ips/:ipId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveIP(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/ips/"+ipID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_GenerateAPIKey_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	resp := &services.GenerateAPIKeyResponse{
		APIKey:     "fg_live_abc123",
		Prefix:     "fg_live_abc1",
		HeaderName: "X-API-Key",
		CreatedAt:  time.Now(),
	}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("GenerateAPIKey", mock.Anything, clientID, mock.AnythingOfType("*services.GenerateAPIKeyInput"), user.ID).Return(resp, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.POST("/clients/:clientId/api-key", func(c *gin.Context) {
		c.Set("user", user)
		h.GenerateAPIKey(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/api-key", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_RevokeAPIKey_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("RevokeAPIKey", mock.Anything, clientID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/clients/:clientId/api-key", func(c *gin.Context) {
		c.Set("user", user)
		h.RevokeAPIKey(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/api-key", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_List_NoUser(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/clients", nil)
	// No user set

	h.List(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientHandler_List_WithTeamFilter(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	teamID := uuid.New()
	clients := []models.Client{
		{ID: uuid.New(), Name: "client1", TeamID: teamID},
	}
	mockClient.On("List", 1, 20, mock.AnythingOfType("*uuid.UUID")).Return(clients, int64(1), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/clients?teamId="+teamID.String(), nil)
	c.Set("user", user)

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_List_InvalidTeamID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/clients?teamId=bad-id", nil)
	c.Set("user", user)

	h.List(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_List_ServiceError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	mockClient.On("List", 1, 20, (*uuid.UUID)(nil)).Return([]models.Client{}, int64(0), errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/clients", nil)
	c.Set("user", user)

	h.List(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_Create_NoUser(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/clients", nil)
	// No user set

	h.Create(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientHandler_Update_NotFound(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	mockClient.On("GetByID", clientID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.PUT("/clients/:clientId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	body, _ := json.Marshal(map[string]string{"name": "updated"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/"+clientID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_Update_NoUser(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	clientID := uuid.New()

	router := gin.New()
	router.PUT("/clients/:clientId", func(c *gin.Context) {
		// No user set
		h.Update(c)
	})

	body, _ := json.Marshal(map[string]string{"name": "updated"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/"+clientID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientHandler_Update_InvalidID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	router := gin.New()
	router.PUT("/clients/:clientId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_Delete_NotFound(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	mockClient.On("GetByID", clientID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.DELETE("/clients/:clientId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_Delete_NoUser(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	clientID := uuid.New()

	router := gin.New()
	router.DELETE("/clients/:clientId", func(c *gin.Context) {
		// No user set
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientHandler_Delete_InvalidID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	router := gin.New()
	router.DELETE("/clients/:clientId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_Delete_ServiceError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("Delete", mock.Anything, clientID).Return(errors.New("has active attachments"))

	router := gin.New()
	router.DELETE("/clients/:clientId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_GenerateAPIKey_NotFound(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	mockClient.On("GetByID", clientID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.POST("/clients/:clientId/api-key", func(c *gin.Context) {
		c.Set("user", user)
		h.GenerateAPIKey(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/api-key", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_GenerateAPIKey_NoUser(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	clientID := uuid.New()

	router := gin.New()
	router.POST("/clients/:clientId/api-key", func(c *gin.Context) {
		// No user set
		h.GenerateAPIKey(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/api-key", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientHandler_RevokeAPIKey_NotFound(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	mockClient.On("GetByID", clientID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.DELETE("/clients/:clientId/api-key", func(c *gin.Context) {
		c.Set("user", user)
		h.RevokeAPIKey(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/api-key", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_ConfigureJWT_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	resp := &services.ConfigureJWTResponse{JWTEnabled: true}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("ConfigureJWT", mock.Anything, clientID, mock.Anything, user.ID).Return(resp, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{
		"issuer":  "https://example.com",
		"jwksUrl": "https://example.com/.well-known/jwks.json",
	})

	router := gin.New()
	router.POST("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.ConfigureJWT(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/jwt", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_ConfigureJWT_NotFound(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	mockClient.On("GetByID", clientID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.POST("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.ConfigureJWT(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/jwt", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_RemoveJWT_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("RemoveJWT", mock.Anything, clientID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveJWT(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/jwt", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_RemoveJWT_NotFound(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	mockClient.On("GetByID", clientID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.DELETE("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveJWT(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/jwt", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_UpdateJWT_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID, JWTEnabled: true}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("ConfigureJWT", mock.Anything, clientID, mock.Anything, user.ID).Return(&services.ConfigureJWTResponse{JWTEnabled: true, JWTIssuer: "https://auth.example.com"}, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"issuer": "https://auth.example.com", "jwksUrl": "https://auth.example.com/.well-known/jwks.json"})

	router := gin.New()
	router.PUT("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateJWT(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/"+clientID.String()+"/jwt", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_UpdateJWT_NotEnabled(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID, JWTEnabled: false}
	mockClient.On("GetByID", clientID).Return(client, nil)

	body, _ := json.Marshal(map[string]string{"issuer": "https://auth.example.com", "jwksUrl": "https://auth.example.com/.well-known/jwks.json"})

	router := gin.New()
	router.PUT("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateJWT(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/"+clientID.String()+"/jwt", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_UpdateJWT_InvalidID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	router := gin.New()
	router.PUT("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateJWT(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/bad-id/jwt", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_UpdateJWT_NoUser(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/clients/"+uuid.New().String()+"/jwt", nil)
	c.Params = gin.Params{{Key: "clientId", Value: uuid.New().String()}}

	h.UpdateJWT(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientHandler_UpdateClientMTLS_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockTeamRepo.On("IsMember", teamID, user.ID).Return(true, nil)
	mockClient.On("UpdateClientMTLS", mock.Anything, clientID, mock.Anything, user.ID).Return(client, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{"enabled": true, "caName": "my-ca", "caPem": "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----"})

	router := gin.New()
	router.PUT("/clients/:clientId/mtls", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateClientMTLS(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/"+clientID.String()+"/mtls", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_UpdateClientMTLS_InvalidID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	router := gin.New()
	router.PUT("/clients/:clientId/mtls", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateClientMTLS(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/bad-id/mtls", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_UpdateClientMTLS_NotFound(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	mockClient.On("GetByID", clientID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.PUT("/clients/:clientId/mtls", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateClientMTLS(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/"+clientID.String()+"/mtls", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestClientHandler_UpdateClientMTLS_NotMember(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := &models.User{ID: uuid.New(), Role: models.UserRoleUser}
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockTeamRepo.On("IsMember", teamID, user.ID).Return(false, nil)

	router := gin.New()
	router.PUT("/clients/:clientId/mtls", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateClientMTLS(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/clients/"+clientID.String()+"/mtls", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestClientHandler_DeleteClientMTLS_Success(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockTeamRepo.On("IsMember", teamID, user.ID).Return(true, nil)
	mockClient.On("UpdateClientMTLS", mock.Anything, clientID, mock.Anything, user.ID).Return(client, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/clients/:clientId/mtls", func(c *gin.Context) {
		c.Set("user", user)
		h.DeleteClientMTLS(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/mtls", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_DeleteClientMTLS_InvalidID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	router := gin.New()
	router.DELETE("/clients/:clientId/mtls", func(c *gin.Context) {
		c.Set("user", user)
		h.DeleteClientMTLS(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/bad-id/mtls", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_DeleteClientMTLS_NotFound(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	mockClient.On("GetByID", clientID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.DELETE("/clients/:clientId/mtls", func(c *gin.Context) {
		c.Set("user", user)
		h.DeleteClientMTLS(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/mtls", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestClientHandler_ListIPs_NoUser(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/clients/"+uuid.New().String()+"/ips", nil)
	c.Params = gin.Params{{Key: "clientId", Value: uuid.New().String()}}

	h.ListIPs(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientHandler_ListIPs_InvalidID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/clients/bad-id/ips", nil)
	c.Params = gin.Params{{Key: "clientId", Value: "bad-id"}}
	c.Set("user", user)

	h.ListIPs(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_ListIPs_ClientNotFound(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	mockClient.On("GetByID", clientID).Return(nil, errors.New("not found"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/clients/"+clientID.String()+"/ips", nil)
	c.Params = gin.Params{{Key: "clientId", Value: clientID.String()}}
	c.Set("user", user)

	h.ListIPs(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestClientHandler_ListIPs_ServiceError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("ListIPs", clientID).Return([]models.ClientIPAddress{}, errors.New("db error"))

	router := gin.New()
	router.GET("/clients/:clientId/ips", func(c *gin.Context) {
		c.Set("user", user)
		h.ListIPs(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/clients/"+clientID.String()+"/ips", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestClientHandler_AddIP_NoUser(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/clients/"+uuid.New().String()+"/ips", nil)
	c.Params = gin.Params{{Key: "clientId", Value: uuid.New().String()}}

	h.AddIP(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientHandler_AddIP_InvalidID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/clients/bad-id/ips", nil)
	c.Params = gin.Params{{Key: "clientId", Value: "bad-id"}}
	c.Set("user", user)

	h.AddIP(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_RemoveIP_NoUser(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/clients/"+uuid.New().String()+"/ips/"+uuid.New().String(), nil)
	c.Params = gin.Params{{Key: "clientId", Value: uuid.New().String()}, {Key: "ipId", Value: uuid.New().String()}}

	h.RemoveIP(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientHandler_RemoveIP_InvalidClientID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/clients/bad-id/ips/"+uuid.New().String(), nil)
	c.Params = gin.Params{{Key: "clientId", Value: "bad-id"}, {Key: "ipId", Value: uuid.New().String()}}
	c.Set("user", user)

	h.RemoveIP(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_RemoveIP_InvalidIPID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/clients/"+clientID.String()+"/ips/bad-id", nil)
	c.Params = gin.Params{{Key: "clientId", Value: clientID.String()}, {Key: "ipId", Value: "bad-id"}}
	c.Set("user", user)

	h.RemoveIP(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_ListIPs_Forbidden(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := &models.User{ID: uuid.New(), Role: models.UserRoleUser}
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockTeamRepo.On("IsMember", teamID, user.ID).Return(false, nil)

	router := gin.New()
	router.GET("/clients/:clientId/ips", func(c *gin.Context) {
		c.Set("user", user)
		h.ListIPs(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/clients/"+clientID.String()+"/ips", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestClientHandler_AddIP_Forbidden(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := &models.User{ID: uuid.New(), Role: models.UserRoleUser}
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockTeamRepo.On("IsMember", teamID, user.ID).Return(false, nil)

	body, _ := json.Marshal(map[string]string{"cidr": "10.0.0.0/8"})

	router := gin.New()
	router.POST("/clients/:clientId/ips", func(c *gin.Context) {
		c.Set("user", user)
		h.AddIP(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/ips", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestClientHandler_RemoveIP_Forbidden(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := &models.User{ID: uuid.New(), Role: models.UserRoleUser}
	clientID := uuid.New()
	ipID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockTeamRepo.On("IsMember", teamID, user.ID).Return(false, nil)

	router := gin.New()
	router.DELETE("/clients/:clientId/ips/:ipId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveIP(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/ips/"+ipID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestClientHandler_GenerateAPIKey_ServiceError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("GenerateAPIKey", mock.Anything, clientID, mock.Anything, user.ID).Return(nil, errors.New("already has key"))

	router := gin.New()
	router.POST("/clients/:clientId/api-key", func(c *gin.Context) {
		c.Set("user", user)
		h.GenerateAPIKey(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/api-key", nil)
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_GenerateAPIKey_InvalidID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	router := gin.New()
	router.POST("/clients/:clientId/api-key", func(c *gin.Context) {
		c.Set("user", user)
		h.GenerateAPIKey(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/clients/bad-id/api-key", nil)
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_RevokeAPIKey_ServiceError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("RevokeAPIKey", mock.Anything, clientID).Return(errors.New("no key to revoke"))

	router := gin.New()
	router.DELETE("/clients/:clientId/api-key", func(c *gin.Context) {
		c.Set("user", user)
		h.RevokeAPIKey(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/api-key", nil)
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_RevokeAPIKey_InvalidID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	router := gin.New()
	router.DELETE("/clients/:clientId/api-key", func(c *gin.Context) {
		c.Set("user", user)
		h.RevokeAPIKey(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("DELETE", "/clients/bad-id/api-key", nil)
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_RevokeAPIKey_NoUser(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	router := gin.New()
	router.DELETE("/clients/:clientId/api-key", func(c *gin.Context) {
		h.RevokeAPIKey(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("DELETE", "/clients/"+uuid.New().String()+"/api-key", nil)
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientHandler_ConfigureJWT_BadBody(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)

	body := []byte("{invalid")

	router := gin.New()
	router.POST("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.ConfigureJWT(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/jwt", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_ConfigureJWT_ServiceError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("ConfigureJWT", mock.Anything, clientID, mock.Anything, user.ID).Return(nil, errors.New("invalid issuer"))

	body, _ := json.Marshal(map[string]interface{}{
		"issuer":  "https://example.com",
		"jwksUrl": "https://example.com/.well-known/jwks.json",
	})

	router := gin.New()
	router.POST("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.ConfigureJWT(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/jwt", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_ConfigureJWT_InternalError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("ConfigureJWT", mock.Anything, clientID, mock.Anything, user.ID).Return(nil, errors.New("internal: db error"))

	body, _ := json.Marshal(map[string]interface{}{
		"issuer":  "https://example.com",
		"jwksUrl": "https://example.com/.well-known/jwks.json",
	})

	router := gin.New()
	router.POST("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.ConfigureJWT(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/clients/"+clientID.String()+"/jwt", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_RemoveJWT_ServiceError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("RemoveJWT", mock.Anything, clientID).Return(errors.New("jwt not configured"))

	router := gin.New()
	router.DELETE("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveJWT(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/jwt", nil)
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_RemoveJWT_InternalError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("RemoveJWT", mock.Anything, clientID).Return(errors.New("internal: db error"))

	router := gin.New()
	router.DELETE("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveJWT(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/jwt", nil)
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_RemoveJWT_NoUser(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/clients/"+uuid.New().String()+"/jwt", nil)
	c.Params = gin.Params{{Key: "clientId", Value: uuid.New().String()}}

	h.RemoveJWT(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientHandler_RemoveJWT_InvalidID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	router := gin.New()
	router.DELETE("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveJWT(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("DELETE", "/clients/bad-id/jwt", nil)
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_ConfigureJWT_NoUser(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/clients/"+uuid.New().String()+"/jwt", nil)
	c.Params = gin.Params{{Key: "clientId", Value: uuid.New().String()}}

	h.ConfigureJWT(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestClientHandler_ConfigureJWT_InvalidID(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()

	router := gin.New()
	router.POST("/clients/:clientId/jwt", func(c *gin.Context) {
		c.Set("user", user)
		h.ConfigureJWT(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/clients/bad-id/jwt", nil)
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClientHandler_Create_ServiceError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	teamID := uuid.New()
	mockClient.On("Create", mock.Anything, user.ID).Return(nil, errors.New("duplicate name"))

	body, _ := json.Marshal(map[string]interface{}{
		"name":   "new-client",
		"teamId": teamID.String(),
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/clients", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_Update_ServiceError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockClient.On("Update", clientID, mock.Anything).Return(nil, errors.New("validation error"))

	body, _ := json.Marshal(map[string]interface{}{"name": "updated-name"})

	router := gin.New()
	router.PUT("/clients/:clientId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("PUT", "/clients/"+clientID.String(), bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_UpdateClientMTLS_ServiceError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockTeamRepo.On("IsMember", teamID, user.ID).Return(true, nil)
	mockClient.On("UpdateClientMTLS", mock.Anything, clientID, mock.Anything, user.ID).Return(nil, errors.New("invalid CA"))

	body, _ := json.Marshal(map[string]interface{}{"enabled": true, "caName": "my-ca", "caPem": "bad-cert"})

	router := gin.New()
	router.PUT("/clients/:clientId/mtls", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateClientMTLS(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("PUT", "/clients/"+clientID.String()+"/mtls", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_DeleteClientMTLS_ServiceError(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := testUser()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockTeamRepo.On("IsMember", teamID, user.ID).Return(true, nil)
	mockClient.On("UpdateClientMTLS", mock.Anything, clientID, mock.Anything, user.ID).Return(nil, errors.New("mtls error"))

	router := gin.New()
	router.DELETE("/clients/:clientId/mtls", func(c *gin.Context) {
		c.Set("user", user)
		h.DeleteClientMTLS(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/mtls", nil)
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockClient.AssertExpectations(t)
}

func TestClientHandler_DeleteClientMTLS_NotMember(t *testing.T) {
	mockClient := new(mocks.MockClientService)
	mockAudit := new(mocks.MockAuditService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	h := handlers.NewClientHandler(mockClient, mockAudit, mockTeamRepo)

	user := &models.User{ID: uuid.New(), Role: models.UserRoleUser}
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, Name: "client1", TeamID: teamID}
	mockClient.On("GetByID", clientID).Return(client, nil)
	mockTeamRepo.On("IsMember", teamID, user.ID).Return(false, nil)

	router := gin.New()
	router.DELETE("/clients/:clientId/mtls", func(c *gin.Context) {
		c.Set("user", user)
		h.DeleteClientMTLS(c)
	})

	w := httptest.NewRecorder()
	req2, _ := http.NewRequest("DELETE", "/clients/"+clientID.String()+"/mtls", nil)
	router.ServeHTTP(w, req2)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
