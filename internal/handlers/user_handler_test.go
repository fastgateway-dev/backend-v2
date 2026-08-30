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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func testUser() *models.User {
	return &models.User{
		ID:       uuid.New(),
		Username: "admin",
		Email:    "admin@test.com",
		Role:     models.UserRoleOwner,
		IsActive: true,
	}
}

func TestUserHandler_List(t *testing.T) {
	mockUser := new(mocks.MockUserService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewUserHandler(mockUser, mockAudit)

	users := []models.User{
		{ID: uuid.New(), Username: "user1", Email: "u1@test.com"},
		{ID: uuid.New(), Username: "user2", Email: "u2@test.com"},
	}
	mockUser.On("List", 1, 20, "").Return(users, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/users", nil)

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
	mockUser.AssertExpectations(t)
}

func TestUserHandler_Get_Success(t *testing.T) {
	mockUser := new(mocks.MockUserService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewUserHandler(mockUser, mockAudit)

	userID := uuid.New()
	user := &models.User{ID: userID, Username: "user1", Email: "u1@test.com"}
	mockUser.On("GetByID", userID).Return(user, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/users/"+userID.String(), nil)
	c.Params = gin.Params{{Key: "userId", Value: userID.String()}}

	h.Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockUser.AssertExpectations(t)
}

func TestUserHandler_Get_InvalidID(t *testing.T) {
	mockUser := new(mocks.MockUserService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewUserHandler(mockUser, mockAudit)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/users/invalid", nil)
	c.Params = gin.Params{{Key: "userId", Value: "invalid"}}

	h.Get(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUserHandler_Create_Success(t *testing.T) {
	mockUser := new(mocks.MockUserService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewUserHandler(mockUser, mockAudit)

	currentUser := testUser()
	newUser := &models.User{ID: uuid.New(), Username: "newuser", Email: "new@test.com", Role: "user"}
	mockUser.On("Create", mock.AnythingOfType("*services.CreateUserInput")).Return(newUser, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{
		"username": "newuser",
		"email":    "new@test.com",
		"password": "password123",
		"role":     "user",
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/users", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", currentUser)

	h.Create(c)

	assert.Equal(t, http.StatusCreated, w.Code)
	mockUser.AssertExpectations(t)
}

func TestUserHandler_Create_BadBody(t *testing.T) {
	mockUser := new(mocks.MockUserService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewUserHandler(mockUser, mockAudit)

	currentUser := testUser()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/users", bytes.NewReader([]byte("{invalid")))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", currentUser)

	h.Create(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUserHandler_Update_Success(t *testing.T) {
	mockUser := new(mocks.MockUserService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewUserHandler(mockUser, mockAudit)

	currentUser := testUser()
	userID := uuid.New()
	updatedUser := &models.User{ID: userID, Username: "user1", Email: "updated@test.com"}
	mockUser.On("Update", userID, mock.AnythingOfType("*services.UpdateUserInput")).Return(updatedUser, nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]string{"email": "updated@test.com"})
	router := gin.New()
	router.PUT("/users/:userId", func(c *gin.Context) {
		c.Set("user", currentUser)
		h.Update(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/users/"+userID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockUser.AssertExpectations(t)
}

func TestUserHandler_Update_InvalidID(t *testing.T) {
	mockUser := new(mocks.MockUserService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewUserHandler(mockUser, mockAudit)

	currentUser := testUser()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/users/invalid", bytes.NewReader([]byte("{}")))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "userId", Value: "invalid"}}
	c.Set("user", currentUser)

	h.Update(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUserHandler_Delete_Success(t *testing.T) {
	mockUser := new(mocks.MockUserService)
	mockAudit := new(mocks.MockAuditService)
	h := handlers.NewUserHandler(mockUser, mockAudit)

	currentUser := testUser()
	userID := uuid.New()
	userToDelete := &models.User{ID: userID, Username: "todelete", Email: "del@test.com"}
	mockUser.On("GetByID", userID).Return(userToDelete, nil)
	mockUser.On("Delete", userID).Return(nil)
	mockAudit.On("LogAction", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	router := gin.New()
	router.DELETE("/users/:userId", func(c *gin.Context) {
		c.Set("user", currentUser)
		h.Delete(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/users/"+userID.String(), nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mockUser.AssertExpectations(t)
}
