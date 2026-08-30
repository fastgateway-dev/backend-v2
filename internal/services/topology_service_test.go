package services_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestProjectTopologyResponse_JSONShape(t *testing.T) {
	resp := services.ProjectTopologyResponse{
		Domains: []services.ProjectTopologyDomain{},
		Clients: []services.ProjectTopologyClient{},
		IPs:     []services.TopologyIPRow{},
	}
	b, err := json.Marshal(resp)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"domains":[],"clients":[],"ips":[]}`, string(b))
}

func TestDomainTopologyResponse_JSONShape(t *testing.T) {
	resp := services.DomainTopologyResponse{}
	b, err := json.Marshal(resp)
	assert.NoError(t, err)
	var m map[string]any
	assert.NoError(t, json.Unmarshal(b, &m))
	for _, k := range []string{"domain", "gateway", "routes", "backends", "clients", "attachments"} {
		_, ok := m[k]
		assert.True(t, ok, "missing key %s", k)
	}
}

func TestSecurityFeatureFlags_KeysMatchSpec(t *testing.T) {
	f := services.SecurityFeatureFlags{}
	b, _ := json.Marshal(f)
	for _, k := range []string{"ipAllowlist", "mtls", "apiKey", "jwt", "basicAuth", "headerAuth", "rateLimit", "extAuth", "oidc", "waf"} {
		assert.Contains(t, string(b), `"`+k+`"`)
	}
}

// requireTopologyDB skips the test unless INTEGRATION_DB_URL points to a
// reachable Postgres instance.
func requireTopologyDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("INTEGRATION_DB_URL")
	if dsn == "" {
		t.Skip("INTEGRATION_DB_URL not set; skipping topology integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	return db
}

// seedTopologyGeneralDomain inserts a minimal user/team/project/domain and two
// general-mode routes that share a single kubernetes backend, then registers
// cleanup. Returns (projectID, domainID).
func seedTopologyGeneralDomain(t *testing.T, db *gorm.DB) (projectID, domainID uuid.UUID) {
	t.Helper()
	userID := uuid.New()
	teamID := uuid.New()
	projectID = uuid.New()
	domainID = uuid.New()
	suffix := projectID.String()

	require.NoError(t, db.Exec(`INSERT INTO users (id, username, email, password_hash, role, is_active, created_at, updated_at)
		VALUES (?, ?, ?, '', 'owner', true, NOW(), NOW())`,
		userID, "u-"+suffix, "u-"+suffix+"@example.com").Error)
	require.NoError(t, db.Exec(`INSERT INTO teams (id, name, created_at, updated_at) VALUES (?, ?, NOW(), NOW())`,
		teamID, "team-"+suffix).Error)
	require.NoError(t, db.Exec(`INSERT INTO projects (id, name, k8s_api_url, k8s_token_encrypted, created_by, created_at, updated_at)
		VALUES (?, ?, '', '', ?, NOW(), NOW())`, projectID, "p-"+suffix, userID).Error)
	require.NoError(t, db.Exec(`INSERT INTO domains (id, project_id, name, hostname, http_port, https_port, namespace, k8s_gateway_class_name, status, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, 80, 443, 'fastgateway-system', 'envoy', 'active', ?, NOW(), NOW())`,
		domainID, projectID, "d-"+suffix, suffix+".example.com", userID).Error)
	require.NoError(t, db.Exec(`INSERT INTO routes (id, domain_id, team_id, name, protocol, security_mode, status, config, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'http', 'general', 'active', '{"matches":[{"path":{"type":"Prefix","value":"/a"}}],"backends":[{"type":"kubernetes","service":"svc","namespace":"ns","port":8080}]}'::jsonb, ?, NOW(), NOW())`,
		uuid.New(), domainID, teamID, "r-a-"+suffix, userID).Error)
	require.NoError(t, db.Exec(`INSERT INTO routes (id, domain_id, team_id, name, protocol, security_mode, status, config, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'http', 'general', 'active', '{"matches":[{"path":{"type":"Prefix","value":"/b"}}],"backends":[{"type":"kubernetes","service":"svc","namespace":"ns","port":8080}]}'::jsonb, ?, NOW(), NOW())`,
		uuid.New(), domainID, teamID, "r-b-"+suffix, userID).Error)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM routes WHERE domain_id = ?`, domainID).Error
		_ = db.Exec(`DELETE FROM domains WHERE id = ?`, domainID).Error
		_ = db.Exec(`DELETE FROM projects WHERE id = ?`, projectID).Error
		_ = db.Exec(`DELETE FROM teams WHERE id = ?`, teamID).Error
		_ = db.Exec(`DELETE FROM users WHERE id = ?`, userID).Error
	})
	return projectID, domainID
}

