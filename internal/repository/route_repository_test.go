package repository_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// requirePostgres skips the test unless INTEGRATION_DB_URL points to a
// reachable Postgres instance. JSONB containment (@>) cannot be tested
// against sqlite.
//
// Example invocation:
//
//	INTEGRATION_DB_URL="postgres://fastgateway:fastgateway@localhost:5432/fastgateway?sslmode=disable" \
//	  go test ./internal/repository/ -run TestRouteRepository_ListByProjectID -v
func requirePostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("INTEGRATION_DB_URL")
	if dsn == "" {
		t.Skip("INTEGRATION_DB_URL not set; skipping Postgres integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	return db
}

// seedProject inserts a user, team, project, and domain, then registers cleanup
// to remove them in reverse FK order when the test ends.
// Returns projectID, domainID, teamID, userID.
func seedProject(t *testing.T, db *gorm.DB) (projectID, domainID, teamID, userID uuid.UUID) {
	t.Helper()
	projectID = uuid.New()
	domainID = uuid.New()
	teamID = uuid.New()
	userID = uuid.New()

	suffix := projectID.String()

	// Insert user (required by projects.created_by, domains.created_by, routes.created_by).
	require.NoError(t, db.Exec(`
		INSERT INTO users (id, username, email, password_hash, role, is_active, created_at, updated_at)
		VALUES (?, ?, ?, '', 'owner', true, NOW(), NOW())`,
		userID, "test-user-"+suffix, "test-"+suffix+"@example.com").Error)

	// Insert team (required by routes.team_id).
	require.NoError(t, db.Exec(`
		INSERT INTO teams (id, name, created_at, updated_at)
		VALUES (?, ?, NOW(), NOW())`,
		teamID, "test-team-"+suffix).Error)

	// Insert project (created_by → user, k8s_api_url and k8s_token_encrypted NOT NULL).
	require.NoError(t, db.Exec(`
		INSERT INTO projects (id, name, k8s_api_url, k8s_token_encrypted, created_by, created_at, updated_at)
		VALUES (?, ?, '', '', ?, NOW(), NOW())`,
		projectID, "test-project-"+suffix, userID).Error)

	// Insert domain (created_by → user).
	require.NoError(t, db.Exec(`
		INSERT INTO domains (id, project_id, name, hostname, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		domainID, projectID, "test-domain-"+domainID.String(), domainID.String()+".test.example.com", userID).Error)

	t.Cleanup(func() {
		// Order matters: routes → domains → projects → teams → users.
		_ = db.Exec(`DELETE FROM routes WHERE domain_id = ?`, domainID).Error
		_ = db.Exec(`DELETE FROM domains WHERE id = ?`, domainID).Error
		_ = db.Exec(`DELETE FROM projects WHERE id = ?`, projectID).Error
		_ = db.Exec(`DELETE FROM teams WHERE id = ?`, teamID).Error
		_ = db.Exec(`DELETE FROM users WHERE id = ?`, userID).Error
	})

	return projectID, domainID, teamID, userID
}

// seedRoute inserts a Route with the given backends/mirrors JSON in config.
// teamID and createdByUserID must already exist in the database.
// Returns the route ID.
func seedRoute(t *testing.T, db *gorm.DB, domainID, teamID, createdByUserID uuid.UUID, name string, backends, mirrors []map[string]any) uuid.UUID {
	t.Helper()
	cfg := map[string]any{}
	if backends != nil {
		cfg["backends"] = backends
	}
	if mirrors != nil {
		cfg["mirrors"] = mirrors
	}
	cfgBytes, err := json.Marshal(cfg)
	require.NoError(t, err)

	id := uuid.New()
	err = db.Exec(`
		INSERT INTO routes (id, domain_id, team_id, name, status, config, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', ?::jsonb, ?, NOW(), NOW())`,
		id, domainID, teamID, name, string(cfgBytes), createdByUserID).Error
	require.NoError(t, err)
	return id
}

func TestRouteRepository_ListByProjectID_FiltersByBackendService(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewRouteRepository(db)

	projectID, domainID, teamID, userID := seedProject(t, db)

	matchingID := seedRoute(t, db, domainID, teamID, userID, "matching",
		[]map[string]any{{"type": "kubernetes", "service": "payments-api", "namespace": "payments", "port": 8080}}, nil)
	seedRoute(t, db, domainID, teamID, userID, "wrong-service",
		[]map[string]any{{"type": "kubernetes", "service": "other", "namespace": "payments", "port": 8080}}, nil)
	seedRoute(t, db, domainID, teamID, userID, "wrong-namespace",
		[]map[string]any{{"type": "kubernetes", "service": "payments-api", "namespace": "other", "port": 8080}}, nil)

	routes, total, err := repo.ListByProjectID(projectID, 1, 50, repository.RouteListFilters{
		BackendService:   "payments-api",
		BackendNamespace: "payments",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, routes, 1)
	assert.Equal(t, matchingID, routes[0].ID)
}

func TestRouteRepository_ListByProjectID_IncludeMirrors(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewRouteRepository(db)

	projectID, domainID, teamID, userID := seedProject(t, db)

	// Route whose mirror (not primary backend) targets payments-api.
	seedRoute(t, db, domainID, teamID, userID, "mirror-only", nil,
		[]map[string]any{{"type": "kubernetes", "service": "payments-api", "namespace": "payments", "port": 8080}})

	// include_mirrors=false → no match
	_, total, err := repo.ListByProjectID(projectID, 1, 50, repository.RouteListFilters{
		BackendService:   "payments-api",
		BackendNamespace: "payments",
		IncludeMirrors:   false,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)

	// include_mirrors=true → match
	routes, total, err := repo.ListByProjectID(projectID, 1, 50, repository.RouteListFilters{
		BackendService:   "payments-api",
		BackendNamespace: "payments",
		IncludeMirrors:   true,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, routes, 1)
}

func TestRouteRepository_ListByProjectID_ExternalBackendsExcluded(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewRouteRepository(db)

	projectID, domainID, teamID, userID := seedProject(t, db)

	// External backend has no service/namespace fields — filter should not match.
	seedRoute(t, db, domainID, teamID, userID, "external",
		[]map[string]any{{"type": "external", "address": "10.0.0.1", "addressType": "ip", "port": 8080}}, nil)

	_, total, err := repo.ListByProjectID(projectID, 1, 50, repository.RouteListFilters{
		BackendService:   "payments-api",
		BackendNamespace: "payments",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}

// Verifies the JOIN to domains scopes results to the requested project — a
// route under a different project's domain must not be returned.
func TestRouteRepository_ListByProjectID_ProjectScoping(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewRouteRepository(db)

	projectA, domainA, teamA, userA := seedProject(t, db)
	_, domainB, teamB, userB := seedProject(t, db)

	// Same backend tuple, different projects.
	seedRoute(t, db, domainA, teamA, userA, "project-a-route",
		[]map[string]any{{"type": "kubernetes", "service": "payments-api", "namespace": "payments", "port": 8080}}, nil)
	seedRoute(t, db, domainB, teamB, userB, "project-b-route",
		[]map[string]any{{"type": "kubernetes", "service": "payments-api", "namespace": "payments", "port": 8080}}, nil)

	routes, total, err := repo.ListByProjectID(projectA, 1, 50, repository.RouteListFilters{
		BackendService:   "payments-api",
		BackendNamespace: "payments",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, routes, 1)
	assert.Equal(t, "project-a-route", routes[0].Name)
}

// Ensure the models package import is used (Route type referenced indirectly
// via repo return values — this blank import silences any "imported and not used" errors
// if the compiler decides the models import is indirect only).
var _ models.Route
