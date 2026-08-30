package services

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// maxDescriptionLen caps description length so multi-paragraph OpenAPI
// `description` blocks don't bloat the route record. UI shows the full
// summary/description in the create page if needed.
const maxDescriptionLen = 500

var openAPIParamRegex = regexp.MustCompile(`\{[^/}]+\}`)

// pathToMatcher converts an OpenAPI path into a FastGateway PathMatch type+value.
// Static paths (no parameters) → ("Exact", path). Parameterized paths →
// ("RegularExpression", "^"+path-with-params-replaced-with-[^/]+ +"$").
func pathToMatcher(path string) (string, string) {
	if !openAPIParamRegex.MatchString(path) {
		return "Exact", path
	}
	regex := openAPIParamRegex.ReplaceAllString(path, "[^/]+")
	return "RegularExpression", "^" + regex + "$"
}

// OpenAPIImportService parses OpenAPI 3.x specs into ParsedRoute slices.
// Stateless and pure — no I/O, no DB.
type OpenAPIImportService struct{}

// NewOpenAPIImportService returns a new service. Stateless; safe to share.
func NewOpenAPIImportService() *OpenAPIImportService {
	return &OpenAPIImportService{}
}

// Parse ingests a raw spec string (YAML or JSON) and returns parsed routes,
// warnings, renames, and spec info. Returns a spec-level error for unrecoverable
// failures (malformed input, no paths, unsupported version).
func (s *OpenAPIImportService) Parse(spec string, backend DefaultBackend) (*OpenAPIImportResponse, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, fmt.Errorf("spec is empty")
	}

	jsonBytes, format, err := specToJSON(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to parse spec: %w", err)
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromData(jsonBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid OpenAPI document: %w", err)
	}

	if doc.OpenAPI == "" {
		return nil, fmt.Errorf("missing 'openapi' field; OpenAPI 2.0 (Swagger) is not supported — convert to 3.x first")
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		return nil, fmt.Errorf("unsupported OpenAPI version %q; only 3.x is supported", doc.OpenAPI)
	}
	if doc.Paths == nil || doc.Paths.Len() == 0 {
		return nil, fmt.Errorf("spec has no paths")
	}

	resp := &OpenAPIImportResponse{
		Routes:   []ParsedRoute{},
		Warnings: []ImportWarning{},
		Renames:  []Rename{},
		SpecInfo: SpecInfo{
			Title:   safeInfoTitle(doc),
			Version: safeInfoVersion(doc),
			Format:  format,
		},
	}

	usedNames := map[string]int{}
	index := 0

	for _, path := range doc.Paths.InMatchingOrder() {
		pathItem := doc.Paths.Value(path)
		if pathItem == nil {
			continue
		}
		ops := pathItemOperations(pathItem)
		for _, op := range ops {
			route, warnings, rename, ok := buildRoute(op.method, path, op.operation, backend, usedNames, index)
			source := op.method + " " + path
			resp.Warnings = append(resp.Warnings, prefixSource(warnings, source)...)
			if rename != nil {
				resp.Renames = append(resp.Renames, *rename)
			}
			if !ok {
				continue
			}
			resp.Routes = append(resp.Routes, route)
			index++
		}
	}

	return resp, nil
}

// specToJSON normalises spec text to JSON bytes, returning a format hint
// ("openapi-3.0" | "openapi-3.1"). Tries JSON first, falls back to YAML.
func specToJSON(spec string) ([]byte, string, error) {
	trimmed := strings.TrimSpace(spec)
	var raw map[string]interface{}

	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal([]byte(spec), &raw); err != nil {
			return nil, "", err
		}
	} else {
		if err := yaml.Unmarshal([]byte(spec), &raw); err != nil {
			return nil, "", err
		}
	}

	out, err := json.Marshal(raw)
	if err != nil {
		return nil, "", err
	}

	format := "openapi-3.0"
	if v, ok := raw["openapi"].(string); ok && strings.HasPrefix(v, "3.1") {
		format = "openapi-3.1"
	}
	return out, format, nil
}

