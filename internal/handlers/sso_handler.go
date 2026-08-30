package handlers

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
)

// SSOHandler handles SSO/OIDC endpoints
type SSOHandler struct {
	ssoService      services.SSOServiceInterface
	settingsService services.SystemSettingsServiceInterface
	frontendURL     string // fallback if settings service unavailable
}

// NewSSOHandler creates a new SSO handler
func NewSSOHandler(ssoService services.SSOServiceInterface, settingsService services.SystemSettingsServiceInterface, frontendURL string) *SSOHandler {
	return &SSOHandler{
		ssoService:      ssoService,
		settingsService: settingsService,
		frontendURL:     frontendURL,
	}
}

// GetPublicConfig returns SSO status for the login page (public, no auth required)
func (h *SSOHandler) GetPublicConfig(c *gin.Context) {
	config, err := h.ssoService.GetPublicConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, config)
}

// GetConfig returns full SSO config for admin (owner only)
func (h *SSOHandler) GetConfig(c *gin.Context) {
	config, err := h.ssoService.GetConfig()
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, config)
}

// UpdateConfig updates SSO config (owner only)
func (h *SSOHandler) UpdateConfig(c *gin.Context) {
	var input services.SSOConfigInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	config, err := h.ssoService.UpdateConfig(input)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, config)
}

// DisableSSO disables SSO (owner only)
func (h *SSOHandler) DisableSSO(c *gin.Context) {
	if err := h.ssoService.DisableSSO(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "SSO disabled"})
}

// Authorize redirects to the IdP's authorization endpoint (public)
func (h *SSOHandler) Authorize(c *gin.Context) {
	callbackURL := fmt.Sprintf("%s/api/v1/auth/sso/callback", h.getBaseURL(c))

	authURL, err := h.ssoService.GetAuthorizeURL(callbackURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.Redirect(http.StatusFound, authURL)
}

// Callback handles the IdP callback (public)
func (h *SSOHandler) Callback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	errParam := c.Query("error")

	if errParam != "" {
		errDesc := c.Query("error_description")
		redirectURL := fmt.Sprintf("%s/login?error=%s", h.getFrontendURL(), url.QueryEscape(errDesc))
		c.Redirect(http.StatusFound, redirectURL)
		return
	}

	if code == "" {
		redirectURL := fmt.Sprintf("%s/login?error=%s", h.getFrontendURL(), url.QueryEscape("missing authorization code"))
		c.Redirect(http.StatusFound, redirectURL)
		return
	}

	callbackURL := fmt.Sprintf("%s/api/v1/auth/sso/callback", h.getBaseURL(c))

	result, err := h.ssoService.HandleCallback(c.Request.Context(), code, state, callbackURL)
	if err != nil {
		redirectURL := fmt.Sprintf("%s/login?error=%s", h.getFrontendURL(), url.QueryEscape(err.Error()))
		c.Redirect(http.StatusFound, redirectURL)
		return
	}

	// Redirect to frontend callback page with tokens
	redirectURL := fmt.Sprintf("%s/login/callback?accessToken=%s&refreshToken=%s",
		h.getFrontendURL(), url.QueryEscape(result.AccessToken), url.QueryEscape(result.RefreshToken))

	c.Redirect(http.StatusFound, redirectURL)
}

// getFrontendURL returns the frontend URL for redirects
func (h *SSOHandler) getFrontendURL() string {
	if h.settingsService != nil {
		if baseURL := h.settingsService.GetBaseURL(); baseURL != "" {
			return baseURL
		}
	}
	return h.frontendURL
}

// getBaseURL returns the configured base URL, falling back to request-derived URL
func (h *SSOHandler) getBaseURL(c *gin.Context) string {
	if h.settingsService != nil {
		if baseURL := h.settingsService.GetBaseURL(); baseURL != "" {
			return baseURL
		}
	}
	// Fallback: derive from request
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, c.Request.Host)
}
