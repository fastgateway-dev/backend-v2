//go:build e2e

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// petstoreYAML and petstoreJSON are copies of
// e2e/regression/fixtures/openapi/petstore.{yaml,json} (read-only source;
// copied inline here rather than read from disk at test time, so this
// package has no runtime dependency on the Python regression tree's
// fixtures directory). Both describe the same 5-operation Petstore API:
// GET/POST /pets, GET/DELETE /pets/{petId}, GET /health.
const petstoreYAML = `openapi: 3.0.3
info:
  title: Petstore API
  version: 1.0.0
paths:
  /pets:
    get:
      operationId: listPets
      summary: List all pets
      tags: [pets]
    post:
      operationId: createPet
      summary: Create a pet
      tags: [pets]
  /pets/{petId}:
    get:
      operationId: getPetById
      summary: Get pet by ID
      tags: [pets]
    delete:
      operationId: deletePet
      tags: [pets]
  /health:
    get:
      operationId: healthCheck
      tags: [system]
`

const petstoreJSON = `{
  "openapi": "3.0.3",
  "info": { "title": "Petstore API", "version": "1.0.0" },
  "paths": {
    "/pets": {
      "get": { "operationId": "listPets", "summary": "List all pets", "tags": ["pets"] },
      "post": { "operationId": "createPet", "summary": "Create a pet", "tags": ["pets"] }
    },
    "/pets/{petId}": {
      "get": { "operationId": "getPetById", "summary": "Get pet by ID", "tags": ["pets"] },
      "delete": { "operationId": "deletePet", "tags": ["pets"] }
    },
    "/health": {
      "get": { "operationId": "healthCheck", "tags": ["system"] }
    }
  }
}`

// importOpenAPI POSTs /projects/:projectId/domains/:domainId/import/openapi
// (OpenAPIImportHandler.Import). No typed harness wrapper exists for it,
// so this goes through API.Do directly.
func importOpenAPI(ctx context.Context, spec string, backend services.DefaultBackend) (services.OpenAPIImportResponse, error) {
	body := services.OpenAPIImportRequest{Spec: spec, DefaultBackend: backend}
	var resp services.OpenAPIImportResponse
	path := "/projects/" + env.ProjectID + "/domains/" + env.DomainID + "/import/openapi"
	_, err := env.Admin.Do(ctx, http.MethodPost, path, body, &resp)
	return resp, err
}

// expectImportRejected asserts importing spec/backend fails with HTTP 400
// and the given error code (the "error" field OpenAPIImportHandler.Import
// always sets on a 400).
func expectImportRejected(t *testing.T, ctx context.Context, spec string, backend services.DefaultBackend, wantErrorCode string) {
	t.Helper()
	_, err := importOpenAPI(ctx, spec, backend)
	if err == nil {
		t.Fatalf("import: succeeded, want rejection (400 %q)", wantErrorCode)
	}
	var statusErr *harness.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("import: error %v is not a *harness.StatusError", err)
	}
	if statusErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("import: got status %d, want %d (body: %s)", statusErr.StatusCode, http.StatusBadRequest, statusErr.Body)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(statusErr.Body), &body); err != nil {
		t.Fatalf("import: decode error body %q: %v", statusErr.Body, err)
	}
	if body.Error != wantErrorCode {
		t.Fatalf("import: got error=%q, want %q (body: %s)", body.Error, wantErrorCode, statusErr.Body)
	}
}

