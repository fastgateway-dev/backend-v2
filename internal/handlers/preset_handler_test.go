package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestPresetHandler_List_Success(t *testing.T) {
	mockPreset := new(mocks.MockPresetService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewPresetHandler(mockPreset, mockAudit)

	projectID := uuid.New()
	presets := []models.PermissionPreset{
		{ID: uuid.New(), ProjectID: projectID, Name: "Admin", Permissions: pq.StringArray{"route.create"}},
		{ID: uuid.New(), ProjectID: projectID, Name: "Viewer", Permissions: pq.StringArray{"route.view"}},
	}
	mockPreset.On("ListByProject", projectID).Return(presets, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/presets", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockPreset.AssertExpectations(t)
}

func TestPresetHandler_Create_Success(t *testing.T) {
	mockPreset := new(mocks.MockPresetService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewPresetHandler(mockPreset, mockAudit)

	user := testUser()
	projectID := uuid.New()
	preset := &models.PermissionPreset{
		ID:          uuid.New(),
		ProjectID:   projectID,
		Name:        "Editor",
		Permissions: pq.StringArray{"route.create", "route.update"},
	}
	mockPreset.On("Create", projectID, mock.AnythingOfType("*services.CreatePresetInput")).Return(preset, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{
		"name":        "Editor",
		"permissions": []string{"route.create", "route.update"},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/projects/"+projectID.String()+"/presets", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}
	c.Set("user", user)

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockPreset.AssertExpectations(t)
}

func TestPresetHandler_Update_Success(t *testing.T) {
	mockPreset := new(mocks.MockPresetService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewPresetHandler(mockPreset, mockAudit)

	user := testUser()
	projectID := uuid.New()
	presetID := uuid.New()
	preset := &models.PermissionPreset{
		ID:          presetID,
		ProjectID:   projectID,
		Name:        "Updated",
		Permissions: pq.StringArray{"route.view"},
	}
	mockPreset.On("Update", projectID, presetID, mock.AnythingOfType("*services.UpdatePresetInput")).Return(preset, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{
		"name":        "Updated",
		"permissions": []string{"route.view"},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/projects/"+projectID.String()+"/presets/"+presetID.String(), bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "presetId", Value: presetID.String()},
	}
	c.Set("user", user)

	h.Update(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockPreset.AssertExpectations(t)
}

func TestPresetHandler_Delete_Success(t *testing.T) {
	mockPreset := new(mocks.MockPresetService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewPresetHandler(mockPreset, mockAudit)

	user := testUser()
	projectID := uuid.New()
	presetID := uuid.New()
	preset := &models.PermissionPreset{ID: presetID, ProjectID: projectID, Name: "ToDelete"}
	mockPreset.On("GetByID", presetID).Return(preset, nil)
	mockPreset.On("Delete", projectID, presetID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/projects/:projectId/presets/:presetId", func(c *gin.Context) {
		c.Set("user", user)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/projects/"+projectID.String()+"/presets/"+presetID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockPreset.AssertExpectations(t)
}
