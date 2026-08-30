package handlers_test

import (
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
)

func TestNotificationHandler_List_Success(t *testing.T) {
	mockNotif := new(mocks.MockNotificationService)
	h := handlers.NewNotificationHandler(mockNotif)

	user := testUser()
	notifications := []models.Notification{
		{ID: uuid.New(), UserID: user.ID, Title: "Notif 1"},
		{ID: uuid.New(), UserID: user.ID, Title: "Notif 2"},
	}
	mockNotif.On("List", user.ID, false, 1, 20).Return(notifications, int64(2), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/notifications", nil)
	c.Set("user", user)

	h.List(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
	mockNotif.AssertExpectations(t)
}

func TestNotificationHandler_CountUnread_Success(t *testing.T) {
	mockNotif := new(mocks.MockNotificationService)
	h := handlers.NewNotificationHandler(mockNotif)

	user := testUser()
	mockNotif.On("CountUnread", user.ID).Return(int64(5), nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/notifications/unread-count", nil)
	c.Set("user", user)

	h.CountUnread(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, float64(5), resp["unread"])
	mockNotif.AssertExpectations(t)
}

func TestNotificationHandler_MarkAsRead_Success(t *testing.T) {
	mockNotif := new(mocks.MockNotificationService)
	h := handlers.NewNotificationHandler(mockNotif)

	user := testUser()
	notifID := uuid.New()
	mockNotif.On("MarkAsRead", notifID, user.ID).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/notifications/"+notifID.String()+"/read", nil)
	c.Params = gin.Params{{Key: "notificationId", Value: notifID.String()}}
	c.Set("user", user)

	h.MarkAsRead(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockNotif.AssertExpectations(t)
}

func TestNotificationHandler_MarkAllAsRead_Success(t *testing.T) {
	mockNotif := new(mocks.MockNotificationService)
	h := handlers.NewNotificationHandler(mockNotif)

	user := testUser()
	mockNotif.On("MarkAllAsRead", user.ID).Return(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/notifications/read-all", nil)
	c.Set("user", user)

	h.MarkAllAsRead(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockNotif.AssertExpectations(t)
}

func TestNotificationHandler_List_NoUser(t *testing.T) {
	mockNotif := new(mocks.MockNotificationService)
	h := handlers.NewNotificationHandler(mockNotif)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/notifications", nil)
	// No user set

	h.List(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestNotificationHandler_CountUnread_NoUser(t *testing.T) {
	mockNotif := new(mocks.MockNotificationService)
	h := handlers.NewNotificationHandler(mockNotif)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/notifications/unread-count", nil)
	// No user set

	h.CountUnread(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestNotificationHandler_MarkAsRead_NoUser(t *testing.T) {
	mockNotif := new(mocks.MockNotificationService)
	h := handlers.NewNotificationHandler(mockNotif)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/notifications/"+uuid.New().String()+"/read", nil)
	c.Params = gin.Params{{Key: "notificationId", Value: uuid.New().String()}}
	// No user set

	h.MarkAsRead(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestNotificationHandler_MarkAsRead_InvalidID(t *testing.T) {
	mockNotif := new(mocks.MockNotificationService)
	h := handlers.NewNotificationHandler(mockNotif)

	user := testUser()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/notifications/bad-id/read", nil)
	c.Params = gin.Params{{Key: "notificationId", Value: "bad-id"}}
	c.Set("user", user)

	h.MarkAsRead(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestNotificationHandler_MarkAllAsRead_NoUser(t *testing.T) {
	mockNotif := new(mocks.MockNotificationService)
	h := handlers.NewNotificationHandler(mockNotif)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("PUT", "/notifications/read-all", nil)
	// No user set

	h.MarkAllAsRead(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