// TestOpenAPIImportPetstore ports
// import/test_openapi_import_basic.py:test_openapi_import_petstore
// (parametrized over petstore.yaml/petstore.json in the Python source;
// ported here as two t.Run subtests of one Go test function). Already a
// real assertion in the Python source; ported unchanged in spirit.
func TestOpenAPIImportPetstore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		spec string
	}{
		{"yaml", petstoreYAML},
		{"json", petstoreJSON},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			resp, err := importOpenAPI(ctx, c.spec, services.DefaultBackend{Service: "petstore", Namespace: "default", Port: 8080})
			if err != nil {
				t.Fatalf("import petstore (%s): %v", c.name, err)
			}

			if resp.SpecInfo.Title != "Petstore API" {
				t.Fatalf("import petstore (%s): specInfo.title=%q, want %q", c.name, resp.SpecInfo.Title, "Petstore API")
			}
			if resp.SpecInfo.Version != "1.0.0" {
				t.Fatalf("import petstore (%s): specInfo.version=%q, want %q", c.name, resp.SpecInfo.Version, "1.0.0")
			}
			if resp.SpecInfo.Format != "openapi-3.0" {
				t.Fatalf("import petstore (%s): specInfo.format=%q, want %q", c.name, resp.SpecInfo.Format, "openapi-3.0")
			}

			if len(resp.Routes) != 5 {
				t.Fatalf("import petstore (%s): got %d routes, want 5", c.name, len(resp.Routes))
			}
			byName := map[string]services.ParsedRoute{}
			for _, r := range resp.Routes {
				byName[r.Name] = r
			}
			for _, want := range []string{"list-pets", "create-pet", "get-pet-by-id", "delete-pet", "health-check"} {
				if _, ok := byName[want]; !ok {
					t.Fatalf("import petstore (%s): missing expected route name %q among %v", c.name, want, routeNames(resp.Routes))
				}
			}

			health := byName["health-check"]
			if health.Config.Matches[0].Path == nil || health.Config.Matches[0].Path.Type != "Exact" || health.Config.Matches[0].Path.Value != "/health" {
				t.Fatalf("import petstore (%s): health-check path match=%+v, want Exact /health", c.name, health.Config.Matches[0].Path)
			}
			getPet := byName["get-pet-by-id"]
			if getPet.Config.Matches[0].Path == nil || getPet.Config.Matches[0].Path.Type != "RegularExpression" || getPet.Config.Matches[0].Path.Value != "^/pets/[^/]+$" {
				t.Fatalf("import petstore (%s): get-pet-by-id path match=%+v, want RegularExpression ^/pets/[^/]+$", c.name, getPet.Config.Matches[0].Path)
			}

			for _, r := range resp.Routes {
				if len(r.Config.Backends) == 0 {
					t.Fatalf("import petstore (%s): route %q has no backends", c.name, r.Name)
				}
				be := r.Config.Backends[0]
				if string(be.Type) != "kubernetes" || be.Service != "petstore" || be.Namespace != "default" || be.Port != 8080 {
					t.Fatalf("import petstore (%s): route %q backend=%+v, want kubernetes petstore/default:8080", c.name, r.Name, be)
				}
			}

			if health.Tag != "system" {
				t.Fatalf("import petstore (%s): health-check tag=%q, want %q", c.name, health.Tag, "system")
			}
			if byName["list-pets"].Tag != "pets" {
				t.Fatalf("import petstore (%s): list-pets tag=%q, want %q", c.name, byName["list-pets"].Tag, "pets")
			}
		})
	}
}

func routeNames(routes []services.ParsedRoute) []string {
	names := make([]string, len(routes))
	for i, r := range routes {
		names[i] = r.Name
	}
	return names
}

// TestOpenAPIImportExternalBackend ports
// import/test_openapi_import_basic.py:test_openapi_import_external_backend.
// Already a real assertion in the Python source; ported unchanged in
// spirit.
func TestOpenAPIImportExternalBackend(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := importOpenAPI(ctx, petstoreYAML, services.DefaultBackend{Address: "api.example.com", Port: 443})
	if err != nil {
		t.Fatalf("import petstore with external backend: %v", err)
	}
	if len(resp.Routes) == 0 {
		t.Fatalf("import petstore with external backend: got 0 routes")
	}
	for _, r := range resp.Routes {
		if len(r.Config.Backends) == 0 {
			t.Fatalf("import petstore with external backend: route %q has no backends", r.Name)
		}
		be := r.Config.Backends[0]
		if string(be.Type) != "external" || be.Address != "api.example.com" || be.Port != 443 {
			t.Fatalf("import petstore with external backend: route %q backend=%+v, want external api.example.com:443", r.Name, be)
		}
	}
}

