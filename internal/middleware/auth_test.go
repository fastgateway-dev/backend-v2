package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestGetCurrentUser_WithUser(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	user := &models.User{
		ID:       uuid.New(),
		Username: "testuser",
		Email:    "test@example.com",
		Role:     models.UserRoleUser,
		IsActive: true,
	}
	c.Set("user", user)

	result := GetCurrentUser(c)
	assert.NotNil(t, result)
	assert.Equal(t, user.ID, result.ID)
	assert.Equal(t, "testuser", result.Username)
}

func TestGetCurrentUser_WithoutUser(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	result := GetCurrentUser(c)
	assert.Nil(t, result)
}

func TestGetAuthMethod_WithMethod(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	c.Set("authMethod", "api_token")

	result := GetAuthMethod(c)
	assert.Equal(t, "api_token", result)
}

func TestGetAuthMethod_WithoutMethod(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	result := GetAuthMethod(c)
	assert.Equal(t, "jwt", result)
}

func TestIsOwner_OwnerRole(t *testing.T) {
	user := &models.User{
		ID:   uuid.New(),
		Role: models.UserRoleOwner,
	}
	assert.True(t, IsOwner(user))
}

func TestIsOwner_UserRole(t *testing.T) {
	user := &models.User{
		ID:   uuid.New(),
		Role: models.UserRoleUser,
	}
	assert.False(t, IsOwner(user))
}

func TestAuditDetails(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	c.Set("authMethod", "api_token")

	details := AuditDetails(c)
	assert.Equal(t, "api_token", details["authMethod"])
}

func TestAuditDetails_DefaultMethod(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	details := AuditDetails(c)
	assert.Equal(t, "jwt", details["authMethod"])
}

func TestAuthenticate_NoAuthHeader(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)

	// AuthMiddleware with nil authService - we won't reach the service call
	m := &AuthMiddleware{}
	handler := m.Authenticate()
	handler(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, c.IsAborted())
}

func TestAuthenticate_InvalidHeaderFormat(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)
	c.Request.Header.Set("Authorization", "InvalidFormat")

	m := &AuthMiddleware{}
	handler := m.Authenticate()
	handler(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, c.IsAborted())
}

func TestRequireRole_CorrectRole(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)

	user := &models.User{
		ID:   uuid.New(),
		Role: models.UserRoleOwner,
	}
	c.Set("user", user)

	m := &AuthMiddleware{}
	handler := m.RequireRole("owner")
	handler(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, c.IsAborted())
}

func TestRequireRole_WrongRole(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)

	user := &models.User{
		ID:   uuid.New(),
		Role: models.UserRoleUser,
	}
	c.Set("user", user)

	m := &AuthMiddleware{}
	handler := m.RequireRole("owner")
	handler(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.True(t, c.IsAborted())
}

func TestRequireRole_NoUser(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)

	m := &AuthMiddleware{}
	handler := m.RequireRole("owner")
	handler(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, c.IsAborted())
}

func TestRequireRole_MultipleRoles(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)

	user := &models.User{
		ID:   uuid.New(),
		Role: models.UserRoleUser,
	}
	c.Set("user", user)

	m := &AuthMiddleware{}
	handler := m.RequireRole("owner", "user")
	handler(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, c.IsAborted())
}

func TestAuthenticate_BearerTokenWithSpace(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)
	c.Request.Header.Set("Authorization", "Basic sometoken")

	m := &AuthMiddleware{}
	handler := m.Authenticate()
	handler(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, c.IsAborted())
}

func TestAuthenticate_EmptyBearer(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)
	c.Request.Header.Set("Authorization", "Bearer")

	m := &AuthMiddleware{}
	handler := m.Authenticate()
	handler(c)

	// "Bearer" without a space-separated token -> parts length is 1
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, c.IsAborted())
}

func TestNewAuthMiddleware(t *testing.T) {
	m := NewAuthMiddleware(nil)
	assert.NotNil(t, m)
	assert.Nil(t, m.authService)
}
