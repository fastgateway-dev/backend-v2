package services

import "testing"

func TestPathToMatcher_StaticPath(t *testing.T) {
	mt, val := pathToMatcher("/health")
	if mt != "Exact" || val != "/health" {
		t.Fatalf("want Exact /health, got %s %s", mt, val)
	}
}

func TestPathToMatcher_SingleParam(t *testing.T) {
	mt, val := pathToMatcher("/users/{id}")
	if mt != "RegularExpression" || val != "^/users/[^/]+$" {
		t.Fatalf("want RegularExpression, got %s %s", mt, val)
	}
}

func TestPathToMatcher_MultipleParams(t *testing.T) {
	mt, val := pathToMatcher("/users/{id}/posts/{postId}")
	if mt != "RegularExpression" || val != "^/users/[^/]+/posts/[^/]+$" {
		t.Fatalf("got %s %s", mt, val)
	}
}

func TestPathToMatcher_RootPath(t *testing.T) {
	mt, val := pathToMatcher("/")
	if mt != "Exact" || val != "/" {
		t.Fatalf("got %s %s", mt, val)
	}
}

const minimalOpenAPI3 = `openapi: 3.0.3
info:
  title: Test API
  version: 1.0.0
paths:
  /health:
    get:
      operationId: getHealth
      summary: Health check
      tags: [system]
  /users/{id}:
    get:
      operationId: getUserById
      summary: Get a user
      tags: [users]
    delete:
      operationId: deleteUser
      tags: [users]
`

func defaultBackendKubernetes() DefaultBackend {
	return DefaultBackend{Service: "petstore", Namespace: "default", Port: 8080}
}

func TestParse_MinimalSpec(t *testing.T) {
	svc := NewOpenAPIImportService()
	resp, err := svc.Parse(minimalOpenAPI3, defaultBackendKubernetes())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Routes) != 3 {
		t.Fatalf("want 3 routes, got %d", len(resp.Routes))
	}
	if resp.SpecInfo.Title != "Test API" || resp.SpecInfo.Version != "1.0.0" {
		t.Fatalf("specInfo wrong: %+v", resp.SpecInfo)
	}
	if resp.SpecInfo.Format != "openapi-3.0" {
		t.Fatalf("want openapi-3.0, got %s", resp.SpecInfo.Format)
	}
}

func TestParse_NameFromOperationID(t *testing.T) {
	svc := NewOpenAPIImportService()
	resp, _ := svc.Parse(minimalOpenAPI3, defaultBackendKubernetes())
	names := map[string]bool{}
	for _, r := range resp.Routes {
		names[r.Name] = true
	}
	if !names["get-health"] {
		t.Fatalf("missing get-health: %v", names)
	}
	if !names["get-user-by-id"] {
		t.Fatalf("missing get-user-by-id: %v", names)
	}
}

func TestParse_TagPopulated(t *testing.T) {
	svc := NewOpenAPIImportService()
	resp, _ := svc.Parse(minimalOpenAPI3, defaultBackendKubernetes())
	for _, r := range resp.Routes {
		if r.Name == "get-health" && r.Tag != "system" {
			t.Fatalf("want tag=system, got %q", r.Tag)
		}
	}
}

func TestParse_DescriptionFromSummary(t *testing.T) {
	svc := NewOpenAPIImportService()
	resp, _ := svc.Parse(minimalOpenAPI3, defaultBackendKubernetes())
	for _, r := range resp.Routes {
		if r.Name == "get-health" && r.Description != "Health check" {
			t.Fatalf("want 'Health check', got %q", r.Description)
		}
	}
}

func TestParse_RejectsSwagger2(t *testing.T) {
	svc := NewOpenAPIImportService()
	_, err := svc.Parse(`swagger: "2.0"
info: {title: x, version: "1"}
paths: {}
`, defaultBackendKubernetes())
	if err == nil {
		t.Fatal("want error for swagger 2.0")
	}
}

func TestParse_MalformedYAMLFails(t *testing.T) {
	svc := NewOpenAPIImportService()
	_, err := svc.Parse("not: valid: yaml: at: all", defaultBackendKubernetes())
	if err == nil {
		t.Fatal("want parse error")
	}
}

func TestParse_MissingPathsFails(t *testing.T) {
	svc := NewOpenAPIImportService()
	_, err := svc.Parse(`openapi: 3.0.3
info: {title: x, version: "1"}
`, defaultBackendKubernetes())
	if err == nil {
		t.Fatal("want error for spec with no paths")
	}
}

func TestParse_JSONInputAccepted(t *testing.T) {
	svc := NewOpenAPIImportService()
	jsonSpec := `{
		"openapi": "3.0.3",
		"info": {"title": "T", "version": "1"},
		"paths": {
			"/health": {"get": {"operationId": "getHealth"}}
		}
	}`
	resp, err := svc.Parse(jsonSpec, defaultBackendKubernetes())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.Routes) != 1 {
		t.Fatalf("want 1 route, got %d", len(resp.Routes))
	}
}

func TestParse_OpenAPI31Accepted(t *testing.T) {
	svc := NewOpenAPIImportService()
	resp, err := svc.Parse(`openapi: 3.1.0
info: {title: T, version: "1"}
paths:
  /x: {get: {operationId: getX}}
`, defaultBackendKubernetes())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.SpecInfo.Format != "openapi-3.1" {
		t.Fatalf("want openapi-3.1, got %s", resp.SpecInfo.Format)
	}
}

