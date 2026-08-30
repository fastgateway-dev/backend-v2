package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
)

func setupOpenAPITestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewOpenAPIImportHandler(services.NewOpenAPIImportService())
	r.POST("/projects/:projectId/domains/:domainId/import/openapi", h.Import)
	return r
}

func TestOpenAPIImportHandler_HappyPath(t *testing.T) {
	r := setupOpenAPITestRouter()
	body, _ := json.Marshal(map[string]interface{}{
		"spec": "openapi: 3.0.3\ninfo: {title: T, version: \"1\"}\npaths:\n  /a: {get: {operationId: getA}}\n",
		"defaultBackend": map[string]interface{}{
			"service":   "petstore",
			"namespace": "default",
			"port":      8080,
		},
	})
	req := httptest.NewRequest("POST", "/projects/p1/domains/d1/import/openapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp services.OpenAPIImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Routes) != 1 {
		t.Fatalf("want 1 route, got %d", len(resp.Routes))
	}
}

func TestOpenAPIImportHandler_MissingSpec(t *testing.T) {
	r := setupOpenAPITestRouter()
	body := []byte(`{"defaultBackend":{"service":"p","namespace":"d","port":80}}`)
	req := httptest.NewRequest("POST", "/projects/p1/domains/d1/import/openapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestOpenAPIImportHandler_BackendBothServiceAndAddress(t *testing.T) {
	r := setupOpenAPITestRouter()
	body := []byte(`{"spec":"openapi: 3.0.3\ninfo: {title: T, version: \"1\"}\npaths:\n  /a: {get: {}}\n","defaultBackend":{"service":"s","namespace":"ns","address":"a.b","port":80}}`)
	req := httptest.NewRequest("POST", "/projects/p1/domains/d1/import/openapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestOpenAPIImportHandler_BackendNeither(t *testing.T) {
	r := setupOpenAPITestRouter()
	body := []byte(`{"spec":"openapi: 3.0.3\ninfo: {title: T, version: \"1\"}\npaths:\n  /a: {get: {}}\n","defaultBackend":{"port":80}}`)
	req := httptest.NewRequest("POST", "/projects/p1/domains/d1/import/openapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestOpenAPIImportHandler_ParseFailReturns400(t *testing.T) {
	r := setupOpenAPITestRouter()
	body := []byte(`{"spec":"not valid openapi at all","defaultBackend":{"service":"s","namespace":"n","port":80}}`)
	req := httptest.NewRequest("POST", "/projects/p1/domains/d1/import/openapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", w.Code, w.Body.String())
	}
	var errResp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp["error"] != "openapi_parse_failed" {
		t.Fatalf("want error=openapi_parse_failed, got %v", errResp)
	}
}

func TestOpenAPIImportHandler_SpecTooLarge(t *testing.T) {
	r := setupOpenAPITestRouter()
	// Build a JSON body just over the 5MB cap
	huge := make([]byte, 6*1024*1024)
	for i := range huge {
		huge[i] = 'a'
	}
	body := []byte(`{"spec":"` + string(huge) + `","defaultBackend":{"service":"s","namespace":"n","port":80}}`)
	req := httptest.NewRequest("POST", "/projects/p1/domains/d1/import/openapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", w.Code)
	}
	var errResp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &errResp)
	if errResp["error"] != "spec_too_large" {
		t.Fatalf("want error=spec_too_large, got %v", errResp)
	}
}
