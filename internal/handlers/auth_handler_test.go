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

func TestAuthHandler_Login_Success(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	resp := &services.LoginResponse{
		AccessToken:  "token123",
		RefreshToken: "refresh123",
		User:         &models.User{ID: uuid.New(), Username: "admin"},
	}
	mockAuth.On("Login", "admin", "pass123").Return(resp, nil)

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "pass123"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/login", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Login(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	assert.Equal(t, "token123", result["accessToken"])
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_Login_WrongCredentials(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	mockAuth.On("Login", "admin", "wrong").Return(nil, errors.New("invalid credentials"))

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "wrong"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/login", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Login(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_GetCurrentUser_Success(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin", Email: "admin@test.com", Role: "owner", IsActive: true}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/auth/me", nil)
	c.Set("user", user)

	h.GetCurrentUser(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	assert.Equal(t, "admin", result["username"])
}

func TestAuthHandler_CreateAPIToken_Success(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}
	token := &models.APIToken{ID: uuid.New(), Name: "my-token", UserID: user.ID}
	mockAuth.On("CreateAPIToken", user.ID, "my-token", mock.Anything).Return(token, "raw-token-value", nil)

	body, _ := json.Marshal(map[string]string{"name": "my-token"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/api-tokens", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.CreateAPIToken(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	assert.Equal(t, "raw-token-value", result["token"])
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_ChangePassword_Success(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}
	mockAuth.On("ChangePassword", user.ID, "oldpass12", "newpass12").Return(nil)

	body, _ := json.Marshal(map[string]string{"currentPassword": "oldpass12", "newPassword": "newpass12"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/change-password", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.ChangePassword(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_ListAPITokens_Success(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}
	tokens := []models.APIToken{
		{ID: uuid.New(), Name: "token1", UserID: user.ID},
		{ID: uuid.New(), Name: "token2", UserID: user.ID},
	}
	mockAuth.On("ListAPITokens", user.ID).Return(tokens, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/auth/api-tokens", nil)
	c.Set("user", user)

	h.ListAPITokens(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var result []interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	assert.Len(t, result, 2)
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_RevokeAPIToken_Success(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}
	tokenID := uuid.New()
	mockAuth.On("RevokeAPIToken", tokenID, user.ID).Return(nil)

	router := gin.New()
	router.DELETE("/auth/api-tokens/:tokenId", func(c *gin.Context) {
		c.Set("user", user)
		h.RevokeAPIToken(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/auth/api-tokens/"+tokenID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_Logout_Success(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	router := gin.New()
	router.POST("/auth/logout", func(c *gin.Context) {
		h.Logout(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/logout", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAuthHandler_RefreshToken_Success(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	resp := &services.LoginResponse{
		AccessToken:  "new-token",
		RefreshToken: "new-refresh",
		User:         &models.User{ID: uuid.New(), Username: "admin"},
	}
	mockAuth.On("RefreshToken", "old-refresh-token").Return(resp, nil)

	body, _ := json.Marshal(map[string]string{"refreshToken": "old-refresh-token"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/refresh", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.RefreshToken(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_ChangePassword_BadBody(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}

	// Missing required fields
	body, _ := json.Marshal(map[string]string{"currentPassword": "old"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/change-password", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.ChangePassword(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthHandler_GetAPITokenCapabilities_Success(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}
	mockAuth.On("CountAPITokens", user.ID).Return(int64(3), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/auth/api-tokens/capabilities", nil)
	c.Set("user", user)

	h.GetAPITokenCapabilities(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	assert.Equal(t, true, result["enabled"])
	assert.Equal(t, float64(10), result["maxTokens"])
	assert.Equal(t, float64(3), result["currentCount"])
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_GetAPITokenCapabilities_NoUser(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/auth/api-tokens/capabilities", nil)
	// No user set

	h.GetAPITokenCapabilities(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthHandler_Login_BadBody(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	// Missing password field
	body, _ := json.Marshal(map[string]string{"username": "admin"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/login", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Login(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthHandler_RefreshToken_InvalidToken(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	mockAuth.On("RefreshToken", "bad-token").Return(nil, errors.New("invalid or expired refresh token"))

	body, _ := json.Marshal(map[string]string{"refreshToken": "bad-token"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/refresh", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.RefreshToken(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_RefreshToken_BadBody(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	// Empty body
	body, _ := json.Marshal(map[string]string{})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/refresh", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.RefreshToken(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthHandler_GetCurrentUser_NoUser(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/auth/me", nil)
	// No user set

	h.GetCurrentUser(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthHandler_CreateAPIToken_NoUser(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	body, _ := json.Marshal(map[string]string{"name": "my-token"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/api-tokens", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	// No user set

	h.CreateAPIToken(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthHandler_CreateAPIToken_BadBody(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}

	// Missing required name field
	body, _ := json.Marshal(map[string]string{})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/api-tokens", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.CreateAPIToken(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthHandler_CreateAPIToken_ServiceError(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}
	mockAuth.On("CreateAPIToken", user.ID, "my-token", mock.Anything).Return(nil, "", errors.New("internal error"))

	body, _ := json.Marshal(map[string]string{"name": "my-token"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/api-tokens", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.CreateAPIToken(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_CreateAPIToken_MaxTokensError(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}
	mockAuth.On("CreateAPIToken", user.ID, "my-token", mock.Anything).Return(nil, "", errors.New("maximum number of API tokens reached"))

	body, _ := json.Marshal(map[string]string{"name": "my-token"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/api-tokens", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.CreateAPIToken(c)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_RevokeAPIToken_InvalidID(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}

	router := gin.New()
	router.DELETE("/auth/api-tokens/:tokenId", func(c *gin.Context) {
		c.Set("user", user)
		h.RevokeAPIToken(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/auth/api-tokens/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthHandler_RevokeAPIToken_NoUser(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	router := gin.New()
	router.DELETE("/auth/api-tokens/:tokenId", func(c *gin.Context) {
		// No user set
		h.RevokeAPIToken(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/auth/api-tokens/"+uuid.New().String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthHandler_RevokeAPIToken_ServiceError(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}
	tokenID := uuid.New()
	mockAuth.On("RevokeAPIToken", tokenID, user.ID).Return(errors.New("token not found"))

	router := gin.New()
	router.DELETE("/auth/api-tokens/:tokenId", func(c *gin.Context) {
		c.Set("user", user)
		h.RevokeAPIToken(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/auth/api-tokens/"+tokenID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_ChangePassword_WrongOldPassword(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}
	mockAuth.On("ChangePassword", user.ID, "wrongpass", "newpass12").Return(errors.New("current password is incorrect"))

	body, _ := json.Marshal(map[string]string{"currentPassword": "wrongpass", "newPassword": "newpass12"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/change-password", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.ChangePassword(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_ChangePassword_NoUser(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	body, _ := json.Marshal(map[string]string{"currentPassword": "oldpass12", "newPassword": "newpass12"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/auth/change-password", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	// No user set

	h.ChangePassword(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthHandler_ListAPITokens_NoUser(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/auth/api-tokens", nil)
	// No user set

	h.ListAPITokens(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthHandler_GetAPITokenCapabilities_CountError(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}
	mockAuth.On("CountAPITokens", user.ID).Return(int64(0), errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/auth/api-tokens/capabilities", nil)
	c.Set("user", user)

	h.GetAPITokenCapabilities(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	// When count errors, currentCount should be 0
	assert.Equal(t, float64(0), result["currentCount"])
	mockAuth.AssertExpectations(t)
}

func TestAuthHandler_ListAPITokens_ServiceError(t *testing.T) {
	mockAuth := new(mocks.MockAuthService)
	h := handlers.NewAuthHandler(mockAuth)

	user := &models.User{ID: uuid.New(), Username: "admin"}
	mockAuth.On("ListAPITokens", user.ID).Return([]models.APIToken{}, errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/auth/api-tokens", nil)
	c.Set("user", user)

	h.ListAPITokens(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockAuth.AssertExpectations(t)
}
