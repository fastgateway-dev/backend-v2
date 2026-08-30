//go:build e2e

package platform

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// previewRouteConfig is a route with enough surface to make the preview
// comparisons meaningful: matches, a weighted backend, a URL rewrite, a
// response header modifier, and a SecurityPolicy. A minimal route would
// let a preview that drops whole sections still pass.
func previewRouteConfig(t *testing.T) (name, path string, cfg services.CreateRouteInput) {
	t.Helper()
	name, path = uniquePath(t)
	allowOrigins := []string{"https://example.com"}
	cfg = services.CreateRouteInput{
		Name:         name,
		SecurityMode: models.SecurityModeGeneral,
		TeamID:       teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeBackend,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: backendNamespace, Service: nginxService, Port: nginxPort, Weight: 100},
			},
			URLRewrite: rewriteTo("/"),
			ResponseHeaderModifier: &models.HeaderModifier{
				Set: []models.HeaderValue{{Name: "X-E2E-Preview", Value: "yes"}},
			},
		},
		SecurityPolicy: &services.SecurityPolicyInput{
			CORS: &models.CORSConfig{AllowOrigins: allowOrigins, AllowMethods: []string{"GET"}},
		},
	}
	return name, path, cfg
}

// previewDirectResponseRouteConfig is a directResponse-type route that ALSO
// sets URLRewrite and RequestHeaderModifier -- fields the deployed
// HTTPRoute for a directResponse route never carries (there is no backend
// to rewrite the path for, or forward a modified request header to).
//
// A directResponse route's HTTPRoute must instead carry an extensionRef
// filter pointing at the route's HTTPRouteFilter (where the actual
// direct-response body/status live), and must NOT carry urlRewrite or
// requestHeaderModifier even though the submitted Config sets them. A
// direct-response fixture that leaves those two fields unset would let a
// preview path that wrongly includes them, or omits the extensionRef,
// pass just as happily as a correct one -- which is exactly the bug this
// test exists to catch.
func previewDirectResponseRouteConfig(t *testing.T) (name, path string, cfg services.CreateRouteInput) {
	t.Helper()
	name, path = uniquePath(t)
	cfg = services.CreateRouteInput{
		Name:   name,
		TeamID: teamID(t),
		Config: models.RouteConfig{
			RouteType: models.RouteTypeDirectResponse,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			DirectResponse: &models.DirectResponseConfig{
				StatusCode:  200,
				ContentType: "application/json",
				Body: &models.DirectResponseBody{
					Type:   models.DirectResponseBodyTypeInline,
					Inline: `{"status":"ok"}`,
				},
			},
			// These two must NOT surface on the deployed (or correctly
			// previewed) HTTPRoute for a directResponse route -- they are
			// only meaningful for backend routes with something to
			// forward a request to. Set here specifically so a preview
			// that wrongly includes them fails this test.
			URLRewrite: rewriteTo("/"),
			RequestHeaderModifier: &models.HeaderModifier{
				Set: []models.HeaderValue{{Name: "X-E2E-Preview-Request", Value: "yes"}},
			},
		},
	}
	return name, path, cfg
}

// TestPreviewCreateDirectResponseMatchesDeployedManifests guards the
// directResponse preview bug: preview used to omit the extensionRef filter
// pointing at the route's HTTPRouteFilter, and wrongly included urlRewrite
// and requestHeaderModifier filters that the deployed route never has. The
// deploy path was always correct, so comparing preview against deployed
// output (rather than against a hand-written expectation) catches any
// future regression that reintroduces the divergence.
//
// TestPreviewCreateMatchesDeployedManifests (below) exercises only a plain
// backend route, which is exactly why it never caught this bug: a backend
// route legitimately carries urlRewrite and requestHeaderModifier, so
// preview and deploy agreeing on those fields proves nothing about whether
// preview correctly special-cases directResponse routes.
func TestPreviewCreateDirectResponseMatchesDeployedManifests(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	_, _, cfg := previewDirectResponseRouteConfig(t)

	var preview services.PreviewCreateResult
	previewPath := "/projects/" + env.ProjectID + "/domains/" + env.DomainID + "/routes/preview"
	if _, err := env.Editor.Do(ctx, http.MethodPost, previewPath, cfg, &preview); err != nil {
		t.Fatalf("preview create (directResponse): %v", err)
	}
	if strings.TrimSpace(preview.ProposedYAML) == "" {
		t.Fatal("preview create (directResponse): proposedYaml is empty")
	}

	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	requireSameObject(t, "HTTPRoute",
		parseYAMLObject(t, "preview HTTPRoute (directResponse)", preview.ProposedYAML),
		deployedObject(t, ctx, httpRouteGVR, route.ID.String()))
}

