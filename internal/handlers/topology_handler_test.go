package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestTopologyHandler_GetProject_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockSvc := new(mocks.MockTopologyService)
	h := handlers.NewTopologyHandler(mockSvc)

	projectID := uuid.New()
	mockSvc.On("GetProjectTopology", mock.Anything, projectID).Return(&services.ProjectTopologyResponse{
		Domains: []services.ProjectTopologyDomain{},
		Clients: []services.ProjectTopologyClient{},
		IPs:     []services.TopologyIPRow{},
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/topology", nil)
	c.Params = gin.Params{{Key: "projectId", Value: projectID.String()}}
	h.GetProjectTopology(c)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	_, ok := resp["domains"]
	assert.True(t, ok)
}

func TestTopologyHandler_GetProject_BadProjectID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockSvc := new(mocks.MockTopologyService)
	h := handlers.NewTopologyHandler(mockSvc)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/bad/topology", nil)
	c.Params = gin.Params{{Key: "projectId", Value: "bad"}}
	h.GetProjectTopology(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTopologyHandler_GetDomain_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockSvc := new(mocks.MockTopologyService)
	h := handlers.NewTopologyHandler(mockSvc)
	projectID := uuid.New()
	domainID := uuid.New()
	wrapped := fmt.Errorf("get domain: %w", services.ErrTopologyNotFound)
	mockSvc.On("GetDomainTopology", mock.Anything, projectID, domainID).Return((*services.DomainTopologyResponse)(nil), wrapped)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/topology", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "domainId", Value: domainID.String()},
	}
	h.GetDomainTopology(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestTopologyHandler_GetDomain_WrongProject_Returns404 verifies that when
// the service signals "domain belongs to a different project" via
// ErrTopologyNotFound, the handler returns 404 and the response body does
// NOT leak the existence of the domain or the other project.
func TestTopologyHandler_GetDomain_WrongProject_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockSvc := new(mocks.MockTopologyService)
	h := handlers.NewTopologyHandler(mockSvc)
	projectID := uuid.New()
	domainID := uuid.New()
	otherProjectID := uuid.New()
	// Service wraps ErrTopologyNotFound for the wrong-project case (same
	// sentinel as missing domain — caller can't distinguish).
	wrapped := fmt.Errorf("get domain: %w", services.ErrTopologyNotFound)
	mockSvc.On("GetDomainTopology", mock.Anything, projectID, domainID).Return((*services.DomainTopologyResponse)(nil), wrapped)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/projects/"+projectID.String()+"/domains/"+domainID.String()+"/topology", nil)
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "domainId", Value: domainID.String()},
	}
	h.GetDomainTopology(c)
	assert.Equal(t, http.StatusNotFound, w.Code)

	body := w.Body.String()
	assert.NotContains(t, strings.ToLower(body), "belong", "404 body must not leak that the domain belongs to a different project")
	assert.NotContains(t, body, otherProjectID.String(), "404 body must not leak any other project's UUID")
	assert.NotContains(t, body, projectID.String(), "404 body must not echo the project UUID")
	assert.NotContains(t, body, domainID.String(), "404 body must not echo the domain UUID")
}
