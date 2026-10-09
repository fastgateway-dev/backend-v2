package repository_test

import (
	"encoding/json"
	"errors"
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

func TestRouteRepository_CountByStreamID(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	streams := repository.NewStreamRepository(db)
	routes := repository.NewRouteRepository(db)

	s := &models.Stream{ProjectID: projectID, Name: "cnt-gw", Namespace: "fastgateway-system",
		GatewayTemplateID: tmplID, K8sGatewayName: "str-cnt-gw", K8sGatewayClass: "public-lb"}
	require.NoError(t, streams.Create(s))

	n, err := routes.CountByStreamID(s.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	for i, port := range []int{5432, 6379} {
		require.NoError(t, db.Exec(`
			INSERT INTO routes (id, stream_id, team_id, name, protocol, listener_port, status, config, created_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'tcp', ?, 'active', '{}'::jsonb, ?, NOW(), NOW())`,
			uuid.New(), s.ID, teamID, "l4-route-"+string(rune('a'+i)), port, userID).Error)
	}

	n, err = routes.CountByStreamID(s.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	// A different stream is unaffected.
	n, err = routes.CountByStreamID(uuid.New())
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

func TestRouteRepository_ListActiveByStreamID(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	streams := repository.NewStreamRepository(db)
	routes := repository.NewRouteRepository(db)

	s := &models.Stream{ProjectID: projectID, Name: "act-gw", Namespace: "fastgateway-system",
		GatewayTemplateID: tmplID, K8sGatewayName: "str-act-gw", K8sGatewayClass: "public-lb"}
	require.NoError(t, streams.Create(s))
	other := &models.Stream{ProjectID: projectID, Name: "act-other", Namespace: "fastgateway-system",
		GatewayTemplateID: tmplID, K8sGatewayName: "str-act-other", K8sGatewayClass: "public-lb"}
	require.NoError(t, streams.Create(other))

	insert := func(streamID uuid.UUID, name, status string, port int) {
		require.NoError(t, db.Exec(`
			INSERT INTO routes (id, stream_id, team_id, name, protocol, listener_port, status, config, created_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'tcp', ?, ?, '{}'::jsonb, ?, NOW(), NOW())`,
			uuid.New(), streamID, teamID, name, port, status, userID).Error)
	}
	// Live in the cluster: active, plus active routes with a pending change.
	insert(s.ID, "live-active", "active", 5432)
	insert(s.ID, "live-pending-update", "pending_update", 5433)
	insert(s.ID, "live-pending-delete", "pending_delete", 5434)
	insert(s.ID, "live-pending-deploy", "pending_deploy", 5435)
	// Not yet (or never) deployed.
	insert(s.ID, "not-pending-create", "pending_create", 6000)
	insert(s.ID, "not-approved", "approved", 6001)
	insert(s.ID, "not-rejected", "rejected", 6002)
	// Another stream's route.
	insert(other.ID, "other-active", "active", 7000)

	got, err := routes.ListActiveByStreamID(s.ID)
	require.NoError(t, err)
	names := make([]string, 0, len(got))
	for _, r := range got {
		names = append(names, r.Name)
	}
	assert.ElementsMatch(t, []string{"live-active", "live-pending-update", "live-pending-delete", "live-pending-deploy"}, names)

	none, err := routes.ListActiveByStreamID(uuid.New())
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestRouteRepository_L4PersistWritesListenerPortColumn(t *testing.T) {
	db := requirePostgres(t)
	projectID, domainID, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	streams := repository.NewStreamRepository(db)
	routes := repository.NewRouteRepository(db)

	s := &models.Stream{ProjectID: projectID, Name: "lp-gw", Namespace: "fastgateway-system",
		GatewayTemplateID: tmplID, K8sGatewayName: "str-lp-gw", K8sGatewayClass: "public-lb"}
	require.NoError(t, streams.Create(s))

	columnOf := func(id uuid.UUID) *int {
		var port *int
		require.NoError(t, db.Raw(`SELECT listener_port FROM routes WHERE id = ?`, id).Scan(&port).Error)
		return port
	}
	newL4 := func(name string, port int) *models.Route {
		return &models.Route{StreamID: &s.ID, TeamID: teamID, Name: name, Protocol: models.RouteProtocolTCP,
			Status: models.RouteStatusPendingCreate, CreatedBy: userID,
			Config: models.RouteConfig{ListenerPort: port}}
	}

	// Create mirrors Config.ListenerPort into the column.
	r := newL4("lp-a", 5432)
	require.NoError(t, routes.Create(r))
	got := columnOf(r.ID)
	require.NotNil(t, got, "listener_port column must be written on L4 create")
	assert.Equal(t, 5432, *got)

	// Update keeps the column in step with the config.
	r.Config.ListenerPort = 5433
	require.NoError(t, routes.Update(r))
	got = columnOf(r.ID)
	require.NotNil(t, got, "listener_port column must be written on L4 update")
	assert.Equal(t, 5433, *got)

	// The unique index (stream_id, protocol, listener_port) is now live.
	dup := newL4("lp-b", 5433)
	assert.Error(t, routes.Create(dup), "same stream/protocol/port must violate idx_route_stream_proto_port")
	// ...but the same port on the other transport is fine.
	udp := newL4("lp-c", 5433)
	udp.Protocol = models.RouteProtocolUDP
	assert.NoError(t, routes.Create(udp))

	// Domain-owned (HTTP) routes leave the column NULL.
	httpRoute := &models.Route{DomainID: &domainID, TeamID: teamID, Name: "lp-http", Protocol: models.RouteProtocolHTTP,
		Status: models.RouteStatusPendingCreate, CreatedBy: userID}
	require.NoError(t, routes.Create(httpRoute))
	assert.Nil(t, columnOf(httpRoute.ID))
}

// The unique index must agree with the service-level collision check: a
// rejected route never reaches the cluster, so it does not hold its port.
func TestRouteRepository_L4UniqueIndex_RejectedRouteDoesNotHoldPort(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	streams := repository.NewStreamRepository(db)
	routes := repository.NewRouteRepository(db)

	s := &models.Stream{ProjectID: projectID, Name: "rej-gw", Namespace: "fastgateway-system",
		GatewayTemplateID: tmplID, K8sGatewayName: "str-rej-gw", K8sGatewayClass: "public-lb"}
	require.NoError(t, streams.Create(s))
	newL4 := func(name string, status models.RouteStatus) *models.Route {
		return &models.Route{StreamID: &s.ID, TeamID: teamID, Name: name, Protocol: models.RouteProtocolTCP,
			Status: status, CreatedBy: userID, Config: models.RouteConfig{ListenerPort: 5432}}
	}

	rejected := newL4("rej-a", models.RouteStatusRejected)
	require.NoError(t, routes.Create(rejected))

	// A rejected route does not block an active route on the same port...
	active := newL4("rej-b", models.RouteStatusActive)
	require.NoError(t, routes.Create(active), "rejected route must not hold the port in the unique index")
	// ...nor a second rejected one.
	require.NoError(t, routes.Create(newL4("rej-c", models.RouteStatusRejected)))

	// Two non-rejected routes on the same (stream, protocol, port) still conflict.
	assert.Error(t, routes.Create(newL4("rej-d", models.RouteStatusPendingCreate)))

	// A rejected route moving back to a live status re-claims the port.
	rejected.Status = models.RouteStatusPendingUpdate
	assert.Error(t, routes.Update(rejected), "un-rejecting onto a held port must violate the index")
}

func TestRouteRepository_ExistsByStreamAndName_And_ListByStreamID(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	streams := repository.NewStreamRepository(db)
	routes := repository.NewRouteRepository(db)

	mk := func(name string) *models.Stream {
		s := &models.Stream{ProjectID: projectID, Name: name, Namespace: "fastgateway-system",
			GatewayTemplateID: tmplID, K8sGatewayName: "str-" + name, K8sGatewayClass: "public-lb"}
		require.NoError(t, streams.Create(s))
		return s
	}
	s1, s2 := mk("ls-one"), mk("ls-two")
	add := func(s *models.Stream, name string, port int, status models.RouteStatus) {
		require.NoError(t, routes.Create(&models.Route{StreamID: &s.ID, TeamID: teamID, Name: name,
			Protocol: models.RouteProtocolTCP, Status: status, CreatedBy: userID,
			Config: models.RouteConfig{ListenerPort: port}}))
	}
	add(s1, "pg", 5432, models.RouteStatusActive)
	add(s1, "redis", 6379, models.RouteStatusPendingCreate)
	add(s2, "pg", 5432, models.RouteStatusActive)

	ok, err := routes.ExistsByStreamAndName(s1.ID, "pg")
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = routes.ExistsByStreamAndName(s1.ID, "kafka")
	require.NoError(t, err)
	assert.False(t, ok)

	got, total, err := routes.ListByStreamID(s1.ID, 1, 20, nil, "")
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, got, 2)
	assert.Equal(t, "pg", got[0].Name, "ordered by name; other streams excluded")
	assert.Equal(t, "redis", got[1].Name)

	got, total, err = routes.ListByStreamID(s1.ID, 1, 20, nil, string(models.RouteStatusActive))
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, got, 1)

	got, total, err = routes.ListByStreamID(s1.ID, 2, 1, nil, "")
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "total counts all pages")
	require.Len(t, got, 1)
	assert.Equal(t, "redis", got[0].Name)
}

// The concurrent-create race loses at idx_route_stream_proto_port with a
// Postgres unique violation; the service maps it by SQLSTATE 23505, so the
// real driver error must expose it.
func TestRouteRepository_L4UniqueViolation_ExposesSQLState23505(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	streams := repository.NewStreamRepository(db)
	routes := repository.NewRouteRepository(db)

	s := &models.Stream{ProjectID: projectID, Name: "race-gw", Namespace: "fastgateway-system",
		GatewayTemplateID: tmplID, K8sGatewayName: "str-race-gw", K8sGatewayClass: "public-lb"}
	require.NoError(t, streams.Create(s))
	newL4 := func(name string) *models.Route {
		return &models.Route{StreamID: &s.ID, TeamID: teamID, Name: name, Protocol: models.RouteProtocolTCP,
			Status: models.RouteStatusPendingCreate, CreatedBy: userID, Config: models.RouteConfig{ListenerPort: 5432}}
	}
	require.NoError(t, routes.Create(newL4("race-a")))

	err := routes.Create(newL4("race-b"))
	require.Error(t, err)
	var sqlErr interface{ SQLState() string }
	require.True(t, errors.As(err, &sqlErr), "driver error must expose SQLState(): %T", err)
	assert.Equal(t, "23505", sqlErr.SQLState())
}
