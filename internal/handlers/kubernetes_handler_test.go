package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestKubernetesHandler_ListNamespaces_Success(t *testing.T) {
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewKubernetesHandler(mockK8s)

	projectID := uuid.New()
	namespaces := []string{"default", "kube-system", "app-ns"}
	mockK8s.On("ListNamespaces", mock.Anything, projectID).Return(namespaces, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/kubernetes/namespaces", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.ListNamespaces(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var result []interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	assert.Len(t, result, 3)
	mockK8s.AssertExpectations(t)
}

func TestKubernetesHandler_ListServices_Success(t *testing.T) {
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewKubernetesHandler(mockK8s)

	projectID := uuid.New()
	serviceList := []map[string]interface{}{
		{
			"name":      "svc1",
			"namespace": "default",
			"ports": []map[string]interface{}{
				{"name": "http", "port": int64(80), "protocol": "TCP"},
			},
		},
	}
	mockK8s.On("ListServices", mock.Anything, projectID, "default").Return(serviceList, nil)

	router := gin.New()
	router.GET("/projects/:projectId/kubernetes/namespaces/:namespace/services", func(c *gin.Context) {
		h.ListServices(c)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/projects/"+projectID.String()+"/kubernetes/namespaces/default/services", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var result []interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	assert.Len(t, result, 1)
	mockK8s.AssertExpectations(t)
}

func TestKubernetesHandler_ListGatewayClasses_Success(t *testing.T) {
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewKubernetesHandler(mockK8s)

	projectID := uuid.New()
	classes := []string{"eg", "istio"}
	mockK8s.On("ListGatewayClasses", mock.Anything, projectID).Return(classes, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/kubernetes/gateway-classes", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}

	h.ListGatewayClasses(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var result []interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	assert.Len(t, result, 2)
	mockK8s.AssertExpectations(t)
}

func TestKubernetesHandler_ListNamespaces_InvalidProjectID(t *testing.T) {
	mockK8s := new(mocks.MockKubernetesService)
	h := handlers.NewKubernetesHandler(mockK8s)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/invalid/kubernetes/namespaces", nil)
	c.Params = gin.Params{{Key: "projectId", Value: "invalid"}}

	h.ListNamespaces(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