// TestOpenAPIImportThenCreateWithEdits ports
// import/test_openapi_import_inline_edit.py:test_openapi_import_then_create_with_edits.
// Already a real assertion in the Python source; ported unchanged in
// spirit, using harness.UniqueName instead of a request-fixture-derived
// name and env.Admin.DeleteRoute for cleanup instead of a raw finalizer.
func TestOpenAPIImportThenCreateWithEdits(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	parsed, err := importOpenAPI(ctx, petstoreYAML, services.DefaultBackend{Service: "petstore", Namespace: "default", Port: 8080})
	if err != nil {
		t.Fatalf("import petstore: %v", err)
	}

	var healthCheck *services.ParsedRoute
	for i := range parsed.Routes {
		if parsed.Routes[i].Name == "health-check" {
			healthCheck = &parsed.Routes[i]
			break
		}
	}
	if healthCheck == nil {
		t.Fatalf("import petstore: no health-check route in parsed response")
	}

	testName := harness.UniqueName(t)
	// A unique path, not a hardcoded "/healthz": a fixed path matcher
	// collides with a leftover from a crashed previous run or a repeated
	// -count=2 execution (the backend rejects duplicate matchers), same
	// as every other route this package creates via uniquePath.
	healthCheckPath := "/" + testName
	healthCheck.Config.Matches[0].Path.Type = "Prefix"
	healthCheck.Config.Matches[0].Path.Value = healthCheckPath
	healthCheck.Config.Backends[0].Type = "external"
	healthCheck.Config.Backends[0].Address = "monitoring.example.com"
	healthCheck.Config.Backends[0].AddressType = "fqdn"
	healthCheck.Config.Backends[0].Port = 443
	healthCheck.Config.Backends[0].Service = ""
	healthCheck.Config.Backends[0].Namespace = ""

	createInput := services.CreateRouteInput{
		Name:         testName,
		Description:  healthCheck.Description,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config:       healthCheck.Config,
	}
	route, err := env.Editor.CreateRoute(ctx, env.ProjectID, env.DomainID, createInput)
	if err != nil {
		t.Fatalf("create route from edited import result: %v", err)
	}
	t.Cleanup(func() { cleanupPendingOrRejectedRoute(t, route) })

	if route.Name != testName {
		t.Fatalf("created route: name=%q, want %q", route.Name, testName)
	}
	if route.Config.Matches[0].Path == nil || route.Config.Matches[0].Path.Type != "Prefix" || route.Config.Matches[0].Path.Value != healthCheckPath {
		t.Fatalf("created route: path match=%+v, want Prefix %s", route.Config.Matches[0].Path, healthCheckPath)
	}
	if len(route.Config.Backends) == 0 || string(route.Config.Backends[0].Type) != "external" || route.Config.Backends[0].Address != "monitoring.example.com" {
		t.Fatalf("created route: backend=%+v, want external monitoring.example.com", route.Config.Backends)
	}
}

// TestOpenAPIImportDuplicateOperationIDRenames ports
// import/test_openapi_import_renames_and_validation.py:test_openapi_import_duplicate_operation_id_renames.
// Already a real assertion in the Python source; ported unchanged in
// spirit.
func TestOpenAPIImportDuplicateOperationIDRenames(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /a: {get: {operationId: getX}}
  /b: {get: {operationId: getX}}
`
	resp, err := importOpenAPI(ctx, spec, services.DefaultBackend{Service: "x", Namespace: "default", Port: 80})
	if err != nil {
		t.Fatalf("import duplicate operationId spec: %v", err)
	}
	names := routeNames(resp.Routes)
	if len(names) != 2 || !(contains(names, "get-x") && contains(names, "get-x-2")) {
		t.Fatalf("import duplicate operationId spec: routes=%v, want [get-x get-x-2]", names)
	}

	found := false
	for _, r := range resp.Renames {
		if r.Final == "get-x-2" && r.Reason == "duplicate" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("import duplicate operationId spec: renames=%+v, want an entry with final=get-x-2 reason=duplicate", resp.Renames)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestOpenAPIImportSwagger2Rejected ports
// import/test_openapi_import_renames_and_validation.py:test_openapi_import_swagger2_rejected.
// Already a real assertion in the Python source; ported unchanged in
// spirit.
func TestOpenAPIImportSwagger2Rejected(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const spec = "swagger: \"2.0\"\ninfo: {title: x, version: \"1\"}\npaths: {}"
	expectImportRejected(t, ctx, spec, services.DefaultBackend{Service: "x", Namespace: "default", Port: 80}, "openapi_parse_failed")
}

// TestOpenAPIImportInvalidBackendBothSet ports
// import/test_openapi_import_renames_and_validation.py:test_openapi_import_invalid_backend_both_set.
// Already a real assertion in the Python source; ported unchanged in
// spirit.
func TestOpenAPIImportInvalidBackendBothSet(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const spec = "openapi: 3.0.3\ninfo: {title: T, version: \"1\"}\npaths:\n  /a: {get: {}}\n"
	expectImportRejected(t, ctx, spec, services.DefaultBackend{Service: "s", Namespace: "ns", Address: "a.b", Port: 80}, "invalid_request")
}
