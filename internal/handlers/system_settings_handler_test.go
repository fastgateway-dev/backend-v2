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
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestSystemSettingsHandler_Get_Success(t *testing.T) {
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSystemSettingsHandler(mockSettings)

	resp := &services.SystemSettingsResponse{
		BaseURL:            "http://localhost:3000",
		JWTExpiry:          "24h",
		RefreshTokenExpiry: "168h",
		LogLevel:           "info",
	}
	mockSettings.On("GetResponse").Return(resp, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/system/settings", nil)

	h.Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	assert.Equal(t, "http://localhost:3000", result["baseUrl"])
	mockSettings.AssertExpectations(t)
}

func TestSystemSettingsHandler_Update_Success(t *testing.T) {
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSystemSettingsHandler(mockSettings)

	input := services.SystemSettingsInput{
		BaseURL:  "http://example.com",
		LogLevel: "debug",
	}
	resp := &services.SystemSettingsResponse{
		BaseURL:  "http://example.com",
		LogLevel: "debug",
	}
	mockSettings.On("Update", input).Return(resp, nil)

	body, _ := json.Marshal(input)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/system/settings", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Update(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	assert.Equal(t, "http://example.com", result["baseUrl"])
	mockSettings.AssertExpectations(t)
}

func TestSystemSettingsHandler_Get_Error(t *testing.T) {
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSystemSettingsHandler(mockSettings)

	mockSettings.On("GetResponse").Return(nil, errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/system/settings", nil)

	h.Get(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockSettings.AssertExpectations(t)
}

func TestSystemSettingsHandler_Update_BadBody(t *testing.T) {
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSystemSettingsHandler(mockSettings)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/system/settings", bytes.NewReader([]byte("invalid")))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Update(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSystemSettingsHandler_Update_ServiceError(t *testing.T) {
	mockSettings := new(mocks.MockSystemSettingsService)
	h := handlers.NewSystemSettingsHandler(mockSettings)

	input := services.SystemSettingsInput{
		BaseURL:  "http://example.com",
		LogLevel: "invalid-level",
	}
	mockSettings.On("Update", input).Return(nil, errors.New("invalid log level"))

	body, _ := json.Marshal(input)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/system/settings", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Update(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockSettings.AssertExpectations(t)
}
