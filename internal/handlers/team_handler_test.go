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

func TestTeamHandler_List_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser() // owner role
	teams := []models.Team{
		{ID: uuid.New(), Name: "team1"},
		{ID: uuid.New(), Name: "team2"},
	}
	mockTeam.On("List").Return(teams, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/teams", nil)
	c.Set("user", user)

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Len(t, resp, 2)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_Create_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	team := &models.Team{ID: uuid.New(), Name: "new-team"}
	mockTeam.On("Create", mock.AnythingOfType("*services.CreateTeamInput")).Return(team, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"name": "new-team"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/teams", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_Get_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	teamID := uuid.New()
	team := &models.Team{ID: teamID, Name: "team1"}
	mockTeam.On("GetByID", teamID).Return(team, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/teams/"+teamID.String(), nil)
	c.Params = gin.Params{{Key: "teamId", Value: teamID.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_Get_InvalidID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/teams/bad-id", nil)
	c.Params = gin.Params{{Key: "teamId", Value: "bad-id"}}

	h.Get(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_Get_NotFound(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	teamID := uuid.New()
	mockTeam.On("GetByID", teamID).Return(nil, errors.New("not found"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/teams/"+teamID.String(), nil)
	c.Params = gin.Params{{Key: "teamId", Value: teamID.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_Update_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	teamID := uuid.New()
	team := &models.Team{ID: teamID, Name: "updated-team"}
	mockTeam.On("Update", teamID, mock.AnythingOfType("*services.UpdateTeamInput")).Return(team, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"name": "updated-team"})

	router := gin.New()
	router.PUT("/teams/:teamId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/teams/"+teamID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_Delete_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	teamID := uuid.New()
	team := &models.Team{ID: teamID, Name: "to-delete"}
	mockTeam.On("GetByID", teamID).Return(team, nil)
	mockTeam.On("Delete", teamID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/teams/:teamId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/teams/"+teamID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListMembers_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	teamID := uuid.New()
	members := []models.User{
		{ID: uuid.New(), Username: "user1"},
		{ID: uuid.New(), Username: "user2"},
	}
	mockTeam.On("ListMembers", teamID).Return(members, nil)

	router := gin.New()
	router.GET("/teams/:teamId/members", func(c *gin.Context) {
		h.ListMembers(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/teams/"+teamID.String()+"/members", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Len(t, resp, 2)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_AddMember_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	teamID := uuid.New()
	memberID := uuid.New()
	mockTeam.On("AddMember", teamID, memberID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"userId": memberID.String()})

	router := gin.New()
	router.POST("/teams/:teamId/members", func(c *gin.Context) {
		c.Set("user", user)
		h.AddMember(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/teams/"+teamID.String()+"/members", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_RemoveMember_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	teamID := uuid.New()
	memberID := uuid.New()
	mockTeam.On("RemoveMember", teamID, memberID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/teams/:teamId/members/:userId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveMember(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/teams/"+teamID.String()+"/members/"+memberID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListProjectTeams_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	projectID := uuid.New()
	teamRoles := []models.ProjectTeamRole{
		{ProjectID: projectID, TeamID: uuid.New()},
	}
	mockTeam.On("ListProjectTeams", projectID).Return(teamRoles, nil)

	router := gin.New()
	router.GET("/projects/:projectId/teams", func(c *gin.Context) {
		h.ListProjectTeams(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/teams", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListTeamProjects_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	teamID := uuid.New()
	projects := []models.ProjectTeamRole{
		{ProjectID: uuid.New(), TeamID: teamID},
	}
	mockTeam.On("ListTeamProjects", teamID).Return(projects, nil)

	router := gin.New()
	router.GET("/teams/:teamId/projects", func(c *gin.Context) {
		h.ListTeamProjects(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/teams/"+teamID.String()+"/projects", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListTeamProjects_InvalidID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	router := gin.New()
	router.GET("/teams/:teamId/projects", func(c *gin.Context) {
		h.ListTeamProjects(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/teams/bad-id/projects", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_GetProjectTeamRole_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	projectID := uuid.New()
	teamID := uuid.New()
	ptr := &models.ProjectTeamRole{ProjectID: projectID, TeamID: teamID}
	mockTeam.On("GetProjectTeamRole", projectID, teamID).Return(ptr, nil)

	router := gin.New()
	router.GET("/projects/:projectId/teams/:teamId", func(c *gin.Context) {
		h.GetProjectTeamRole(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/teams/"+teamID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_GetProjectTeamRole_InvalidProjectID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	router := gin.New()
	router.GET("/projects/:projectId/teams/:teamId", func(c *gin.Context) {
		h.GetProjectTeamRole(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/bad-id/teams/"+uuid.New().String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_RemoveTeamFromProject_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	projectID := uuid.New()
	teamID := uuid.New()
	mockTeam.On("RemoveTeamFromProject", projectID, teamID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/teams/:teamId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveTeamFromProject(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/teams/"+teamID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListMyTeams_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser() // owner role - sees all teams
	teams := []models.Team{
		{ID: uuid.New(), Name: "team1"},
	}
	mockTeam.On("List").Return(teams, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/teams/my", nil)
	c.Set("user", user)

	h.ListMyTeams(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListMyTeams_NoUser(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/teams/my", nil)
	// No user set

	h.ListMyTeams(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestTeamHandler_ListMyTeamsInProject_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	projectID := uuid.New()
	teamRoles := []models.ProjectTeamRole{
		{ProjectID: projectID, TeamID: uuid.New()},
	}
	mockTeam.On("GetUserTeamsInProject", projectID, user.ID).Return(teamRoles, nil)

	router := gin.New()
	router.GET("/projects/:projectId/my-teams", func(c *gin.Context) {
		c.Set("user", user)
		h.ListMyTeamsInProject(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/my-teams", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListMyTeamsInProject_NoUser(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	projectID := uuid.New()

	router := gin.New()
	router.GET("/projects/:projectId/my-teams", func(c *gin.Context) {
		// No user set
		h.ListMyTeamsInProject(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/my-teams", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestTeamHandler_List_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	mockTeam.On("List").Return([]models.Team{}, errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/teams", nil)
	c.Set("user", user)

	h.List(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_Update_InvalidID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()

	router := gin.New()
	router.PUT("/teams/:teamId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/teams/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_Delete_InvalidID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()

	router := gin.New()
	router.DELETE("/teams/:teamId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/teams/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_Delete_NotFound(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	teamID := uuid.New()
	mockTeam.On("GetByID", teamID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.DELETE("/teams/:teamId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/teams/"+teamID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_AssignTeamToProject_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	projectID := uuid.New()
	teamID := uuid.New()
	presetID := uuid.New()
	ptr := &models.ProjectTeamRole{ProjectID: projectID, TeamID: teamID}
	mockTeam.On("AssignTeamToProject", projectID, mock.AnythingOfType("*services.AssignTeamInput")).Return(ptr, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{
		"teamId":    teamID.String(),
		"presetIds": []string{presetID.String()},
	})

	router := gin.New()
	router.POST("/projects/:projectId/teams", func(c *gin.Context) {
		c.Set("user", user)
		h.AssignTeamToProject(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_UpdateTeamPresets_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	projectID := uuid.New()
	teamID := uuid.New()
	presetID := uuid.New()
	ptr := &models.ProjectTeamRole{ProjectID: projectID, TeamID: teamID}
	mockTeam.On("UpdateTeamPresets", projectID, teamID, mock.AnythingOfType("*services.UpdateTeamPresetsInput")).Return(ptr, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{
		"presetIds": []string{presetID.String()},
	})

	router := gin.New()
	router.PUT("/projects/:projectId/teams/:teamId/presets", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateTeamPresets(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/teams/"+teamID.String()+"/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_UpdateTeamPresets_InvalidProjectID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()

	router := gin.New()
	router.PUT("/projects/:projectId/teams/:teamId/presets", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateTeamPresets(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/bad-id/teams/"+uuid.New().String()+"/presets", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_UpdateTeamPresets_InvalidTeamID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()

	router := gin.New()
	router.PUT("/projects/:projectId/teams/:teamId/presets", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateTeamPresets(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+uuid.New().String()+"/teams/bad-id/presets", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_AddMemberByEmail_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	mockEmailInvite := new(mocks.MockTeamEmailInviteService)
	h.SetEmailInviteService(mockEmailInvite)

	user := testUser()
	teamID := uuid.New()
	result := &services.AddMemberResult{Type: "added", User: &models.User{ID: uuid.New()}}
	mockEmailInvite.On("AddMemberByEmail", teamID, "user@example.com", user.ID).Return(result, nil)

	body, _ := json.Marshal(map[string]string{"email": "user@example.com"})

	router := gin.New()
	router.POST("/teams/:teamId/members/email", func(c *gin.Context) {
		c.Set("user", user)
		h.AddMemberByEmail(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/teams/"+teamID.String()+"/members/email", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockEmailInvite.AssertExpectations(t)
}

func TestTeamHandler_AddMemberByEmail_InvalidTeamID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	router := gin.New()
	router.POST("/teams/:teamId/members/email", func(c *gin.Context) {
		c.Set("user", testUser())
		h.AddMemberByEmail(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/teams/bad-id/members/email", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_AddMemberByEmail_InvalidBody(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	router := gin.New()
	router.POST("/teams/:teamId/members/email", func(c *gin.Context) {
		c.Set("user", testUser())
		h.AddMemberByEmail(c)
	})

	body, _ := json.Marshal(map[string]string{"email": "not-an-email"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/teams/"+uuid.New().String()+"/members/email", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_AddMemberByEmail_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	mockEmailInvite := new(mocks.MockTeamEmailInviteService)
	h.SetEmailInviteService(mockEmailInvite)

	user := testUser()
	teamID := uuid.New()
	mockEmailInvite.On("AddMemberByEmail", teamID, "user@example.com", user.ID).Return(nil, errors.New("already member"))

	body, _ := json.Marshal(map[string]string{"email": "user@example.com"})

	router := gin.New()
	router.POST("/teams/:teamId/members/email", func(c *gin.Context) {
		c.Set("user", user)
		h.AddMemberByEmail(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/teams/"+teamID.String()+"/members/email", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockEmailInvite.AssertExpectations(t)
}

func TestTeamHandler_ListInvites_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	mockEmailInvite := new(mocks.MockTeamEmailInviteService)
	h.SetEmailInviteService(mockEmailInvite)

	teamID := uuid.New()
	mockEmailInvite.On("ListInvites", teamID).Return([]models.TeamEmailInvite{}, nil)

	router := gin.New()
	router.GET("/teams/:teamId/invites", func(c *gin.Context) {
		h.ListInvites(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/teams/"+teamID.String()+"/invites", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockEmailInvite.AssertExpectations(t)
}

func TestTeamHandler_ListInvites_InvalidTeamID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	router := gin.New()
	router.GET("/teams/:teamId/invites", func(c *gin.Context) {
		h.ListInvites(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/teams/bad-id/invites", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_DeleteInvite_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	mockEmailInvite := new(mocks.MockTeamEmailInviteService)
	h.SetEmailInviteService(mockEmailInvite)

	inviteID := uuid.New()
	mockEmailInvite.On("DeleteInvite", inviteID).Return(nil)

	router := gin.New()
	router.DELETE("/invites/:inviteId", func(c *gin.Context) {
		h.DeleteInvite(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/invites/"+inviteID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockEmailInvite.AssertExpectations(t)
}

func TestTeamHandler_DeleteInvite_InvalidID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	router := gin.New()
	router.DELETE("/invites/:inviteId", func(c *gin.Context) {
		h.DeleteInvite(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/invites/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_ListProjectMembers_Success(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	projectID := uuid.New()
	members := []models.User{
		{ID: uuid.New(), Username: "user1"},
	}
	mockTeam.On("ListProjectMembers", projectID, "").Return(members, nil)

	router := gin.New()
	router.GET("/projects/:projectId/members", func(c *gin.Context) {
		h.ListProjectMembers(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/members", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListProjectMembers_InvalidProjectID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	router := gin.New()
	router.GET("/projects/:projectId/members", func(c *gin.Context) {
		h.ListProjectMembers(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/bad-id/members", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_ListProjectMembers_WithSearch(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	projectID := uuid.New()
	mockTeam.On("ListProjectMembers", projectID, "john").Return([]models.User{}, nil)

	router := gin.New()
	router.GET("/projects/:projectId/members", func(c *gin.Context) {
		h.ListProjectMembers(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/members?search=john", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_Create_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	mockTeam.On("Create", mock.AnythingOfType("*services.CreateTeamInput")).Return(nil, errors.New("duplicate name"))

	body, _ := json.Marshal(map[string]string{"name": "dup-team"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/teams", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_Create_InvalidBody(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/teams", bytes.NewReader([]byte("invalid")))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", user)

	h.Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_AddMember_InvalidTeamID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()

	router := gin.New()
	router.POST("/teams/:teamId/members", func(c *gin.Context) {
		c.Set("user", user)
		h.AddMember(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/teams/bad-id/members", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_AddMember_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	teamID := uuid.New()
	memberID := uuid.New()
	mockTeam.On("AddMember", teamID, memberID).Return(errors.New("already a member"))

	body, _ := json.Marshal(map[string]string{"userId": memberID.String()})

	router := gin.New()
	router.POST("/teams/:teamId/members", func(c *gin.Context) {
		c.Set("user", user)
		h.AddMember(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/teams/"+teamID.String()+"/members", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_RemoveMember_InvalidTeamID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()

	router := gin.New()
	router.DELETE("/teams/:teamId/members/:userId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveMember(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/teams/bad-id/members/"+uuid.New().String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_RemoveMember_InvalidUserID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()

	router := gin.New()
	router.DELETE("/teams/:teamId/members/:userId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveMember(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/teams/"+uuid.New().String()+"/members/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_RemoveMember_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	teamID := uuid.New()
	memberID := uuid.New()
	mockTeam.On("RemoveMember", teamID, memberID).Return(errors.New("cannot remove last member"))

	router := gin.New()
	router.DELETE("/teams/:teamId/members/:userId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveMember(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/teams/"+teamID.String()+"/members/"+memberID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_List_NoUser(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/teams", nil)
	// No user set

	h.List(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestTeamHandler_List_NonOwnerWithPermission(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := &models.User{
		ID:       uuid.New(),
		Username: "regularuser",
		Email:    "user@test.com",
		Role:     models.UserRoleUser,
		IsActive: true,
	}
	mockTeamRepo.On("HasPermissionInAnyProject", user.ID, models.PermProjectTeams).Return(true, nil)
	teams := []models.Team{{ID: uuid.New(), Name: "team1"}}
	mockTeam.On("List").Return(teams, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/teams", nil)
	c.Set("user", user)

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockTeamRepo.AssertExpectations(t)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_List_NonOwnerForbidden(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := &models.User{
		ID:       uuid.New(),
		Username: "regularuser",
		Email:    "user@test.com",
		Role:     models.UserRoleUser,
		IsActive: true,
	}
	mockTeamRepo.On("HasPermissionInAnyProject", user.ID, models.PermProjectTeams).Return(false, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/teams", nil)
	c.Set("user", user)

	h.List(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamHandler_List_NonOwnerRepoError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := &models.User{
		ID:       uuid.New(),
		Username: "regularuser",
		Email:    "user@test.com",
		Role:     models.UserRoleUser,
		IsActive: true,
	}
	mockTeamRepo.On("HasPermissionInAnyProject", user.ID, models.PermProjectTeams).Return(false, errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/teams", nil)
	c.Set("user", user)

	h.List(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockTeamRepo.AssertExpectations(t)
}

func TestTeamHandler_ListProjectTeams_InvalidProjectID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	router := gin.New()
	router.GET("/projects/:projectId/teams", func(c *gin.Context) {
		h.ListProjectTeams(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/bad-id/teams", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_ListProjectTeams_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	projectID := uuid.New()
	mockTeam.On("ListProjectTeams", projectID).Return([]models.ProjectTeamRole{}, errors.New("db error"))

	router := gin.New()
	router.GET("/projects/:projectId/teams", func(c *gin.Context) {
		h.ListProjectTeams(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/teams", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_AssignTeamToProject_InvalidProjectID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()

	router := gin.New()
	router.POST("/projects/:projectId/teams", func(c *gin.Context) {
		c.Set("user", user)
		h.AssignTeamToProject(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/bad-id/teams", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_AssignTeamToProject_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	projectID := uuid.New()
	mockTeam.On("AssignTeamToProject", projectID, mock.AnythingOfType("*services.AssignTeamInput")).Return(nil, errors.New("already assigned"))

	body, _ := json.Marshal(map[string]interface{}{
		"teamId":    uuid.New().String(),
		"presetIds": []string{uuid.New().String()},
	})

	router := gin.New()
	router.POST("/projects/:projectId/teams", func(c *gin.Context) {
		c.Set("user", user)
		h.AssignTeamToProject(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/projects/"+projectID.String()+"/teams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_RemoveTeamFromProject_InvalidProjectID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()

	router := gin.New()
	router.DELETE("/projects/:projectId/teams/:teamId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveTeamFromProject(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/bad-id/teams/"+uuid.New().String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_RemoveTeamFromProject_InvalidTeamID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()

	router := gin.New()
	router.DELETE("/projects/:projectId/teams/:teamId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveTeamFromProject(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+uuid.New().String()+"/teams/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_RemoveTeamFromProject_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	projectID := uuid.New()
	teamID := uuid.New()
	mockTeam.On("RemoveTeamFromProject", projectID, teamID).Return(errors.New("not assigned"))

	router := gin.New()
	router.DELETE("/projects/:projectId/teams/:teamId", func(c *gin.Context) {
		c.Set("user", user)
		h.RemoveTeamFromProject(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/teams/"+teamID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_GetProjectTeamRole_InvalidTeamID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	router := gin.New()
	router.GET("/projects/:projectId/teams/:teamId", func(c *gin.Context) {
		h.GetProjectTeamRole(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+uuid.New().String()+"/teams/bad-id", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_GetProjectTeamRole_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	projectID := uuid.New()
	teamID := uuid.New()
	mockTeam.On("GetProjectTeamRole", projectID, teamID).Return(nil, errors.New("not found"))

	router := gin.New()
	router.GET("/projects/:projectId/teams/:teamId", func(c *gin.Context) {
		h.GetProjectTeamRole(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/teams/"+teamID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListMyTeams_NonOwnerServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := &models.User{
		ID:       uuid.New(),
		Username: "regularuser",
		Email:    "user@test.com",
		Role:     models.UserRoleUser,
		IsActive: true,
	}
	mockTeam.On("GetUserTeams", user.ID).Return([]models.Team{}, errors.New("db error"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/teams/my", nil)
	c.Set("user", user)

	h.ListMyTeams(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListMyTeamsInProject_InvalidProjectID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()

	router := gin.New()
	router.GET("/projects/:projectId/my-teams", func(c *gin.Context) {
		c.Set("user", user)
		h.ListMyTeamsInProject(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/bad-id/my-teams", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_ListMyTeamsInProject_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	projectID := uuid.New()
	mockTeam.On("GetUserTeamsInProject", projectID, user.ID).Return([]models.ProjectTeamRole{}, errors.New("db error"))

	router := gin.New()
	router.GET("/projects/:projectId/my-teams", func(c *gin.Context) {
		c.Set("user", user)
		h.ListMyTeamsInProject(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/my-teams", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListMembers_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	teamID := uuid.New()
	mockTeam.On("ListMembers", teamID).Return([]models.User{}, errors.New("db error"))

	router := gin.New()
	router.GET("/teams/:teamId/members", func(c *gin.Context) {
		h.ListMembers(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/teams/"+teamID.String()+"/members", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListTeamProjects_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	teamID := uuid.New()
	mockTeam.On("ListTeamProjects", teamID).Return([]models.ProjectTeamRole{}, errors.New("db error"))

	router := gin.New()
	router.GET("/teams/:teamId/projects", func(c *gin.Context) {
		h.ListTeamProjects(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/teams/"+teamID.String()+"/projects", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_Update_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	teamID := uuid.New()
	mockTeam.On("Update", teamID, mock.AnythingOfType("*services.UpdateTeamInput")).Return(nil, errors.New("not found"))

	body, _ := json.Marshal(map[string]string{"name": "updated-team"})

	router := gin.New()
	router.PUT("/teams/:teamId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/teams/"+teamID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_Update_BadBody(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	teamID := uuid.New()

	router := gin.New()
	router.PUT("/teams/:teamId", func(c *gin.Context) {
		c.Set("user", user)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/teams/"+teamID.String(), bytes.NewReader([]byte("invalid")))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTeamHandler_Delete_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	teamID := uuid.New()
	team := &models.Team{ID: teamID, Name: "to-delete"}
	mockTeam.On("GetByID", teamID).Return(team, nil)
	mockTeam.On("Delete", teamID).Return(errors.New("has members"))

	router := gin.New()
	router.DELETE("/teams/:teamId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/teams/"+teamID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListProjectMembers_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	projectID := uuid.New()
	mockTeam.On("ListProjectMembers", projectID, "").Return([]models.User{}, errors.New("db error"))

	router := gin.New()
	router.GET("/projects/:projectId/members", func(c *gin.Context) {
		h.ListProjectMembers(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/members", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListInvites_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	mockEmailInvite := new(mocks.MockTeamEmailInviteService)
	h.SetEmailInviteService(mockEmailInvite)

	teamID := uuid.New()
	mockEmailInvite.On("ListInvites", teamID).Return([]models.TeamEmailInvite{}, errors.New("db error"))

	router := gin.New()
	router.GET("/teams/:teamId/invites", func(c *gin.Context) {
		h.ListInvites(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/teams/"+teamID.String()+"/invites", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockEmailInvite.AssertExpectations(t)
}

func TestTeamHandler_DeleteInvite_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	mockEmailInvite := new(mocks.MockTeamEmailInviteService)
	h.SetEmailInviteService(mockEmailInvite)

	inviteID := uuid.New()
	mockEmailInvite.On("DeleteInvite", inviteID).Return(errors.New("not found"))

	router := gin.New()
	router.DELETE("/invites/:inviteId", func(c *gin.Context) {
		h.DeleteInvite(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/invites/"+inviteID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mockEmailInvite.AssertExpectations(t)
}

func TestTeamHandler_UpdateTeamPresets_ServiceError(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	user := testUser()
	projectID := uuid.New()
	teamID := uuid.New()
	mockTeam.On("UpdateTeamPresets", projectID, teamID, mock.AnythingOfType("*services.UpdateTeamPresetsInput")).Return(nil, errors.New("not found"))

	body, _ := json.Marshal(map[string]interface{}{
		"presetIds": []string{uuid.New().String()},
	})

	router := gin.New()
	router.PUT("/projects/:projectId/teams/:teamId/presets", func(c *gin.Context) {
		c.Set("user", user)
		h.UpdateTeamPresets(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/projects/"+projectID.String()+"/teams/"+teamID.String()+"/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mockTeam.AssertExpectations(t)
}

func TestTeamHandler_ListMembers_InvalidTeamID(t *testing.T) {
	mockTeam := new(mocks.MockTeamService)
	mockTeamRepo := new(mocks.MockTeamRepository)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewTeamHandler(mockTeam, permsFor(mockTeamRepo), mockAudit)

	router := gin.New()
	router.GET("/teams/:teamId/members", func(c *gin.Context) {
		h.ListMembers(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/teams/bad-id/members", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
