//go:build e2e

package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Env bundles everything a test package needs to talk to a seeded
// FastGateway deployment: authenticated API clients for each role, a
// data-plane client, a Kubernetes client, and the seeded project/domain/
// team IDs (created by cmd/e2e-seed, the Go replacement for bootstrap.py).
// Build it once per package -- typically from TestMain -- and share it
// across parallel tests.
type Env struct {
	Cfg      *Config
	Admin    *API
	Editor   *API
	Approver *API
	GW       *Gateway
	Kube     *Kube

	ProjectID string
	DomainID  string
	TeamID    string
}

// NewEnv builds an Env from the ambient environment (see Config.FromEnv):
// it logs in as the admin, editor, and approver users, resolves the seeded
// project/domain/"dev" team by name, and builds the gateway and Kubernetes
// clients. Intended to be called once from a package's TestMain, e.g.:
//
//	var env *harness.Env
//
//	func TestMain(m *testing.M) {
//		var err error
//		env, err = harness.NewEnv(context.Background())
//		if err != nil {
//			log.Fatal(err)
//		}
//		os.Exit(m.Run())
//	}
func NewEnv(ctx context.Context) (*Env, error) {
	cfg, err := FromEnv()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	admin, err := Login(ctx, cfg, cfg.AdminUser, cfg.AdminPass)
	if err != nil {
		return nil, fmt.Errorf("login as admin (%s): %w", cfg.AdminUser, err)
	}
	editor, err := Login(ctx, cfg, cfg.EditorUser, cfg.EditorPass)
	if err != nil {
		return nil, fmt.Errorf("login as editor (%s): %w", cfg.EditorUser, err)
	}
	approver, err := Login(ctx, cfg, cfg.ApproverUser, cfg.ApproverPass)
	if err != nil {
		return nil, fmt.Errorf("login as approver (%s): %w", cfg.ApproverUser, err)
	}

	project, err := admin.GetProjectByName(ctx, cfg.ProjectName)
	if err != nil {
		return nil, fmt.Errorf("resolve project %q: %w", cfg.ProjectName, err)
	}
	domain, err := admin.GetDomainByName(ctx, project.ID.String(), cfg.DomainName)
	if err != nil {
		return nil, fmt.Errorf("resolve domain %q: %w", cfg.DomainName, err)
	}
	team, err := admin.GetTeamByName(ctx, project.ID.String(), "dev")
	if err != nil {
		return nil, fmt.Errorf("resolve team \"dev\": %w", err)
	}

	kube, err := NewKube(cfg)
	if err != nil {
		return nil, fmt.Errorf("build kube client: %w", err)
	}

	return &Env{
		Cfg:       cfg,
		Admin:     admin,
		Editor:    editor,
		Approver:  approver,
		GW:        NewGateway(cfg),
		Kube:      kube,
		ProjectID: project.ID.String(),
		DomainID:  domain.ID.String(),
		TeamID:    team.TeamID.String(),
	}, nil
}

var slugSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

// UniqueName generates a resource name unique to this test run:
// "e2e-<test-slug>-<8 hex chars>". Every fixture-created resource must use
// it. The predecessor suite used fixed names ("reg-*"), so a crashed run
// left conflicting state behind and the suite could never run in parallel.
func UniqueName(t *testing.T) string {
	t.Helper()

	slug := strings.ToLower(t.Name())
	slug = strings.ReplaceAll(slug, "/", "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	slug = slugSanitizer.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "test"
	}
	// Kubernetes object names are capped at 63 chars; leave room for the
	// "e2e-" prefix and "-<8hex>" suffix.
	const maxSlugLen = 63 - len("e2e-") - len("-12345678")
	if len(slug) > maxSlugLen {
		slug = slug[:maxSlugLen]
		slug = strings.Trim(slug, "-")
	}

	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("UniqueName: read random suffix: %v", err)
	}
	return fmt.Sprintf("e2e-%s-%s", slug, hex.EncodeToString(buf[:]))
}

// Fixture creates routes through the full lifecycle -- submit as editor,
// approve as approver, deploy -- and guarantees teardown even if the test
// fails partway through.
type Fixture struct {
	t   *testing.T
	env *Env
}

// NewFixture returns a Fixture bound to t and env.
func NewFixture(t *testing.T, env *Env) *Fixture {
	t.Helper()
	return &Fixture{t: t, env: env}
}

// Route creates a route from cfg (typically a services.CreateRouteInput
// value, or any value that marshals to the route-creation JSON body),
// approves every pending stage as the approver, deploys it, and registers
// t.Cleanup to tear it down. It fails the test via t.Fatalf if creation,
// approval, or deploy errors.
//
// Cleanup deletes the route (also an approval-gated action: RouteService.Delete
// always creates a "delete" approval when the project has approvals enabled,
// same as create/update) as admin, approves the resulting pending-delete
// approval if one was created, then deploys again so the deletion is
// actually pushed to the cluster. Unlike the predecessor fixture -- which
// only emitted a warning on cleanup failure, letting leaked routes
// accumulate silently -- every step's failure is reported via t.Errorf so a
// leak fails the test.
func (f *Fixture) Route(cfg any) Route {
	t := f.t
	t.Helper()
	ctx := context.Background()

	route, err := f.env.Editor.CreateRoute(ctx, f.env.ProjectID, f.env.DomainID, cfg)
	if err != nil {
		t.Fatalf("fixture: create route: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Mirror the creation path's role split: the submitter and the
		// approver must be different users, or the backend's self-approval
		// guard (ApprovalService.ApproveStage, "submitter cannot approve
		// their own submission") rejects the approval. Editor submits,
		// Approver approves, Editor deploys.
		if err := f.env.Editor.DeleteRoute(cleanupCtx, f.env.ProjectID, f.env.DomainID, route.ID.String()); err != nil {
			t.Errorf("fixture cleanup: delete route %s (%s): %v", route.Name, route.ID, err)
			return
		}
		if err := f.env.Approver.ApproveAllStages(cleanupCtx, f.env.ProjectID, route.ID.String()); err != nil &&
			!strings.Contains(err.Error(), "no pending approval found") {
			t.Errorf("fixture cleanup: approve delete for route %s (%s): %v", route.Name, route.ID, err)
			return
		}
		if err := f.env.Editor.DeployRoute(cleanupCtx, f.env.ProjectID, f.env.DomainID, route.ID.String()); err != nil {
			t.Errorf("fixture cleanup: deploy delete for route %s (%s): %v", route.Name, route.ID, err)
		}
	})

	if err := f.env.Approver.ApproveAllStages(ctx, f.env.ProjectID, route.ID.String()); err != nil {
		t.Fatalf("fixture: approve route %s (%s): %v", route.Name, route.ID, err)
	}

	if err := f.env.Editor.DeployRoute(ctx, f.env.ProjectID, f.env.DomainID, route.ID.String()); err != nil {
		t.Fatalf("fixture: deploy route %s (%s): %v", route.Name, route.ID, err)
	}

	deployed, err := f.env.Editor.GetRoute(ctx, f.env.ProjectID, f.env.DomainID, route.ID.String())
	if err != nil {
		t.Fatalf("fixture: fetch deployed route %s (%s): %v", route.Name, route.ID, err)
	}
	return deployed
}
