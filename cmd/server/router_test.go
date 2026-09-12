package main

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/handlers"
	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

// TestMain puts gin into its quiet TestMode before any test builds a
// router, purely to suppress the "[GIN-debug] ... registered N routes"
// startup logging gin.New() emits in its default DebugMode. It does not
// change route registration itself (setupRouter's own gin.SetMode call, if
// any, is main()'s concern -- there is none; main() sets the mode once,
// globally, via cfg.LogLevel, before calling setupRouter).
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// TestRouteSpecParity is the sync guard for spec<->code drift (Phase 2O Task
// 7), the same role mocks-check plays for internal/mocks (see the Makefile
// comment on that target). Tasks 1-6 reconciled docs/openapi/ against the
// handlers; without this test a future route change that forgets the spec
// (or vice versa) would go unnoticed until someone hits the undocumented
// endpoint or the documented-but-dead one.
//
// It builds the real router via setupRouter -- the same function main()
// calls -- and enumerates its routes with engine.Routes(), then parses the
// committed, //go:embed-ed cmd/server/openapi.yaml (the openapiSpec
// variable declared in main.go; these are the literal bytes served at
// /api/v1/docs/openapi.yaml) for its (method, path) operations. It asserts
// the two sets are equal modulo the normalization in normalizeGinPath, save
// for the explicit allowlist below.
//
// setupRouter is built with zero-value handler/middleware structs: route
// registration only takes method values (never calls them), so no database,
// repository, or service is needed to enumerate the routes.
func TestRouteSpecParity(t *testing.T) {
	deps := RouterDeps{
		AuthMiddleware:          &middleware.AuthMiddleware{},
		PermChecker:             &middleware.PermissionChecker{},
		AuthHandler:             &handlers.AuthHandler{},
		SSOHandler:              &handlers.SSOHandler{},
		DocsHandler:             &handlers.DocsHandler{},
		UserHandler:             &handlers.UserHandler{},
		SystemSettingsHandler:   &handlers.SystemSettingsHandler{},
		TeamHandler:             &handlers.TeamHandler{},
		ClientHandler:           &handlers.ClientHandler{},
		ClientAttachmentHandler: &handlers.ClientAttachmentHandler{},
		AIHandler:               &handlers.AIHandler{},
		ProjectHandler:          &handlers.ProjectHandler{},
		MetricsHandler:          &handlers.MetricsHandler{},
		ProjectVersionHandler:   &handlers.ProjectVersionHandler{},
		PermissionHandler:       &handlers.PermissionHandler{},
		PresetHandler:           &handlers.PresetHandler{},
		DomainTemplateHandler:   &handlers.DomainTemplateHandler{},
		ProjectNamespaceHandler: &handlers.ProjectNamespaceHandler{},
		DomainHandler:           &handlers.DomainHandler{},
		TopologyHandler:         &handlers.TopologyHandler{},
		OpenAPIImportHandler:    &handlers.OpenAPIImportHandler{},
		RouteHandler:            &handlers.RouteHandler{},
		RouteVersionHandler:     &handlers.RouteVersionHandler{},
		ApprovalHandler:         &handlers.ApprovalHandler{},
		CommentHandler:          &handlers.CommentHandler{},
		ApprovalPolicyHandler:   &handlers.ApprovalPolicyHandler{},
		K8sHandler:              &handlers.KubernetesHandler{},
		AuditHandler:            &handlers.AuditHandler{},
		NotificationHandler:     &handlers.NotificationHandler{},
	}

	router := setupRouter(deps)

	// routeAllowlist names routes that are deliberately registered on the
	// engine but have no (and should have no) operation in docs/openapi/:
	// infra/runtime endpoints rather than API surface. Empty today -- both
	// candidates considered below turned out to already have a genuine spec
	// operation once paths are compared relative to the spec's declared
	// server URL (see normalizeGinPath), so neither belongs here:
	//
	//   - GET /health has no /api/v1 prefix in code (it is registered
	//     directly on the root engine, not under the v1 group) and the spec
	//     documents /health relative to its "servers: - url: .../api/v1"
	//     entry, which is the same convention normalizeGinPath applies: a
	//     path with no /api/v1 prefix is compared as-is, so it matches
	//     cmd/server/openapi.yaml's `/health: get:` operation directly.
	//   - GET /api/v1/docs/openapi.yaml strips to /docs/openapi.yaml, which
	//     matches the spec's own `/docs/openapi.yaml: get:` operation (the
	//     spec documents the endpoint that serves it).
	//
	// Kept as a named, commented var (rather than deleted) so the next
	// genuinely-infra route someone adds has an obvious place to go instead
	// of weakening the comparison below.
	routeAllowlist := map[routeKey]string{
		// routeKey{Method: "GET", Path: "/some/infra/path"}: "reason this is not API surface",
	}

	ginRoutes := collectGinRoutes(t, router)
	specOps := collectSpecOperations(t, openapiSpec)

	var unmatchedRoutes []routeKey // registered but no spec operation
	for rk := range ginRoutes {
		if _, allowed := routeAllowlist[rk]; allowed {
			continue
		}
		if _, ok := specOps[rk]; !ok {
			unmatchedRoutes = append(unmatchedRoutes, rk)
		}
	}

	var unmatchedOps []routeKey // documented but no registered route
	for rk := range specOps {
		if _, ok := ginRoutes[rk]; !ok {
			unmatchedOps = append(unmatchedOps, rk)
		}
	}

	sort.Slice(unmatchedRoutes, func(i, j int) bool { return unmatchedRoutes[i].String() < unmatchedRoutes[j].String() })
	sort.Slice(unmatchedOps, func(i, j int) bool { return unmatchedOps[i].String() < unmatchedOps[j].String() })

	if len(unmatchedRoutes) > 0 {
		t.Errorf("routes registered in setupRouter with no matching operation in cmd/server/openapi.yaml (%d):", len(unmatchedRoutes))
		for _, rk := range unmatchedRoutes {
			t.Errorf("  %s", rk.String())
		}
	}
	if len(unmatchedOps) > 0 {
		t.Errorf("operations documented in cmd/server/openapi.yaml with no matching registered route (%d):", len(unmatchedOps))
		for _, rk := range unmatchedOps {
			t.Errorf("  %s", rk.String())
		}
	}
}

