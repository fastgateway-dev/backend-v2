package repository_test

import (
	"os"
	"strings"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// insertTemplate inserts a DomainTemplate for the project and registers cleanup.
// Capability is expressed through the listener list: a stream template carries
// a TCP/UDP range listener, a domain template an HTTPS listener.
// userID must already exist (domain_templates.created_by).
func insertTemplate(t *testing.T, db *gorm.DB, projectID, userID uuid.UUID, enableStream bool) uuid.UUID {
	t.Helper()
	listeners := models.Listeners{{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate}}
	if enableStream {
		listeners = models.Listeners{{Name: "tcpudp", Protocol: models.ListenerTCP, PortRangeMin: 1, PortRangeMax: 65535}}
	}
	tmpl := &models.DomainTemplate{
		ProjectID: projectID,
		Name:      "test-tmpl-" + uuid.NewString(),
		CreatedBy: userID,
		Listeners: listeners,
	}
	require.NoError(t, db.Create(tmpl).Error)
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM streams WHERE gateway_template_id = ?`, tmpl.ID).Error
		_ = db.Exec(`DELETE FROM domain_templates WHERE id = ?`, tmpl.ID).Error
	})
	return tmpl.ID
}

func TestStreamRepository_CRUD(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, _, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)

	s := &models.Stream{ProjectID: projectID, Name: "db-gw", Namespace: "fastgateway-system",
		GatewayTemplateID: tmplID, K8sGatewayName: "str-db-gw", K8sGatewayClass: "public-lb"}
	require.NoError(t, repo.Create(s))

	got, err := repo.GetByID(s.ID)
	require.NoError(t, err)
	assert.Equal(t, "db-gw", got.Name)
	assert.Equal(t, "pending", got.Status)

	list, err := repo.ListByProjectID(projectID)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	count, err := repo.CountByGatewayTemplateID(tmplID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	got.Status = "active"
	got.StatusMessage = "ok"
	require.NoError(t, repo.Update(got))
	updated, err := repo.GetByID(s.ID)
	require.NoError(t, err)
	assert.Equal(t, "active", updated.Status)
	assert.Equal(t, "ok", updated.StatusMessage)

	require.NoError(t, repo.Delete(s.ID))
	count, err = repo.CountByGatewayTemplateID(tmplID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

// insertL4Route inserts a tcp/udp route on a stream with the given listener
// port in config. teamID/userID must already exist.
func insertL4Route(t *testing.T, db *gorm.DB, streamID, teamID, userID uuid.UUID, protocol string, port int, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO routes (id, stream_id, team_id, name, protocol, security_mode, status, config, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'general', ?, jsonb_build_object('listenerPort', ?::int), ?, NOW(), NOW())`,
		id, streamID, teamID, "r-"+id.String(), protocol, status, port, userID).Error)
	t.Cleanup(func() { _ = db.Exec(`DELETE FROM routes WHERE id = ?`, id).Error })
	return id
}

func TestStreamRepository_UsedPortsByStream(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)
	s := &models.Stream{ProjectID: projectID, Name: "ports", Namespace: "fastgateway-system", GatewayTemplateID: tmplID}
	require.NoError(t, repo.Create(s))
	other := &models.Stream{ProjectID: projectID, Name: "ports-other", Namespace: "fastgateway-system", GatewayTemplateID: tmplID}
	require.NoError(t, repo.Create(other))

	tcp := insertL4Route(t, db, s.ID, teamID, userID, "tcp", 5432, "active")
	insertL4Route(t, db, s.ID, teamID, userID, "udp", 53, "pending_create")
	insertL4Route(t, db, s.ID, teamID, userID, "tcp", 9999, "rejected")
	insertL4Route(t, db, other.ID, teamID, userID, "tcp", 7000, "active")

	got, err := repo.UsedPortsByStream(s.ID, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []repository.PortUse{{Transport: "TCP", Port: 5432}, {Transport: "UDP", Port: 53}}, got)

	got, err = repo.UsedPortsByStream(s.ID, &tcp)
	require.NoError(t, err)
	assert.Equal(t, []repository.PortUse{{Transport: "UDP", Port: 53}}, got)
}

