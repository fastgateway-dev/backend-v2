package repository_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// portUsesOf projects owner rows onto the (transport, port) identities the
// collision queries return, so the two can be compared directly.
func portUsesOf(owners []repository.L4PortOwner) []repository.PortUse {
	out := make([]repository.PortUse, 0, len(owners))
	for _, o := range owners {
		out = append(out, repository.PortUse{Transport: o.Transport, Port: o.Port})
	}
	return out
}

func TestStreamRepository_L4PortOwners_MatchUsedPortQueries(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, teamID, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	otherTmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)
	mk := func(name string, tmpl uuid.UUID) *models.Stream {
		s := &models.Stream{ProjectID: projectID, Name: name, Namespace: "fastgateway-system", GatewayTemplateID: tmpl}
		require.NoError(t, repo.Create(s))
		return s
	}
	a, b, c := mk("owner-a", tmplID), mk("owner-b", tmplID), mk("owner-c", otherTmplID)

	pg := insertL4Route(t, db, a.ID, teamID, userID, "tcp", 5432, "active")
	dns := insertL4Route(t, db, a.ID, teamID, userID, "udp", 53, "pending_create")
	insertL4Route(t, db, a.ID, teamID, userID, "tcp", 9999, "rejected")
	redis := insertL4Route(t, db, b.ID, teamID, userID, "tcp", 6379, "pending_update")
	insertL4Route(t, db, c.ID, teamID, userID, "tcp", 7000, "active")

	// By stream: same rows as UsedPortsByStream, with owners filled in.
	byStream, err := repo.L4PortOwnersByStream(a.ID)
	require.NoError(t, err)
	used, err := repo.UsedPortsByStream(a.ID, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, used, portUsesOf(byStream), "owner query must match UsedPortsByStream")
	assert.Equal(t, []repository.L4PortOwner{
		{Transport: "UDP", Port: 53, StreamID: a.ID, StreamName: "owner-a", RouteID: dns, RouteName: "r-" + dns.String(), Status: "pending_create"},
		{Transport: "TCP", Port: 5432, StreamID: a.ID, StreamName: "owner-a", RouteID: pg, RouteName: "r-" + pg.String(), Status: "active"},
	}, byStream, "rejected routes hold no port; rows are ordered by port")

	// By template: every non-rejected L4 route of the template's streams.
	byTmpl, err := repo.L4PortOwnersByTemplate(tmplID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []repository.PortUse{{Transport: "UDP", Port: 53}, {Transport: "TCP", Port: 5432}, {Transport: "TCP", Port: 6379}}, portUsesOf(byTmpl))
	require.Len(t, byTmpl, 3)
	assert.Equal(t, redis, byTmpl[2].RouteID)
	assert.Equal(t, "owner-b", byTmpl[2].StreamName)

	// By stream IDs: one query for many streams.
	byIDs, err := repo.L4PortOwnersByStreamIDs([]uuid.UUID{a.ID, c.ID})
	require.NoError(t, err)
	assert.ElementsMatch(t, []repository.PortUse{{Transport: "UDP", Port: 53}, {Transport: "TCP", Port: 5432}, {Transport: "TCP", Port: 7000}}, portUsesOf(byIDs))

	empty, err := repo.L4PortOwnersByStreamIDs(nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.NotNil(t, empty)
}

func TestStreamRepository_ListByGatewayTemplateID(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, _, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	otherTmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)
	for _, s := range []*models.Stream{
		{ProjectID: projectID, Name: "zeta", Namespace: "fastgateway-system", GatewayTemplateID: tmplID},
		{ProjectID: projectID, Name: "alpha", Namespace: "fastgateway-system", GatewayTemplateID: tmplID},
		{ProjectID: projectID, Name: "other", Namespace: "fastgateway-system", GatewayTemplateID: otherTmplID},
	} {
		require.NoError(t, repo.Create(s))
	}

	got, err := repo.ListByGatewayTemplateID(tmplID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "alpha", got[0].Name)
	assert.Equal(t, "zeta", got[1].Name)
}

func TestDomainRepository_PortOwnersByTemplate_MatchesUsedPortsByTemplate(t *testing.T) {
	db := requirePostgres(t)
	projectID, domainID, _, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, false)
	require.NoError(t, db.Exec(`UPDATE domain_templates SET listeners = ? WHERE id = ?`,
		`[{"name":"http","protocol":"HTTP","port":8080},{"name":"https","protocol":"HTTPS","port":8443,"tlsMode":"Terminate"},{"name":"unbound","protocol":"HTTP","port":9090}]`, tmplID).Error)
	// "gone" is a stale binding to a listener the template no longer
	// declares: both queries must ignore it.
	require.NoError(t, db.Exec(`UPDATE domains SET domain_template_id = ?, bound_listeners = ? WHERE id = ?`,
		tmplID, `["http","https","gone"]`, domainID).Error)
	var name string
	require.NoError(t, db.Raw(`SELECT name FROM domains WHERE id = ?`, domainID).Scan(&name).Error)

	repo := repository.NewDomainRepository(db)
	owners, err := repo.PortOwnersByTemplate(tmplID)
	require.NoError(t, err)
	used, err := repo.UsedPortsByTemplate(tmplID)
	require.NoError(t, err)

	got := make([]repository.PortUse, 0, len(owners))
	for _, o := range owners {
		got = append(got, repository.PortUse{Transport: o.Transport, Port: o.Port})
		assert.Equal(t, domainID, o.DomainID)
		assert.Equal(t, name, o.DomainName)
	}
	assert.ElementsMatch(t, used, got, "owner query must match UsedPortsByTemplate")
	assert.ElementsMatch(t, []repository.PortUse{{Transport: "TCP", Port: 8080}, {Transport: "TCP", Port: 8443}}, got,
		"a stale binding holds no port")

	missing, err := repo.PortOwnersByTemplate(uuid.New())
	require.NoError(t, err)
	assert.Empty(t, missing)
}
