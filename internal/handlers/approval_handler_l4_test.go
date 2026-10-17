package handlers_test

import (
	"encoding/json"
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
	"github.com/stretchr/testify/require"
)

func TestApprovalHandler_GetDiff_L4RouteIs200WithRouteKind(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	h := handlers.NewApprovalHandler(mockApproval, new(mocks.MockAuditService))
	id := uuid.New()
	mockApproval.On("GetDiff", id).Return(&services.ApprovalDiffResult{
		Action: "update", RouteKind: "TCPRoute", CurrentYAML: "kind: TCPRoute\n", ProposedYAML: "kind: TCPRoute\n",
	}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/"+id.String()+"/diff", nil)
	c.Params = gin.Params{{Key: "approvalId", Value: id.String()}}
	h.GetDiff(c)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "TCPRoute", body["routeKind"])
}

func TestApprovalHandler_Get_CarriesStreamFields(t *testing.T) {
	mockApproval := new(mocks.MockApprovalService)
	h := handlers.NewApprovalHandler(mockApproval, new(mocks.MockAuditService))
	id, sid := uuid.New(), uuid.New()
	mockApproval.On("GetByID", id).Return(&models.Approval{ID: id, EntityType: models.ApprovalEntityRoute, StreamID: &sid, StreamName: "test-stream"}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/approvals/"+id.String(), nil)
	c.Params = gin.Params{{Key: "approvalId", Value: id.String()}}
	h.Get(c)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, sid.String(), body["streamId"])
	assert.Equal(t, "test-stream", body["streamName"])
}