func TestTopologyService_GetDomainTopology_GeneralMode_BackendDedup(t *testing.T) {
	db := requireTopologyDB(t)
	projectID, domainID := seedTopologyGeneralDomain(t, db)

	svc := services.NewTopologyService(
		repository.NewDomainRepository(db),
		repository.NewRouteRepository(db),
		repository.NewClientAttachmentRepository(db),
		repository.NewClientRepository(db),
		repository.NewClientIPRepository(db),
		repository.NewSecurityPolicyRepository(db),
		repository.NewWafPolicyRepository(db),
		repository.NewBackendTrafficPolicyRepository(db),
		repository.NewTeamRepository(db),
		repository.NewDomainTemplateRepository(db),
	)

	resp, err := svc.GetDomainTopology(context.Background(), projectID, domainID)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, domainID, resp.Domain.ID)
	assert.Equal(t, "general", resp.Domain.SecurityMode)
	assert.Len(t, resp.Routes, 2)
	assert.Len(t, resp.Backends, 1, "shared backend should be deduped")
	assert.Equal(t, 2, resp.Backends[0].HitCount)
	assert.Empty(t, resp.Clients, "general mode → no clients")
	assert.Empty(t, resp.Attachments, "general mode → no attachments")
}

func seedTopologyClientDomain(t *testing.T, db *gorm.DB) (projectID, domainID, routeID, clientID uuid.UUID) {
	t.Helper()
	userID := uuid.New()
	teamID := uuid.New()
	projectID = uuid.New()
	domainID = uuid.New()
	routeID = uuid.New()
	clientID = uuid.New()
	suffix := projectID.String()

	require.NoError(t, db.Exec(`INSERT INTO users (id, username, email, password_hash, role, is_active, created_at, updated_at) VALUES (?, ?, ?, '', 'owner', true, NOW(), NOW())`, userID, "u-"+suffix, "u-"+suffix+"@example.com").Error)
	require.NoError(t, db.Exec(`INSERT INTO teams (id, name, created_at, updated_at) VALUES (?, ?, NOW(), NOW())`, teamID, "team-"+suffix).Error)
	require.NoError(t, db.Exec(`INSERT INTO projects (id, name, k8s_api_url, k8s_token_encrypted, created_by, created_at, updated_at) VALUES (?, ?, '', '', ?, NOW(), NOW())`, projectID, "p-"+suffix, userID).Error)
	require.NoError(t, db.Exec(`INSERT INTO domains (id, project_id, name, hostname, http_port, https_port, namespace, k8s_gateway_class_name, status, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, 80, 443, 'fg', 'envoy', 'active', ?, NOW(), NOW())`, domainID, projectID, "d-"+suffix, suffix+".example.com", userID).Error)
	require.NoError(t, db.Exec(`INSERT INTO routes (id, domain_id, team_id, name, protocol, security_mode, status, config, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, 'http', 'client', 'active', '{"matches":[{"path":{"type":"Prefix","value":"/x"}}],"backends":[{"type":"kubernetes","service":"svc","namespace":"ns","port":8080}]}'::jsonb, ?, NOW(), NOW())`, routeID, domainID, teamID, "r-"+suffix, userID).Error)
	require.NoError(t, db.Exec(`INSERT INTO clients (id, team_id, name, api_key_enabled, jwt_enabled, mtls_enabled, created_by, created_at, updated_at) VALUES (?, ?, ?, true, false, false, ?, NOW(), NOW())`, clientID, teamID, "c-"+suffix, userID).Error)
	require.NoError(t, db.Exec(`INSERT INTO client_route_attachments (id, client_id, route_id, enable_ip_allowlist, enable_api_key, enable_jwt, enable_mtls, status, created_by, created_at, updated_at) VALUES (?, ?, ?, true, true, false, false, 'active', ?, NOW(), NOW())`, uuid.New(), clientID, routeID, userID).Error)

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM client_route_attachments WHERE route_id = ?`, routeID).Error
		_ = db.Exec(`DELETE FROM clients WHERE id = ?`, clientID).Error
		_ = db.Exec(`DELETE FROM routes WHERE domain_id = ?`, domainID).Error
		_ = db.Exec(`DELETE FROM domains WHERE id = ?`, domainID).Error
		_ = db.Exec(`DELETE FROM projects WHERE id = ?`, projectID).Error
		_ = db.Exec(`DELETE FROM teams WHERE id = ?`, teamID).Error
		_ = db.Exec(`DELETE FROM users WHERE id = ?`, userID).Error
	})
	return
}

func TestTopologyService_GetDomainTopology_ClientMode(t *testing.T) {
	db := requireTopologyDB(t)
	projectID, domainID, _, _ := seedTopologyClientDomain(t, db)

	svc := newTestTopologySvcFromDB(t, db)
	resp, err := svc.GetDomainTopology(context.Background(), projectID, domainID)
	require.NoError(t, err)

	assert.Equal(t, "client", resp.Domain.SecurityMode)
	assert.Len(t, resp.Clients, 1)
	assert.True(t, resp.Clients[0].Capabilities.APIKey)
	assert.Len(t, resp.Attachments, 1)
	assert.True(t, resp.Attachments[0].Enforced.IPAllowlist)
	assert.True(t, resp.Attachments[0].Enforced.APIKey)
	assert.False(t, resp.Attachments[0].Enforced.MTLS)
	assert.Equal(t, services.TopologyStatusDeployed, resp.Attachments[0].Status)
}

func TestTopologyService_GetProjectTopology_DomainCards(t *testing.T) {
	db := requireTopologyDB(t)
	projectID, _ := seedTopologyGeneralDomain(t, db)

	svc := newTestTopologySvcFromDB(t, db)
	resp, err := svc.GetProjectTopology(context.Background(), projectID)
	require.NoError(t, err)
	require.Len(t, resp.Domains, 1)
	assert.Equal(t, 2, resp.Domains[0].Counts.Routes)
	assert.Equal(t, 0, resp.Domains[0].Counts.ClientsAttached)
	assert.Equal(t, "general", resp.Domains[0].SecurityMode)
}

func TestTopologyService_GetProjectTopology_ClientPerDomain(t *testing.T) {
	db := requireTopologyDB(t)
	projectID, _, _, clientID := seedTopologyClientDomain(t, db)
	svc := newTestTopologySvcFromDB(t, db)
	resp, err := svc.GetProjectTopology(context.Background(), projectID)
	require.NoError(t, err)
	require.Len(t, resp.Clients, 1)
	assert.Equal(t, clientID, resp.Clients[0].ID)
	require.Len(t, resp.Clients[0].PerDomain, 1)
	for _, pd := range resp.Clients[0].PerDomain {
		assert.Equal(t, 1, pd.RouteCount)
		assert.Equal(t, services.TopologyStatusDeployed, pd.AggregateStatus)
	}
}

func TestTopologyService_GetProjectTopology_IPRows_RouteAndClientSources(t *testing.T) {
	db := requireTopologyDB(t)
	projectID, _, routeID, clientID := seedTopologyClientDomain(t, db)

	require.NoError(t, db.Exec(`INSERT INTO client_ip_addresses (id, client_id, cidr, created_by, created_at) SELECT ?, ?, '10.0.0.0/24', created_by, NOW() FROM clients WHERE id = ?`, uuid.New(), clientID, clientID).Error)
	require.NoError(t, db.Exec(`INSERT INTO security_policies (id, route_id, project_id, config, created_at, updated_at) VALUES (?, ?, ?, ?::jsonb, NOW(), NOW())`,
		uuid.New(), routeID, projectID, `{"authorization":{"defaultAction":"Deny","rules":[{"action":"Allow","principal":{"clientCIDRs":["1.2.3.4"]}}]}}`).Error)

	svc := newTestTopologySvcFromDB(t, db)
	resp, err := svc.GetProjectTopology(context.Background(), projectID)
	require.NoError(t, err)

	var sawRoute, sawClient bool
	for _, ip := range resp.IPs {
		if ip.Source == "route" && ip.CIDR == "1.2.3.4/32" {
			sawRoute = true
		}
		if ip.Source == "client" && ip.CIDR == "10.0.0.0/24" {
			sawClient = true
			assert.Contains(t, ip.Reach.RouteIDs, routeID)
		}
	}
	assert.True(t, sawRoute, "expected route-source IP row")
	assert.True(t, sawClient, "expected client-source IP row")
}

func newTestTopologySvcFromDB(t *testing.T, db *gorm.DB) *services.TopologyService {
	t.Helper()
	return services.NewTopologyService(
		repository.NewDomainRepository(db),
		repository.NewRouteRepository(db),
		repository.NewClientAttachmentRepository(db),
		repository.NewClientRepository(db),
		repository.NewClientIPRepository(db),
		repository.NewSecurityPolicyRepository(db),
		repository.NewWafPolicyRepository(db),
		repository.NewBackendTrafficPolicyRepository(db),
		repository.NewTeamRepository(db),
		repository.NewDomainTemplateRepository(db),
	)
}
