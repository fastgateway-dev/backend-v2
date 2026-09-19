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
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

func TestDNSCredentialHandler_Get_Found(t *testing.T) {
	mockSvc := new(mocks.MockDNSCredentialService)
	h := handlers.NewDNSCredentialHandler(mockSvc)

	id := uuid.New()
	cred := &models.DNSProviderCredential{
		ID:           id,
		Name:         "cf-prod",
		ProviderType: "cloudflare",
		Credentials:  models.DNSCredentialData{"apiToken": "super-secret-ciphertext"},
	}
	mockSvc.On("GetByID", id).Return(cred, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/dns/credentials/"+id.String(), nil)
	c.Params = gin.Params{{Key: "dnsCredentialId", Value: id.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, id.String())
	assert.Contains(t, body, "cf-prod")
	assert.Contains(t, body, "cloudflare")
	assert.NotContains(t, body, "credentials")
	assert.NotContains(t, body, "super-secret-ciphertext")
	mockSvc.AssertExpectations(t)
}

func TestDNSCredentialHandler_Get_NotFound(t *testing.T) {
	mockSvc := new(mocks.MockDNSCredentialService)
	h := handlers.NewDNSCredentialHandler(mockSvc)

	id := uuid.New()
	mockSvc.On("GetByID", id).Return(nil, gorm.ErrRecordNotFound)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/dns/credentials/"+id.String(), nil)
	c.Params = gin.Params{{Key: "dnsCredentialId", Value: id.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockSvc.AssertExpectations(t)
}

func TestDNSCredentialHandler_Get_InvalidID(t *testing.T) {
	mockSvc := new(mocks.MockDNSCredentialService)
	h := handlers.NewDNSCredentialHandler(mockSvc)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/dns/credentials/not-a-uuid", nil)
	c.Params = gin.Params{{Key: "dnsCredentialId", Value: "not-a-uuid"}}

	h.Get(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSvc.AssertExpectations(t)
}

func TestDNSCredentialHandler_Update_Success(t *testing.T) {
	mockSvc := new(mocks.MockDNSCredentialService)
	h := handlers.NewDNSCredentialHandler(mockSvc)

	id := uuid.New()
	updated := &models.DNSProviderCredential{
		ID:           id,
		Name:         "cf-prod-renamed",
		ProviderType: "cloudflare",
		Credentials:  models.DNSCredentialData{"apiToken": "new-ciphertext"},
	}
	mockSvc.On("Update", id, mock.AnythingOfType("*services.UpdateDNSCredentialInput")).Return(updated, nil)

	body, _ := json.Marshal(services.UpdateDNSCredentialInput{Name: "cf-prod-renamed"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PATCH", "/dns/credentials/"+id.String(), bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "dnsCredentialId", Value: id.String()}}

	h.Update(c)

	assert.Equal(t, http.StatusOK, w.Code)
	respBody := w.Body.String()
	assert.Contains(t, respBody, "cf-prod-renamed")
	assert.NotContains(t, respBody, "new-ciphertext")
	mockSvc.AssertExpectations(t)
}

func TestDNSCredentialHandler_Update_NotFound(t *testing.T) {
	mockSvc := new(mocks.MockDNSCredentialService)
	h := handlers.NewDNSCredentialHandler(mockSvc)

	id := uuid.New()
	mockSvc.On("Update", id, mock.AnythingOfType("*services.UpdateDNSCredentialInput")).Return(nil, gorm.ErrRecordNotFound)

	body, _ := json.Marshal(services.UpdateDNSCredentialInput{Name: "whatever"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PATCH", "/dns/credentials/"+id.String(), bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "dnsCredentialId", Value: id.String()}}

	h.Update(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockSvc.AssertExpectations(t)
}

func TestDNSCredentialHandler_Update_InvalidID(t *testing.T) {
	mockSvc := new(mocks.MockDNSCredentialService)
	h := handlers.NewDNSCredentialHandler(mockSvc)

	body, _ := json.Marshal(services.UpdateDNSCredentialInput{Name: "whatever"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PATCH", "/dns/credentials/not-a-uuid", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "dnsCredentialId", Value: "not-a-uuid"}}

	h.Update(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSvc.AssertExpectations(t)
}