func safeInfoTitle(doc *openapi3.T) string {
	if doc.Info == nil {
		return ""
	}
	return doc.Info.Title
}

func safeInfoVersion(doc *openapi3.T) string {
	if doc.Info == nil {
		return ""
	}
	return doc.Info.Version
}

type opEntry struct {
	method    string
	operation *openapi3.Operation
}

func pathItemOperations(pi *openapi3.PathItem) []opEntry {
	out := []opEntry{}
	if pi.Get != nil {
		out = append(out, opEntry{"GET", pi.Get})
	}
	if pi.Put != nil {
		out = append(out, opEntry{"PUT", pi.Put})
	}
	if pi.Post != nil {
		out = append(out, opEntry{"POST", pi.Post})
	}
	if pi.Delete != nil {
		out = append(out, opEntry{"DELETE", pi.Delete})
	}
	if pi.Patch != nil {
		out = append(out, opEntry{"PATCH", pi.Patch})
	}
	if pi.Head != nil {
		out = append(out, opEntry{"HEAD", pi.Head})
	}
	if pi.Options != nil {
		out = append(out, opEntry{"OPTIONS", pi.Options})
	}
	if pi.Trace != nil {
		out = append(out, opEntry{"TRACE", pi.Trace})
	}
	return out
}

func prefixSource(ws []ImportWarning, source string) []ImportWarning {
	out := make([]ImportWarning, len(ws))
	for i, w := range ws {
		w.Source = source
		out[i] = w
	}
	return out
}

// buildRoute maps a single OpenAPI operation into a ParsedRoute.
//
// kin-openapi's loader is all-or-nothing: a malformed operation makes
// LoadFromData fail at the spec level, so this never receives a nil op
// from pathItemOperations (which only walks non-nil pointer fields).
// Per-operation skip warnings would need a manual operation walker rather
// than relying on the loader; for now we accept all-or-nothing parsing.
func buildRoute(method, path string, op *openapi3.Operation, backend DefaultBackend, used map[string]int, index int) (ParsedRoute, []ImportWarning, *Rename, bool) {
	// Name pipeline
	originalSource := op.OperationID
	if originalSource == "" {
		originalSource = strings.ToLower(method) + " " + path
	}
	candidate, reasons := generateRouteName(op.OperationID, method, path, index)
	final := disambiguate(candidate, used)

	var rename *Rename
	switch {
	case final != candidate:
		rename = &Rename{Original: originalSource, Final: final, Reason: "duplicate"}
	case len(reasons) > 0:
		// First reason wins (sanitized takes precedence over truncated for clarity)
		rename = &Rename{Original: originalSource, Final: final, Reason: reasons[0]}
	}

	// Path matcher
	matchType, matchValue := pathToMatcher(path)

	// Backend (always one, from default)
	be := models.RouteBackend{Port: backend.Port}
	if backend.Service != "" {
		be.Type = models.BackendTypeKubernetes
		be.Service = backend.Service
		be.Namespace = backend.Namespace
	} else {
		be.Type = models.BackendTypeExternal
		be.Address = backend.Address
		if net.ParseIP(backend.Address) != nil {
			be.AddressType = models.ExternalAddressTypeIP
		} else {
			be.AddressType = models.ExternalAddressTypeFQDN
		}
	}

	description := op.Summary
	if description == "" {
		description = op.Description
	}
	if len(description) > maxDescriptionLen {
		description = description[:maxDescriptionLen-3] + "..."
	}

	tag := ""
	if len(op.Tags) > 0 {
		tag = op.Tags[0]
	}

	route := ParsedRoute{
		Name:         final,
		Description:  description,
		Protocol:     "http",
		SecurityMode: "general",
		Tag:          tag,
		Config: models.RouteConfig{
			Matches: []models.RouteMatch{{
				Path:   &models.PathMatch{Type: matchType, Value: matchValue},
				Method: method,
			}},
			Backends: []models.RouteBackend{be},
		},
	}
	return route, nil, rename, true
}
