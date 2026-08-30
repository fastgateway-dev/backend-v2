package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestPermissionHandler_GetPermissions_Success(t *testing.T) {
	mockProjectRepo := new(mocks.MockProjectRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	pc := middleware.NewPermissionChecker(mockProjectRepo, mockTeamRepo)
	h := handlers.NewPermissionHandler(pc)

	user := testUser() // owner role
	projectID := uuid.New()
	mockProjectRepo.On("IsAdmin", projectID, user.ID).Return(false, nil)

	router := gin.New()
	router.GET("/projects/:projectId/permissions", func(c *gin.Context) {
		c.Set("user", user)
		h.GetPermissions(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/permissions", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockProjectRepo.AssertExpectations(t)
}

func TestPermissionHandler_GetPermissions_NoUser(t *testing.T) {
	mockProjectRepo := new(mocks.MockProjectRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	pc := middleware.NewPermissionChecker(mockProjectRepo, mockTeamRepo)
	h := handlers.NewPermissionHandler(pc)

	router := gin.New()
	router.GET("/projects/:projectId/permissions", func(c *gin.Context) {
		// No user set
		h.GetPermissions(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+uuid.New().String()+"/permissions", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPermissionHandler_GetPermissions_InvalidProjectID(t *testing.T) {
	mockProjectRepo := new(mocks.MockProjectRepository)
	mockTeamRepo := new(mocks.MockTeamRepository)
	pc := middleware.NewPermissionChecker(mockProjectRepo, mockTeamRepo)
	h := handlers.NewPermissionHandler(pc)

	user := testUser()

	router := gin.New()
	router.GET("/projects/:projectId/permissions", func(c *gin.Context) {
		c.Set("user", user)
		h.GetPermissions(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/bad-id/permissions", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
