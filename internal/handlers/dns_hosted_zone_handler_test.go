package handlers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// withCurrentUser mirrors how auth middleware stores the authenticated user
// in the gin context (see middleware.GetCurrentUser), for handler tests that
// bypass the middleware chain.
func withCurrentUser(c *gin.Context, user *models.User) {
	c.Set("user", user)
}

func TestDNSHostedZoneHandler_Create_Success(t *testing.T) {
	mockSvc := new(mocks.MockDNSHostedZoneService)
	h := handlers.NewDNSHostedZoneHandler(mockSvc)

	credID := uuid.New()
	userID := uuid.New()
	zone := &models.DNSHostedZone{
		ID:                   uuid.New(),
		Name:                 "example.com",
		ProviderCredentialID: credID,
		Status:               models.DNSZoneStatusReady,
		CreatedBy:            userID,
	}
	mockSvc.On("Create", "example.com", credID, userID).Return(zone, nil)

	body, _ := json.Marshal(map[string]string{
		"name":                 "example.com",
		"providerCredentialId": credID.String(),
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/dns/zones", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	withCurrentUser(c, &models.User{ID: userID})

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	respBody := w.Body.String()
	assert.Contains(t, respBody, "example.com")
	assert.Contains(t, respBody, zone.ID.String())
	assert.NotContains(t, respBody, "providerZoneId")
	mockSvc.AssertExpectations(t)
}

func TestDNSHostedZoneHandler_Create_InvalidBody(t *testing.T) {
	mockSvc := new(mocks.MockDNSHostedZoneService)
	h := handlers.NewDNSHostedZoneHandler(mockSvc)

	// Missing required fields.
	body, _ := json.Marshal(map[string]string{"name": "example.com"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/dns/zones", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	withCurrentUser(c, &models.User{ID: uuid.New()})

	h.Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSvc.AssertExpectations(t)
}

func TestDNSHostedZoneHandler_Delete_InUse_Returns409(t *testing.T) {
	mockSvc := new(mocks.MockDNSHostedZoneService)
	h := handlers.NewDNSHostedZoneHandler(mockSvc)

	id := uuid.New()
	mockSvc.On("Delete", id).Return(services.ErrDNSHostedZoneInUse)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/dns/zones/"+id.String(), nil)
	c.Params = gin.Params{{Key: "hostedZoneId", Value: id.String()}}

	h.Delete(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	mockSvc.AssertExpectations(t)
}

func TestDNSHostedZoneHandler_Delete_UnexpectedError_Returns500(t *testing.T) {
	mockSvc := new(mocks.MockDNSHostedZoneService)
	h := handlers.NewDNSHostedZoneHandler(mockSvc)

	id := uuid.New()
	mockSvc.On("Delete", id).Return(errors.New("db exploded"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("DELETE", "/dns/zones/"+id.String(), nil)
	c.Params = gin.Params{{Key: "hostedZoneId", Value: id.String()}}

	h.Delete(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockSvc.AssertExpectations(t)
}

func TestDNSHostedZoneHandler_List_Success(t *testing.T) {
	mockSvc := new(mocks.MockDNSHostedZoneService)
	h := handlers.NewDNSHostedZoneHandler(mockSvc)

	zones := []models.DNSHostedZone{
		{ID: uuid.New(), Name: "example.com", Status: models.DNSZoneStatusReady},
		{ID: uuid.New(), Name: "example.org", Status: models.DNSZoneStatusError, StatusMessage: "zone not found at provider"},
	}
	mockSvc.On("List").Return(zones, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/dns/zones", nil)

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "example.com")
	assert.Contains(t, body, "example.org")
	mockSvc.AssertExpectations(t)
}

func TestDNSHostedZoneHandler_Get_NotFound(t *testing.T) {
	mockSvc := new(mocks.MockDNSHostedZoneService)
	h := handlers.NewDNSHostedZoneHandler(mockSvc)

	id := uuid.New()
	mockSvc.On("GetByID", id).Return(nil, services.ErrHostedZoneNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/dns/zones/"+id.String(), nil)
	c.Params = gin.Params{{Key: "hostedZoneId", Value: id.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockSvc.AssertExpectations(t)
}

func TestDNSHostedZoneHandler_Get_InvalidID(t *testing.T) {
	mockSvc := new(mocks.MockDNSHostedZoneService)
	h := handlers.NewDNSHostedZoneHandler(mockSvc)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/dns/zones/not-a-uuid", nil)
	c.Params = gin.Params{{Key: "hostedZoneId", Value: "not-a-uuid"}}

	h.Get(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSvc.AssertExpectations(t)
}

// Note: a 403-without-"owner"-role case is enforced by the
// RequireRole("owner") route-group middleware registered in cmd/server/main.go,
// not by DNSHostedZoneHandler itself. A direct handler unit test (as above)
// never passes through that middleware, so it can't exercise the 403 path --
// skipped here rather than writing a vacuous test.
