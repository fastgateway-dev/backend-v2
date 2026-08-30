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
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestSSOHandler_GetPublicConfig_Success(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	config := &services.SSOPublicConfig{
		Enabled:      true,
		ProviderName: "Okta",
	}
	mockSSO.On("GetPublicConfig").Return(config, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/auth/sso/config", nil)

	h.GetPublicConfig(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, true, resp["enabled"])
	assert.Equal(t, "Okta", resp["providerName"])
	mockSSO.AssertExpectations(t)
}

func TestSSOHandler_GetPublicConfig_Error(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	mockSSO.On("GetPublicConfig").Return(nil, errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/auth/sso/config", nil)

	h.GetPublicConfig(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockSSO.AssertExpectations(t)
}

func TestSSOHandler_GetConfig_Success(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	config := &models.SSOConfig{
		ID:           uuid.New(),
		Enabled:      true,
		ProviderName: "Okta",
		IssuerURL:    "https://okta.example.com",
	}
	mockSSO.On("GetConfig").Return(config, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/auth/sso/admin-config", nil)

	h.GetConfig(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockSSO.AssertExpectations(t)
}

func TestSSOHandler_GetConfig_Error(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	mockSSO.On("GetConfig").Return(nil, errors.New("forbidden"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/auth/sso/admin-config", nil)

	h.GetConfig(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockSSO.AssertExpectations(t)
}

func TestSSOHandler_UpdateConfig_Success(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	input := services.SSOConfigInput{
		ProviderName: "Okta",
		IssuerURL:    "https://okta.example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}
	config := &models.SSOConfig{
		ID:           uuid.New(),
		Enabled:      true,
		ProviderName: "Okta",
	}
	mockSSO.On("UpdateConfig", input).Return(config, nil)

	body, _ := json.Marshal(input)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/auth/sso/admin-config", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateConfig(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockSSO.AssertExpectations(t)
}

func TestSSOHandler_UpdateConfig_InvalidBody(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/auth/sso/admin-config", bytes.NewReader([]byte("{}")))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateConfig(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSSOHandler_UpdateConfig_ServiceError(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	mockSSO.On("UpdateConfig", mock.AnythingOfType("SSOConfigInput")).Return(nil, errors.New("invalid config"))

	body, _ := json.Marshal(services.SSOConfigInput{
		ProviderName: "Okta",
		IssuerURL:    "https://okta.example.com",
		ClientID:     "id",
		ClientSecret: "secret",
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/auth/sso/admin-config", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.UpdateConfig(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSSO.AssertExpectations(t)
}

func TestSSOHandler_DisableSSO_Success(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	mockSSO.On("DisableSSO").Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/sso/disable", nil)

	h.DisableSSO(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "SSO disabled", resp["message"])
	mockSSO.AssertExpectations(t)
}

func TestSSOHandler_DisableSSO_Error(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	mockSSO.On("DisableSSO").Return(errors.New("failed"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/sso/disable", nil)

	h.DisableSSO(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSSO.AssertExpectations(t)
}

func TestSSOHandler_Authorize_Success(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	mockSettings.On("GetBaseURL").Return("")
	mockSSO.On("GetAuthorizeURL", "http://localhost/api/v1/auth/sso/callback").Return("https://idp.example.com/authorize?client_id=abc", nil)

	router := gin.New()
	router.GET("/auth/sso/authorize", func(c *gin.Context) {
		h.Authorize(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/auth/sso/authorize", nil)
	req.Host = "localhost"
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "idp.example.com")
	mockSSO.AssertExpectations(t)
}

func TestSSOHandler_Authorize_Error(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	mockSettings.On("GetBaseURL").Return("")
	mockSSO.On("GetAuthorizeURL", mock.Anything).Return("", errors.New("SSO not configured"))

	router := gin.New()
	router.GET("/auth/sso/authorize", func(c *gin.Context) {
		h.Authorize(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/auth/sso/authorize", nil)
	req.Host = "localhost"
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSSO.AssertExpectations(t)
}

func TestSSOHandler_Callback_ErrorParam(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	mockSettings.On("GetBaseURL").Return("")

	router := gin.New()
	router.GET("/auth/sso/callback", func(c *gin.Context) {
		h.Callback(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/auth/sso/callback?error=access_denied&error_description=User+denied+access", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "login?error=")
}

func TestSSOHandler_Callback_MissingCode(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	mockSettings.On("GetBaseURL").Return("")

	router := gin.New()
	router.GET("/auth/sso/callback", func(c *gin.Context) {
		h.Callback(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/auth/sso/callback", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "missing+authorization+code")
}

func TestSSOHandler_Callback_Success(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	mockSettings.On("GetBaseURL").Return("http://localhost:3000")
	result := &services.SSOCallbackResult{
		AccessToken:  "access-token-123",
		RefreshToken: "refresh-token-456",
	}
	mockSSO.On("HandleCallback", mock.Anything, "auth-code", "state-value", "http://localhost:3000/api/v1/auth/sso/callback").Return(result, nil)

	router := gin.New()
	router.GET("/auth/sso/callback", func(c *gin.Context) {
		h.Callback(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/auth/sso/callback?code=auth-code&state=state-value", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	loc := w.Header().Get("Location")
	assert.Contains(t, loc, "login/callback")
	assert.Contains(t, loc, "accessToken=access-token-123")
	assert.Contains(t, loc, "refreshToken=refresh-token-456")
	mockSSO.AssertExpectations(t)
}

func TestSSOHandler_Callback_HandleCallbackError(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	mockSettings.On("GetBaseURL").Return("")
	mockSSO.On("HandleCallback", mock.Anything, "bad-code", "", mock.Anything).Return(nil, errors.New("invalid code"))

	router := gin.New()
	router.GET("/auth/sso/callback", func(c *gin.Context) {
		h.Callback(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/auth/sso/callback?code=bad-code", nil)
	req.Host = "localhost"
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "login?error=")
	mockSSO.AssertExpectations(t)
}

func TestSSOHandler_Authorize_WithBaseURL(t *testing.T) {
	mockSSO := new(mocks.MockSSOService)
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSSOHandler(mockSSO, mockSettings, "http://localhost:3000")

	mockSettings.On("GetBaseURL").Return("https://gateway.example.com")
	mockSSO.On("GetAuthorizeURL", "https://gateway.example.com/api/v1/auth/sso/callback").Return("https://idp.example.com/authorize", nil)

	router := gin.New()
	router.GET("/auth/sso/authorize", func(c *gin.Context) {
		h.Authorize(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/auth/sso/authorize", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	mockSSO.AssertExpectations(t)
}