func TestStreamRepository_UsedL4Ports_AcrossStreamsOfTemplate(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	otherTmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)
	mk := func(name string, tmpl uuid.UUID) uuid.UUID {
		s := &models.Stream{ProjectID: projectID, Name: name, Namespace: "fastgateway-system", GatewayTemplateID: tmpl}
		require.NoError(t, repo.Create(s))
		return s.ID
	}
	a, b, c := mk("a", tmplID), mk("b", tmplID), mk("c", otherTmplID)
	ra := insertL4Route(t, db, a, teamID, userID, "tcp", 5432, "active")
	insertL4Route(t, db, b, teamID, userID, "udp", 5353, "active")
	insertL4Route(t, db, c, teamID, userID, "tcp", 6000, "active")

	got, err := repo.UsedL4Ports(tmplID, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []repository.PortUse{{Transport: "TCP", Port: 5432}, {Transport: "UDP", Port: 5353}}, got)

	got, err = repo.UsedL4Ports(tmplID, &ra)
	require.NoError(t, err)
	assert.Equal(t, []repository.PortUse{{Transport: "UDP", Port: 5353}}, got)
}

func TestDomainRepository_UsedPortsByTemplate(t *testing.T) {
	db := requirePostgres(t)
	projectID, domainID, _, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, false)
	require.NoError(t, db.Exec(`UPDATE domain_templates SET listeners = ? WHERE id = ?`,
		`[{"name":"http","protocol":"HTTP","port":8080},{"name":"https","protocol":"HTTPS","port":8443,"tlsMode":"Terminate"},{"name":"unbound","protocol":"HTTP","port":9090}]`, tmplID).Error)
	require.NoError(t, db.Exec(`UPDATE domains SET domain_template_id = ?, bound_listeners = ? WHERE id = ?`,
		tmplID, `["http","https"]`, domainID).Error)

	got, err := repository.NewDomainRepository(db).UsedPortsByTemplate(tmplID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []repository.PortUse{{Transport: "TCP", Port: 8080}, {Transport: "TCP", Port: 8443}}, got)
}