func TestParse_KubernetesBackendApplied(t *testing.T) {
	svc := NewOpenAPIImportService()
	resp, _ := svc.Parse(minimalOpenAPI3, defaultBackendKubernetes())
	if len(resp.Routes) == 0 {
		t.Fatal("no routes")
	}
	r := resp.Routes[0]
	if len(r.Config.Backends) != 1 {
		t.Fatalf("want 1 backend, got %d", len(r.Config.Backends))
	}
	be := r.Config.Backends[0]
	if string(be.Type) != "kubernetes" || be.Service != "petstore" || be.Namespace != "default" || be.Port != 8080 {
		t.Fatalf("backend wrong: %+v", be)
	}
}

func TestParse_ExternalBackendApplied(t *testing.T) {
	svc := NewOpenAPIImportService()
	be := DefaultBackend{Address: "api.example.com", Port: 443}
	resp, _ := svc.Parse(minimalOpenAPI3, be)
	if len(resp.Routes) == 0 {
		t.Fatal("no routes")
	}
	r := resp.Routes[0]
	b := r.Config.Backends[0]
	if string(b.Type) != "external" || b.Address != "api.example.com" || b.Port != 443 {
		t.Fatalf("external backend wrong: %+v", b)
	}
}

func TestParse_StaticPathBecomesExactMatch(t *testing.T) {
	svc := NewOpenAPIImportService()
	resp, _ := svc.Parse(minimalOpenAPI3, defaultBackendKubernetes())
	for _, r := range resp.Routes {
		if r.Name == "get-health" {
			if len(r.Config.Matches) != 1 {
				t.Fatalf("want 1 match, got %d", len(r.Config.Matches))
			}
			m := r.Config.Matches[0]
			if m.Path == nil || m.Path.Type != "Exact" || m.Path.Value != "/health" {
				t.Fatalf("path match wrong: %+v", m.Path)
			}
			if m.Method != "GET" {
				t.Fatalf("want method GET, got %q", m.Method)
			}
		}
	}
}

func TestParse_ParameterizedPathBecomesRegex(t *testing.T) {
	svc := NewOpenAPIImportService()
	resp, _ := svc.Parse(minimalOpenAPI3, defaultBackendKubernetes())
	for _, r := range resp.Routes {
		if r.Name == "get-user-by-id" {
			m := r.Config.Matches[0]
			if m.Path == nil || m.Path.Type != "RegularExpression" || m.Path.Value != "^/users/[^/]+$" {
				t.Fatalf("regex path wrong: %+v", m.Path)
			}
		}
	}
}

func TestParse_DuplicateOperationIDDisambiguated(t *testing.T) {
	dupSpec := `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /a: {get: {operationId: getX}}
  /b: {get: {operationId: getX}}
`
	svc := NewOpenAPIImportService()
	resp, _ := svc.Parse(dupSpec, defaultBackendKubernetes())
	if len(resp.Routes) != 2 {
		t.Fatalf("want 2 routes, got %d", len(resp.Routes))
	}
	if resp.Routes[0].Name != "get-x" || resp.Routes[1].Name != "get-x-2" {
		t.Fatalf("disambiguation wrong: %s, %s", resp.Routes[0].Name, resp.Routes[1].Name)
	}
	foundDup := false
	for _, rn := range resp.Renames {
		if rn.Final == "get-x-2" && rn.Reason == "duplicate" {
			foundDup = true
		}
	}
	if !foundDup {
		t.Fatalf("missing duplicate rename: %+v", resp.Renames)
	}
}

func TestParse_DefaultsAppliedHttpAndGeneral(t *testing.T) {
	svc := NewOpenAPIImportService()
	resp, _ := svc.Parse(minimalOpenAPI3, defaultBackendKubernetes())
	for _, r := range resp.Routes {
		if r.Protocol != "http" {
			t.Fatalf("want protocol http, got %q", r.Protocol)
		}
		if r.SecurityMode != "general" {
			t.Fatalf("want securityMode general, got %q", r.SecurityMode)
		}
	}
}

func TestParse_ExternalBackendIPDetectedAsIP(t *testing.T) {
	svc := NewOpenAPIImportService()
	resp, _ := svc.Parse(minimalOpenAPI3, DefaultBackend{Address: "10.0.0.1", Port: 80})
	if len(resp.Routes) == 0 {
		t.Fatal("no routes")
	}
	be := resp.Routes[0].Config.Backends[0]
	if string(be.AddressType) != "ip" {
		t.Fatalf("want addressType ip for 10.0.0.1, got %q", be.AddressType)
	}
}

func TestParse_ExternalBackendFQDNDetectedAsFQDN(t *testing.T) {
	svc := NewOpenAPIImportService()
	resp, _ := svc.Parse(minimalOpenAPI3, DefaultBackend{Address: "api.example.com", Port: 443})
	if len(resp.Routes) == 0 {
		t.Fatal("no routes")
	}
	be := resp.Routes[0].Config.Backends[0]
	if string(be.AddressType) != "fqdn" {
		t.Fatalf("want addressType fqdn for api.example.com, got %q", be.AddressType)
	}
}

func TestParse_DescriptionTruncated(t *testing.T) {
	long := "x"
	for i := 0; i < 600; i++ {
		long += "y"
	}
	spec := "openapi: 3.0.3\ninfo: {title: T, version: \"1\"}\npaths:\n  /a:\n    get:\n      operationId: getA\n      summary: \"" + long + "\"\n"
	svc := NewOpenAPIImportService()
	resp, err := svc.Parse(spec, defaultBackendKubernetes())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.Routes) == 0 {
		t.Fatal("no routes")
	}
	if len(resp.Routes[0].Description) > 500 {
		t.Fatalf("description not truncated, got len %d", len(resp.Routes[0].Description))
	}
}