// routeKey is a normalized (method, path) pair comparable between gin's
// route table and the OpenAPI spec's path map.
type routeKey struct {
	Method string
	Path   string
}

func (k routeKey) String() string { return k.Method + " " + k.Path }

// ginParamPattern matches a gin path parameter segment, e.g. ":projectId" or
// ":teamId". Gin (this version) only uses the ":name" form -- no "*splat"
// wildcards are registered anywhere in setupRouter -- so that's the only
// form normalizeGinPath needs to handle.
var ginParamPattern = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

// normalizeGinPath converts a gin route path into the spec's path style:
// gin's ":id" becomes OpenAPI's "{id}", and the "/api/v1" prefix -- which
// comes from the v1 := router.Group("/api/v1") in setupRouter, not from
// docs/openapi/ -- is stripped, matching the spec's own
// "servers: - url: http://localhost:8081/api/v1" base, relative to which
// every documented path (including /health and /docs/openapi.yaml) is
// written.
func normalizeGinPath(path string) string {
	const apiPrefix = "/api/v1"
	if rest, ok := strings.CutPrefix(path, apiPrefix); ok {
		if rest == "" {
			rest = "/"
		}
		path = rest
	}
	return ginParamPattern.ReplaceAllString(path, "{$1}")
}

// collectGinRoutes enumerates engine.Routes() and normalizes each into a
// routeKey. t.Fatal on zero routes: an empty result would make the parity
// check vacuously pass, hiding a setupRouter regression instead of catching
// one (that's exactly the failure mode the falsification check in the task
// brief verifies this test does NOT have).
func collectGinRoutes(t *testing.T, router *gin.Engine) map[routeKey]struct{} {
	t.Helper()
	routes := router.Routes()
	if len(routes) == 0 {
		t.Fatal("setupRouter registered zero routes; something is badly broken")
	}
	out := make(map[routeKey]struct{}, len(routes))
	for _, r := range routes {
		out[routeKey{Method: r.Method, Path: normalizeGinPath(r.Path)}] = struct{}{}
	}
	return out
}

// openAPIDoc captures just enough of the document shape to enumerate
// operations: a map of path -> (HTTP method -> operation object). The
// operation object's own content (summary, parameters, responses, ...)
// is irrelevant here, so it is decoded as an opaque yaml.Node rather than
// a fully typed struct.
type openAPIDoc struct {
	Paths map[string]map[string]yaml.Node `yaml:"paths"`
}

// httpMethods are the OpenAPI path-item keys that denote an operation, as
// opposed to sibling keys like "parameters", "summary", or "servers" that
// can legally appear alongside them on a path item. cmd/server/openapi.yaml
// currently uses only get/post/put/patch/delete, but the full set is
// checked so a future addition (e.g. a HEAD health-check variant) isn't
// miscounted as a registered route with no spec match.
var httpMethods = map[string]struct{}{
	"get": {}, "post": {}, "put": {}, "patch": {}, "delete": {}, "head": {}, "options": {}, "trace": {},
}

// collectSpecOperations parses the bundled OpenAPI document (the literal
// //go:embed-ed bytes served at /api/v1/docs/openapi.yaml -- see
// openapiSpec in main.go) and returns every (method, path) operation it
// declares, as routeKeys in the same normalized form collectGinRoutes
// produces (the spec's "{param}" style is already what normalizeGinPath
// converts gin's ":param" into, so spec paths pass through unchanged).
func collectSpecOperations(t *testing.T, spec []byte) map[routeKey]struct{} {
	t.Helper()
	var doc openAPIDoc
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		t.Fatalf("failed to parse embedded cmd/server/openapi.yaml: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("embedded cmd/server/openapi.yaml declares zero paths; something is badly broken")
	}
	out := make(map[routeKey]struct{})
	for path, item := range doc.Paths {
		for method := range item {
			if _, ok := httpMethods[strings.ToLower(method)]; !ok {
				continue
			}
			out[routeKey{Method: strings.ToUpper(method), Path: path}] = struct{}{}
		}
	}
	return out
}