// insertRouteApproval inserts an approval for a route whose snapshot proposes
// listenerPort. projectID/userID must already exist.
func insertRouteApproval(t *testing.T, db *gorm.DB, projectID, userID, routeID uuid.UUID, action, status string, port int) {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO approvals (id, project_id, entity_type, entity_id, action, config_snapshot, submitted_by, status, created_at)
		VALUES (?, ?, 'route', ?, ?, jsonb_build_object('routeConfig', jsonb_build_object('listenerPort', ?::int)), ?, ?, NOW())`,
		id, projectID, routeID, action, port, userID, status).Error)
	t.Cleanup(func() { _ = db.Exec(`DELETE FROM approvals WHERE id = ?`, id).Error })
}

func newStream(t *testing.T, repo *repository.StreamRepository, projectID, tmplID uuid.UUID, name string, ports models.StreamPorts) *models.Stream {
	t.Helper()
	s := &models.Stream{ProjectID: projectID, Name: name, Namespace: "fastgateway-system", GatewayTemplateID: tmplID, Ports: ports}
	require.NoError(t, repo.Create(s))
	return s
}

func TestStreamRepository_UsedPortsByStream_IncludesPendingApprovals(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)
	s := newStream(t, repo, projectID, tmplID, "pending-ports", nil)

	a := insertL4Route(t, db, s.ID, teamID, userID, "tcp", 5432, "active")
	insertRouteApproval(t, db, projectID, userID, a, "update", "pending", 5433)

	got, err := repo.UsedPortsByStream(s.ID, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []repository.PortUse{{Transport: "TCP", Port: 5432}, {Transport: "TCP", Port: 5433}}, got)

	got, err = repo.UsedPortsByStream(s.ID, &a)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestStreamRepository_UsedPortsByStream_IgnoresRejectedAndCancelledApprovals(t *testing.T) {
	for _, status := range []string{"rejected", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			db := requirePostgres(t)
			projectID, _, teamID, userID := seedProject(t, db)
			tmplID := insertTemplate(t, db, projectID, userID, true)
			repo := repository.NewStreamRepository(db)
			s := newStream(t, repo, projectID, tmplID, "ignored-"+status, nil)

			a := insertL4Route(t, db, s.ID, teamID, userID, "tcp", 5432, "active")
			insertRouteApproval(t, db, projectID, userID, a, "update", status, 5433)

			got, err := repo.UsedPortsByStream(s.ID, nil)
			require.NoError(t, err)
			assert.Equal(t, []repository.PortUse{{Transport: "TCP", Port: 5432}}, got)
		})
	}
}

func TestStreamRepository_ClaimedPortsByTemplate(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, _, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	otherTmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)
	s1 := newStream(t, repo, projectID, tmplID, "s1", models.StreamPorts{{Transport: "TCP", Port: 80}, {Transport: "UDP", Port: 53}})
	newStream(t, repo, projectID, tmplID, "s2", models.StreamPorts{{Transport: "TCP", Port: 81}})
	newStream(t, repo, projectID, otherTmplID, "s3", models.StreamPorts{{Transport: "TCP", Port: 82}})

	got, err := repo.ClaimedPortsByTemplate(tmplID, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []repository.PortUse{
		{Transport: "TCP", Port: 80}, {Transport: "UDP", Port: 53}, {Transport: "TCP", Port: 81},
	}, got)

	got, err = repo.ClaimedPortsByTemplate(tmplID, &s1.ID)
	require.NoError(t, err)
	assert.Equal(t, []repository.PortUse{{Transport: "TCP", Port: 81}}, got)
}

func TestStreamRepository_PendingL4PortOwnersByStream(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)
	s := newStream(t, repo, projectID, tmplID, "pending-owners", nil)

	a := insertL4Route(t, db, s.ID, teamID, userID, "tcp", 5432, "active")
	insertRouteApproval(t, db, projectID, userID, a, "update", "pending", 5433)
	insertRouteApproval(t, db, projectID, userID, a, "update", "rejected", 5434)

	got, err := repo.PendingL4PortOwnersByStream(s.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "TCP", got[0].Transport)
	assert.Equal(t, 5433, got[0].Port)
	assert.True(t, got[0].Pending)
	assert.Equal(t, s.ID, got[0].StreamID)
	assert.Equal(t, "pending-owners", got[0].StreamName)
	assert.Equal(t, a, got[0].RouteID)
	assert.Equal(t, "r-"+a.String(), got[0].RouteName)

	// the row-based owners are not marked pending
	rows, err := repo.L4PortOwnersByStream(s.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.False(t, rows[0].Pending)
}

func TestStreamRepository_ListAll(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, _, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)
	s := newStream(t, repo, projectID, tmplID, "list-all", models.StreamPorts{{Transport: "TCP", Port: 80}})

	got, err := repo.ListAll()
	require.NoError(t, err)
	var found *models.Stream
	for i := range got {
		if got[i].ID == s.ID {
			found = &got[i]
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, models.StreamPorts{{Transport: "TCP", Port: 80}}, found.Ports)
}

// TestMigration000052_Backfill executes the backfill statement of the real
// migration file against seeded rows (inside a rolled-back transaction).
func TestMigration000052_Backfill(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)

	routed := newStream(t, repo, projectID, tmplID, "bf-routed", nil)
	insertL4Route(t, db, routed.ID, teamID, userID, "tcp", 5432, "active")
	insertL4Route(t, db, routed.ID, teamID, userID, "tcp", 9999, "rejected")

	withPending := newStream(t, repo, projectID, tmplID, "bf-pending", nil)
	r := insertL4Route(t, db, withPending.ID, teamID, userID, "tcp", 5432, "active")
	insertRouteApproval(t, db, projectID, userID, r, "update", "pending", 5433)
	insertRouteApproval(t, db, projectID, userID, r, "update", "cancelled", 5434)

	empty := newStream(t, repo, projectID, tmplID, "bf-empty", nil)

	raw, err := os.ReadFile("../../migrations/000052_stream_ports.up.sql")
	require.NoError(t, err)
	sqlText := string(raw)
	idx := strings.Index(sqlText, "UPDATE streams")
	require.GreaterOrEqual(t, idx, 0, "backfill statement not found in migration")
	backfill := sqlText[idx:]

	tx := db.Begin()
	require.NoError(t, tx.Error)
	defer tx.Rollback()
	require.NoError(t, tx.Exec(backfill).Error)

	portsOf := func(id uuid.UUID) models.StreamPorts {
		var s models.Stream
		require.NoError(t, tx.First(&s, "id = ?", id).Error)
		return s.Ports
	}
	assert.Equal(t, models.StreamPorts{{Transport: "TCP", Port: 5432}}, portsOf(routed.ID))
	assert.Equal(t, models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "TCP", Port: 5433}}, portsOf(withPending.ID))
	assert.Equal(t, models.StreamPorts{}, portsOf(empty.ID))
}