// TestPreviewCreateMatchesDeployedManifests is the assertion the preview
// endpoints exist for: what the reviewer is shown before approving must be
// what the cluster actually gets.
//
// It previews a create, then deploys the SAME input through the normal
// lifecycle, then reads the real objects back out of Kubernetes and
// requires them to match the preview field for field -- excluding only
// what cannot match by construction (the throwaway UUID the preview mints
// for the not-yet-existing route, and server-assigned metadata; see
// normalizeForComparison).
//
// Checking only that the YAML parses, or contains the route name, would
// pass just as happily if preview and deploy built their manifests from
// divergent code paths -- which is precisely the bug worth catching, since
// nothing else in the system would report it.
func TestPreviewCreateMatchesDeployedManifests(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	_, _, cfg := previewRouteConfig(t)

	var preview services.PreviewCreateResult
	previewPath := "/projects/" + env.ProjectID + "/domains/" + env.DomainID + "/routes/preview"
	if _, err := env.Editor.Do(ctx, http.MethodPost, previewPath, cfg, &preview); err != nil {
		t.Fatalf("preview create: %v", err)
	}
	if strings.TrimSpace(preview.ProposedYAML) == "" {
		t.Fatal("preview create: proposedYaml is empty")
	}
	if strings.TrimSpace(preview.ProposedSecurityPolicyYAML) == "" {
		t.Fatal("preview create: proposedSecurityPolicyYaml is empty even though the input carries a CORS SecurityPolicy")
	}

	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	requireSameObject(t, "HTTPRoute",
		parseYAMLObject(t, "preview HTTPRoute", preview.ProposedYAML),
		deployedObject(t, ctx, httpRouteGVR, route.ID.String()))

	requireSameObject(t, "SecurityPolicy",
		parseYAMLObject(t, "preview SecurityPolicy", preview.ProposedSecurityPolicyYAML),
		deployedObject(t, ctx, harness.SecurityPolicyGVR, route.ID.String()))
}

// TestRouteYAMLsMatchDeployedManifests covers GET /routes/:routeId/yamls,
// the "show me this route's manifests" view. Unlike preview-create this
// describes a route that already exists, so the object name and route-id
// label must match the live objects EXACTLY -- there is no throwaway UUID
// involved. normalizeForComparison blanks both anyway, so this test
// additionally asserts them directly.
func TestRouteYAMLsMatchDeployedManifests(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	_, _, cfg := previewRouteConfig(t)
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	var yamls services.RouteYAMLs
	path := "/projects/" + env.ProjectID + "/domains/" + env.DomainID + "/routes/" + route.ID.String() + "/yamls"
	if _, err := env.Editor.Do(ctx, http.MethodGet, path, nil, &yamls); err != nil {
		t.Fatalf("get route yamls: %v", err)
	}

	deployedRoute := deployedObject(t, ctx, httpRouteGVR, route.ID.String())
	shownRoute := parseYAMLObject(t, "yamls HTTPRoute", yamls.HTTPRouteYAML)

	// Exact, not normalized: this route exists, so the reported name and
	// owner label must be the live object's own.
	if got, want := shownRoute.GetName(), deployedRoute.GetName(); got != want {
		t.Errorf("route yamls: HTTPRoute name=%q, want the deployed object's name %q", got, want)
	}
	if got := shownRoute.GetLabels()["fastgateway.dev/route-id"]; got != route.ID.String() {
		t.Errorf("route yamls: HTTPRoute route-id label=%q, want %q", got, route.ID.String())
	}
	requireSameObject(t, "HTTPRoute", shownRoute, deployedRoute)

	requireSameObject(t, "SecurityPolicy",
		parseYAMLObject(t, "yamls SecurityPolicy", yamls.SecurityPolicyYAML),
		deployedObject(t, ctx, harness.SecurityPolicyGVR, route.ID.String()))
}

