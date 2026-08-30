package middleware

import (
	"net/http"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
)

// AuthMiddleware handles authentication
type AuthMiddleware struct {
	authService *services.AuthService
}

// NewAuthMiddleware creates a new auth middleware
func NewAuthMiddleware(authService *services.AuthService) *AuthMiddleware {
	return &AuthMiddleware{
		authService: authService,
	}
}

// Authenticate validates the JWT token and sets the user in context
func (m *AuthMiddleware) Authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
			c.Abort()
			return
		}

		// Check for Bearer token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization header format"})
			c.Abort()
			return
		}

		token := parts[1]

		// Validate JWT token
		authMethod := "jwt"
		user, err := m.authService.ValidateToken(token)
		if err != nil {
			// Try API token
			user, err = m.authService.ValidateAPIToken(token)
			if err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
				c.Abort()
				return
			}
			authMethod = "api_token"
		}

		// Check if user is active
		if !user.IsActive {
			c.JSON(http.StatusForbidden, gin.H{"error": "User account is disabled"})
			c.Abort()
			return
		}

		// Set user and auth method in context
		c.Set("user", user)
		c.Set("userId", user.ID.String())
		c.Set("authMethod", authMethod)

		c.Next()
	}
}

// RequireRole checks if the user has one of the required roles
func (m *AuthMiddleware) RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, exists := c.Get("user")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in context"})
			c.Abort()
			return
		}

		u := user.(*models.User)

		for _, role := range roles {
			if string(u.Role) == role {
				c.Next()
				return
			}
		}

		c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
		c.Abort()
	}
}

// GetCurrentUser returns the current user from context
func GetCurrentUser(c *gin.Context) *models.User {
	user, exists := c.Get("user")
	if !exists {
		return nil
	}
	return user.(*models.User)
}

// GetAuthMethod returns how the current user authenticated ("jwt", "api_token")
func GetAuthMethod(c *gin.Context) string {
	method, exists := c.Get("authMethod")
	if !exists {
		return "jwt"
	}
	return method.(string)
}

// AuditDetails returns audit log details including the auth method
func AuditDetails(c *gin.Context) models.AuditDetails {
	return models.AuditDetails{
		"authMethod": GetAuthMethod(c),
	}
}
