package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type stubRateLimit struct{ ok bool }

func (s stubRateLimit) IsRateLimitAvailable(context.Context, uuid.UUID) (bool, error) {
	return s.ok, nil
}

type stubCaps struct{ m map[string]bool }

func (s stubCaps) Evaluate(context.Context, uuid.UUID) map[string]bool { return s.m }

func TestGetCapabilities_IncludesStreamAvailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewProjectHandler(nil, nil, stubRateLimit{ok: true}, stubCaps{m: map[string]bool{"streams": false}})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "projectId", Value: uuid.New().String()}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h.GetCapabilities(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]bool
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["rateLimitAvailable"] != true {
		t.Errorf("rateLimitAvailable = %v, want true", body["rateLimitAvailable"])
	}
	if _, ok := body["streamAvailable"]; !ok {
		t.Error("response must include streamAvailable")
	}
	if body["streamAvailable"] != false {
		t.Errorf("streamAvailable = %v, want false", body["streamAvailable"])
	}
}