// TestPreviewUpdateShowsCurrentAndProposed covers POST
// /routes/:routeId/preview. The result carries both sides of the change,
// and both have to be right for a reviewer to judge it: "current" must be
// what is deployed NOW, and "proposed" must differ in exactly the field
// being changed and nowhere else.
func TestPreviewUpdateShowsCurrentAndProposed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	_, path, cfg := previewRouteConfig(t)
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	// Change one thing: the response header value.
	updated := services.UpdateRouteInput{
		Config:         cfg.Config,
		SecurityPolicy: cfg.SecurityPolicy,
	}
	updated.Config.ResponseHeaderModifier = &models.HeaderModifier{
		Set: []models.HeaderValue{{Name: "X-E2E-Preview", Value: "changed"}},
	}

	var preview services.PreviewUpdateResult
	previewPath := "/projects/" + env.ProjectID + "/domains/" + env.DomainID + "/routes/" + route.ID.String() + "/preview"
	if _, err := env.Editor.Do(ctx, http.MethodPost, previewPath, updated, &preview); err != nil {
		t.Fatalf("preview update: %v", err)
	}

	// "current" is a claim about the cluster's present state, so check it
	// against the cluster.
	requireSameObject(t, "current HTTPRoute",
		parseYAMLObject(t, "preview currentYaml", preview.CurrentYAML),
		deployedObject(t, ctx, httpRouteGVR, route.ID.String()))

	if preview.ProposedYAML == preview.CurrentYAML {
		t.Fatal("preview update: proposedYaml is identical to currentYaml, but the input changed X-E2E-Preview")
	}
	if !strings.Contains(preview.ProposedYAML, "changed") {
		t.Errorf("preview update: proposedYaml does not carry the new header value %q:\n%s", "changed", preview.ProposedYAML)
	}
	if strings.Contains(preview.CurrentYAML, "changed") {
		t.Errorf("preview update: currentYaml carries the PROPOSED header value -- it must describe the deployed state:\n%s", preview.CurrentYAML)
	}
	// Nothing else moved: the path match is untouched by this update.
	if !strings.Contains(preview.ProposedYAML, path) {
		t.Errorf("preview update: proposedYaml lost the route's path match %q:\n%s", path, preview.ProposedYAML)
	}
}

// TestPreviewDeleteShowsWhatWouldBeRemoved covers GET
// /routes/:routeId/preview-delete -- the confirmation a reviewer sees
// before approving a deletion. Everything it lists must be what is
// actually in the cluster; listing a stale or invented manifest here would
// have someone approve the removal of something other than what they were
// shown.
func TestPreviewDeleteShowsWhatWouldBeRemoved(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	_, _, cfg := previewRouteConfig(t)
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	var preview services.PreviewDeleteResult
	path := "/projects/" + env.ProjectID + "/domains/" + env.DomainID + "/routes/" + route.ID.String() + "/preview-delete"
	if _, err := env.Editor.Do(ctx, http.MethodGet, path, nil, &preview); err != nil {
		t.Fatalf("preview delete: %v", err)
	}

	requireSameObject(t, "HTTPRoute to be deleted",
		parseYAMLObject(t, "preview-delete currentYaml", preview.CurrentYAML),
		deployedObject(t, ctx, httpRouteGVR, route.ID.String()))

	requireSameObject(t, "SecurityPolicy to be deleted",
		parseYAMLObject(t, "preview-delete currentSecurityPolicyYaml", preview.CurrentSecurityPolicyYAML),
		deployedObject(t, ctx, harness.SecurityPolicyGVR, route.ID.String()))
}
