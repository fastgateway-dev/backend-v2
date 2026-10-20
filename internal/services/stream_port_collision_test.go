package services_test

import (
	"encoding/json"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// collisionEnv is a seeded project (user + team + project) plus a real,
// Postgres-backed StreamService wired with the port sources.
type collisionEnv struct {
	db        *gorm.DB
	projectID uuid.UUID
	teamID    uuid.UUID
	userID    uuid.UUID
	svc       *services.StreamService
}

func newCollisionEnv(t *testing.T) *collisionEnv {
	t.Helper()
	// Silent logger: the not-found test would otherwise print gorm's
	// "record not found" trace.
	db := requireTopologyDB(t).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	e := &collisionEnv{db: db, projectID: uuid.New(), teamID: uuid.New(), userID: uuid.New()}
	suffix := e.projectID.String()

	require.NoError(t, db.Exec(`INSERT INTO users (id, username, email, password_hash, role, is_active, created_at, updated_at)
		VALUES (?, ?, ?, '', 'owner', true, NOW(), NOW())`,
		e.userID, "u-"+suffix, "u-"+suffix+"@example.com").Error)
	require.NoError(t, db.Exec(`INSERT INTO teams (id, name, created_at, updated_at) VALUES (?, ?, NOW(), NOW())`,
		e.teamID, "team-"+suffix).Error)
	require.NoError(t, db.Exec(`INSERT INTO projects (id, name, k8s_api_url, k8s_token_encrypted, created_by, created_at, updated_at)
		VALUES (?, ?, '', '', ?, NOW(), NOW())`, e.projectID, "p-"+suffix, e.userID).Error)

	t.Cleanup(func() {
		// routes cascade from streams; domains and templates are removed explicitly.
		_ = db.Exec(`DELETE FROM routes WHERE team_id = ?`, e.teamID).Error
		_ = db.Exec(`DELETE FROM streams WHERE project_id = ?`, e.projectID).Error
		_ = db.Exec(`DELETE FROM domains WHERE project_id = ?`, e.projectID).Error
		_ = db.Exec(`DELETE FROM domain_templates WHERE project_id = ?`, e.projectID).Error
		_ = db.Exec(`DELETE FROM projects WHERE id = ?`, e.projectID).Error
		_ = db.Exec(`DELETE FROM teams WHERE id = ?`, e.teamID).Error
		_ = db.Exec(`DELETE FROM users WHERE id = ?`, e.userID).Error
	})

	applier := mocks.NewMockGatewayApplier(t)
	applier.EXPECT().CreateGateway(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	streamRepo := repository.NewStreamRepository(db)
	e.svc = services.NewStreamService(streamRepo, repository.NewDomainTemplateRepository(db),
		repository.NewRouteRepository(db), applier, allowAllNamespaces{})
	e.svc.SetPortSources(streamRepo, repository.NewDomainRepository(db))
	return e
}

// template inserts a Gateway Template with HTTP:80, HTTPS:443 and a TCP/UDP
// range, plus the given MergeGateways.
func (e *collisionEnv) template(t *testing.T, merge bool) uuid.UUID {
	t.Helper()
	tmpl := &models.DomainTemplate{
		ProjectID: e.projectID, Name: "tmpl-" + uuid.NewString(), CreatedBy: e.userID,
		MergeGateways: merge,
		Listeners: models.Listeners{
			{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
			{Name: "https", Protocol: models.ListenerHTTPS, Port: 443},
			{Name: "stream", Protocol: models.ListenerTCP, PortRangeMin: 1024, PortRangeMax: 65535},
		},
	}
	require.NoError(t, e.db.Create(tmpl).Error)
	return tmpl.ID
}

func (e *collisionEnv) stream(t *testing.T, tmplID uuid.UUID) uuid.UUID {
	t.Helper()
	name := "s-" + uuid.NewString()[:8]
	s := &models.Stream{ProjectID: e.projectID, Name: name, Namespace: "fastgateway-system",
		GatewayTemplateID: tmplID, K8sGatewayName: "str-" + name, K8sGatewayClass: "public-lb"}
	require.NoError(t, repository.NewStreamRepository(e.db).Create(s))
	return s.ID
}

// domain inserts a domain bound to the template's http and https listeners
// (ports 80 and 443 on the template() fixture).
func (e *collisionEnv) domain(t *testing.T, tmplID uuid.UUID) {
	t.Helper()
	id := uuid.New()
	require.NoError(t, e.db.Exec(`INSERT INTO domains (id, project_id, domain_template_id, name, hostname, bound_listeners, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, '["http","https"]'::jsonb, ?, NOW(), NOW())`,
		id, e.projectID, tmplID, "d-"+id.String(), id.String()+".test.example.com", e.userID).Error)
}

// l4Route inserts an L4 route of the given protocol ("tcp"/"udp") on a stream.
func (e *collisionEnv) l4Route(t *testing.T, streamID uuid.UUID, protocol string, port int, status string) uuid.UUID {
	t.Helper()
	cfg, err := json.Marshal(map[string]any{"listenerPort": port})
	require.NoError(t, err)
	id := uuid.New()
	require.NoError(t, e.db.Exec(`INSERT INTO routes (id, stream_id, team_id, name, protocol, security_mode, status, config, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'general', ?, ?::jsonb, ?, NOW(), NOW())`,
		id, streamID, e.teamID, "r-"+id.String(), protocol, status, string(cfg), e.userID).Error)
	return id
}

func TestCheckPortCollision_SamePortSameProto_WithinStream(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.template(t, false))
	e.l4Route(t, streamID, "tcp", 5432, "active")

	err := e.svc.CheckPortCollision(streamID, "TCP", 5432, nil)
	require.ErrorIs(t, err, services.ErrPortCollision)
	assert.Contains(t, err.Error(), "TCP/5432")
}

func TestCheckPortCollision_TCPvsUDP_SameNumber_OK(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.template(t, false))
	e.l4Route(t, streamID, "tcp", 53, "active")

	assert.NoError(t, e.svc.CheckPortCollision(streamID, "UDP", 53, nil))
	// ...but another TCP/53 still collides.
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 53, nil), services.ErrPortCollision)
}

func TestCheckPortCollision_Merged_HitsDomainHTTPSPort(t *testing.T) {
	e := newCollisionEnv(t)
	tmplID := e.template(t, true)
	e.domain(t, tmplID)
	streamID := e.stream(t, tmplID)

	// HTTPS 443 and HTTP 80 are TCP-transport listeners on the merged class.
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 443, nil), services.ErrPortCollision)
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 80, nil), services.ErrPortCollision)
	// UDP/443 is a different transport; an unused TCP port is free.
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "UDP", 443, nil))
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 5432, nil))
}

func TestCheckPortCollision_NotMerged_OtherStreamSamePort_OK(t *testing.T) {
	e := newCollisionEnv(t)
	tmplID := e.template(t, false)
	streamA := e.stream(t, tmplID)
	streamB := e.stream(t, tmplID)
	e.l4Route(t, streamA, "tcp", 5432, "active")
	// Domains on an unmerged template have their own Gateways: no collision.
	e.domain(t, tmplID)

	assert.NoError(t, e.svc.CheckPortCollision(streamB, "TCP", 5432, nil))
	assert.NoError(t, e.svc.CheckPortCollision(streamA, "TCP", 443, nil))
}

func TestCheckPortCollision_Merged_OtherStreamOnSameTemplate(t *testing.T) {
	e := newCollisionEnv(t)
	tmplID := e.template(t, true)
	streamA := e.stream(t, tmplID)
	streamB := e.stream(t, tmplID)
	e.l4Route(t, streamA, "udp", 5353, "active")

	assert.ErrorIs(t, e.svc.CheckPortCollision(streamB, "UDP", 5353, nil), services.ErrPortCollision)
	assert.NoError(t, e.svc.CheckPortCollision(streamB, "TCP", 5353, nil))

	// A different template (even merged) is a different set of Gateways.
	otherStream := e.stream(t, e.template(t, true))
	assert.NoError(t, e.svc.CheckPortCollision(otherStream, "UDP", 5353, nil))
}

func TestCheckPortCollision_ExcludeRouteID(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.template(t, false))
	routeID := e.l4Route(t, streamID, "tcp", 5432, "active")

	// Updating the route that owns the port must not collide with itself.
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 5432, &routeID))
	// A different route id does not get the exemption.
	other := uuid.New()
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 5432, &other), services.ErrPortCollision)
}

func TestCheckPortCollision_RejectedRouteDoesNotHoldPort(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.template(t, false))
	e.l4Route(t, streamID, "tcp", 5432, "rejected")
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 5432, nil))

	e.l4Route(t, streamID, "tcp", 6000, "pending_create")
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 6000, nil), services.ErrPortCollision)
}

func TestCheckPortCollision_UnknownStream(t *testing.T) {
	e := newCollisionEnv(t)
	assert.ErrorIs(t, e.svc.CheckPortCollision(uuid.New(), "TCP", 80, nil), services.ErrStreamNotFound)
}

func TestCheckPortCollision_InvalidTransport(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.template(t, false))
	assert.Error(t, e.svc.CheckPortCollision(streamID, "HTTP", 80, nil))
}

// templateWithDomain inserts a Gateway Template with explicit MergeGateways.
// Hostname-routed (HTTP/HTTPS) listeners are declared only when enableDomain is
// true; the TCP/UDP stream range is always present so the template stays valid.
func (e *collisionEnv) templateWithDomain(t *testing.T, merge, enableDomain bool, httpPort, httpsPort int) uuid.UUID {
	t.Helper()
	ls := models.Listeners{{Name: "stream", Protocol: models.ListenerTCP, PortRangeMin: 1024, PortRangeMax: 65535}}
	if enableDomain {
		ls = append(models.Listeners{
			{Name: "http", Protocol: models.ListenerHTTP, Port: httpPort},
			{Name: "https", Protocol: models.ListenerHTTPS, Port: httpsPort},
		}, ls...)
	}
	tmpl := &models.DomainTemplate{
		ProjectID: e.projectID, Name: "tmpl-" + uuid.NewString(), CreatedBy: e.userID,
		MergeGateways: merge, Listeners: ls,
	}
	require.NoError(t, e.db.Create(tmpl).Error)
	return tmpl.ID
}

var defaultReserved = services.DefaultReservedPorts

func TestValidateListenerPort_RejectsEnvoyInternal(t *testing.T) {
	assert.ErrorIs(t, services.ValidateListenerPort(19000, defaultReserved), services.ErrReservedPort)
	assert.ErrorIs(t, services.ValidateListenerPort(19001, defaultReserved), services.ErrReservedPort)
}

func TestValidateListenerPort_PlaceholderPortIsNormal(t *testing.T) {
	assert.NoError(t, services.ValidateListenerPort(60000, defaultReserved))
}

func TestValidateListenerPort_RejectsOutOfRange(t *testing.T) {
	assert.ErrorIs(t, services.ValidateListenerPort(0, defaultReserved), services.ErrInvalidListenerPort)
	assert.ErrorIs(t, services.ValidateListenerPort(-1, defaultReserved), services.ErrInvalidListenerPort)
	assert.ErrorIs(t, services.ValidateListenerPort(70000, defaultReserved), services.ErrInvalidListenerPort)
}

func TestValidateListenerPort_RejectsDomainDefaults(t *testing.T) {
	reserved := services.ReservedPorts{DomainDefaults: []int{80, 443}}
	assert.ErrorIs(t, services.ValidateListenerPort(443, reserved), services.ErrReservedPort)
	assert.ErrorIs(t, services.ValidateListenerPort(80, reserved), services.ErrReservedPort)
}

func TestValidateListenerPort_AcceptsOrdinaryPorts(t *testing.T) {
	assert.NoError(t, services.ValidateListenerPort(5432, defaultReserved))
	assert.NoError(t, services.ValidateListenerPort(1, defaultReserved))
	assert.NoError(t, services.ValidateListenerPort(65535, defaultReserved))
	// 80/443 are free unless the caller reserves them (merged+domain-enabled).
	assert.NoError(t, services.ValidateListenerPort(443, defaultReserved))
}

func TestCheckPortCollision_MergedDomainEnabled_Reserves80And443(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.templateWithDomain(t, true, true, 80, 443))

	// No Domain exists yet, but the template's default ports are reserved.
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 443, nil), services.ErrReservedPort)
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 80, nil), services.ErrReservedPort)
	// UDP is a different transport; ordinary TCP ports are free.
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "UDP", 443, nil))
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 5432, nil))
}

func TestCheckPortCollision_MergedDomainEnabled_ReservesCustomTemplatePorts(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.templateWithDomain(t, true, true, 8080, 8443))

	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 8443, nil), services.ErrReservedPort)
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 8080, nil), services.ErrReservedPort)
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 443, nil))
}

func TestCheckPortCollision_MergedDomainDisabled_Allows443(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.templateWithDomain(t, true, false, 80, 443))
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 443, nil))
}

func TestCheckPortCollision_NotMerged_Allows443(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.templateWithDomain(t, false, true, 80, 443))
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 443, nil))
}

func TestCheckPortCollision_RejectsStaticReserved(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.template(t, false))
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 19001, nil), services.ErrReservedPort)
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "UDP", 60000, nil))
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 0, nil), services.ErrInvalidListenerPort)
}

// ---------------------------------------------------------------------------
// Claim validation (mock-based, no DB)
// ---------------------------------------------------------------------------

// claimsFixture is a StreamService wired with mock port sources.
type claimsFixture struct {
	svc     *services.StreamService
	store   *mocks.MockStreamPortStore
	domains *mocks.MockDomainPortReader
}

func newClaimsFixture(t *testing.T) *claimsFixture {
	t.Helper()
	f := &claimsFixture{
		store:   mocks.NewMockStreamPortStore(t),
		domains: mocks.NewMockDomainPortReader(t),
	}
	f.svc = services.NewStreamService(mocks.NewMockStreamStore(t), &fakeTemplateReader{}, &fakeRouteCounter{},
		mocks.NewMockGatewayApplier(t), allowAllNamespaces{})
	f.svc.SetPortSources(f.store, f.domains)
	return f
}

// claimsTemplate is HTTP:80 + HTTPS:443 + a TCP/UDP range of 1-65535.
func claimsTemplate(merge bool) *models.DomainTemplate {
	return &models.DomainTemplate{
		ID: uuid.New(), Name: "tpl", MergeGateways: merge,
		Listeners: models.Listeners{
			{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
			{Name: "https", Protocol: models.ListenerHTTPS, Port: 443},
			{Name: "stream", Protocol: models.ListenerTCP, PortRangeMin: 1, PortRangeMax: 65535},
		},
	}
}

func TestValidateStreamPorts_Rules(t *testing.T) {
	narrow := claimsTemplate(false)
	narrow.Listeners[2].PortRangeMin, narrow.Listeners[2].PortRangeMax = 1024, 2000
	for _, tc := range []struct {
		name  string
		tmpl  *models.DomainTemplate
		ports models.StreamPorts
		is    error
	}{
		{"empty", claimsTemplate(false), models.StreamPorts{}, services.ErrNoStreamPorts},
		{"nil", claimsTemplate(false), nil, services.ErrNoStreamPorts},
		{"bad transport", claimsTemplate(false), models.StreamPorts{{Transport: "SCTP", Port: 80}}, services.ErrInvalidStreamPort},
		{"duplicate after normalizing", claimsTemplate(false), models.StreamPorts{{Transport: "TCP", Port: 80}, {Transport: "tcp", Port: 80}}, services.ErrInvalidStreamPort},
		{"port zero", claimsTemplate(false), models.StreamPorts{{Transport: "TCP", Port: 0}}, services.ErrInvalidListenerPort},
		{"port above 65535", claimsTemplate(false), models.StreamPorts{{Transport: "UDP", Port: 70000}}, services.ErrInvalidListenerPort},
		{"envoy internal", claimsTemplate(false), models.StreamPorts{{Transport: "TCP", Port: 19000}}, services.ErrReservedPort},
		{"envoy internal UDP", claimsTemplate(false), models.StreamPorts{{Transport: "UDP", Port: 19001}}, services.ErrReservedPort},
		{"outside template range", narrow, models.StreamPorts{{Transport: "TCP", Port: 5432}}, services.ErrPortOutOfRange},
		{"template without range", &models.DomainTemplate{Name: "domains-only", Listeners: models.Listeners{{Name: "http", Protocol: models.ListenerHTTP, Port: 80}}},
			models.StreamPorts{{Transport: "TCP", Port: 5432}}, services.ErrPortOutOfRange},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newClaimsFixture(t) // dedicated templates read no store: strict mocks catch any call
			got, err := services.ValidateStreamPortsForTest(f.svc, tc.tmpl, nil, tc.ports)
			require.ErrorIs(t, err, tc.is)
			assert.Nil(t, got)
		})
	}

	t.Run("60000 is a normal port", func(t *testing.T) {
		f := newClaimsFixture(t)
		got, err := services.ValidateStreamPortsForTest(f.svc, claimsTemplate(false), nil, models.StreamPorts{{Transport: "UDP", Port: 60000}})
		require.NoError(t, err)
		assert.Equal(t, models.StreamPorts{{Transport: "UDP", Port: 60000}}, got)
	})
}

func TestValidateStreamPorts_SharedCollisions(t *testing.T) {
	tmpl := claimsTemplate(true)
	selfID := uuid.New()
	one := func(transport string, port int) models.StreamPorts {
		return models.StreamPorts{{Transport: transport, Port: port}}
	}
	for _, tc := range []struct {
		name    string
		claimed []services.PortUse
		domain  []services.PortUse
		ports   models.StreamPorts
		is      error
	}{
		{"another stream's claim", []services.PortUse{{Transport: "TCP", Port: 5432}}, nil, one("TCP", 5432), services.ErrPortCollision},
		{"domain port", nil, []services.PortUse{{Transport: "TCP", Port: 8443}}, one("TCP", 8443), services.ErrPortCollision},
		{"template hostname-routed TCP port", nil, nil, one("TCP", 443), services.ErrReservedPort},
		{"UDP on the HTTPS number is fine", nil, nil, one("UDP", 443), nil},
		{"same number other transport is fine", []services.PortUse{{Transport: "TCP", Port: 53}}, nil, one("UDP", 53), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newClaimsFixture(t)
			f.store.EXPECT().ClaimedPortsByTemplate(tmpl.ID, &selfID).Return(tc.claimed, nil).Maybe()
			f.domains.EXPECT().UsedPortsByTemplate(tmpl.ID).Return(tc.domain, nil).Maybe()
			_, err := services.ValidateStreamPortsForTest(f.svc, tmpl, &selfID, tc.ports)
			if tc.is == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.is)
		})
	}

	t.Run("collision message names the port", func(t *testing.T) {
		f := newClaimsFixture(t)
		f.store.EXPECT().ClaimedPortsByTemplate(tmpl.ID, (*uuid.UUID)(nil)).Return([]services.PortUse{{Transport: "TCP", Port: 5432}}, nil)
		f.domains.EXPECT().UsedPortsByTemplate(tmpl.ID).Return(nil, nil)
		_, err := services.ValidateStreamPortsForTest(f.svc, tmpl, nil, one("TCP", 5432))
		require.ErrorIs(t, err, services.ErrPortCollision)
		assert.Contains(t, err.Error(), "TCP/5432")
	})

	t.Run("selfID is passed so the stream's own claims are excluded", func(t *testing.T) {
		f := newClaimsFixture(t)
		// The store applies the exclusion; the service must hand it selfID
		// (the strict expectation fails on any other argument).
		f.store.EXPECT().ClaimedPortsByTemplate(tmpl.ID, &selfID).Return(nil, nil).Once()
		f.domains.EXPECT().UsedPortsByTemplate(tmpl.ID).Return(nil, nil).Once()
		_, err := services.ValidateStreamPortsForTest(f.svc, tmpl, &selfID, one("TCP", 5432))
		require.NoError(t, err)
	})
}

func TestValidateStreamPorts_DedicatedIgnoresOthers(t *testing.T) {
	// Strict mocks: a dedicated template must not read the store or domains,
	// so another stream's TCP/5432 claim cannot matter.
	f := newClaimsFixture(t)
	got, err := services.ValidateStreamPortsForTest(f.svc, claimsTemplate(false), nil, models.StreamPorts{{Transport: "TCP", Port: 5432}})
	require.NoError(t, err)
	assert.Equal(t, models.StreamPorts{{Transport: "TCP", Port: 5432}}, got)
}

func TestValidateStreamPorts_SourceErrorsPropagate(t *testing.T) {
	tmpl := claimsTemplate(true)
	f := newClaimsFixture(t)
	f.store.EXPECT().ClaimedPortsByTemplate(tmpl.ID, (*uuid.UUID)(nil)).Return(nil, assert.AnError)
	_, err := services.ValidateStreamPortsForTest(f.svc, tmpl, nil, models.StreamPorts{{Transport: "TCP", Port: 5432}})
	require.ErrorIs(t, err, assert.AnError)
	assert.NotErrorIs(t, err, services.ErrPortCollision)
}

func TestValidateStreamPorts_UnwiredFailsClosed(t *testing.T) {
	svc := services.NewStreamService(mocks.NewMockStreamStore(t), &fakeTemplateReader{}, &fakeRouteCounter{},
		mocks.NewMockGatewayApplier(t), allowAllNamespaces{})
	_, err := services.ValidateStreamPortsForTest(svc, claimsTemplate(true), nil, models.StreamPorts{{Transport: "TCP", Port: 5432}})
	assert.ErrorContains(t, err, "not configured")
}

func TestValidateStreamPorts_NormalizesTransportAndOrder(t *testing.T) {
	f := newClaimsFixture(t)
	got, err := services.ValidateStreamPortsForTest(f.svc, claimsTemplate(false), nil,
		models.StreamPorts{{Transport: "udp", Port: 53}, {Transport: "tcp", Port: 9000}})
	require.NoError(t, err)
	assert.Equal(t, models.StreamPorts{{Transport: "TCP", Port: 9000}, {Transport: "UDP", Port: 53}}, got)
}

func TestCheckUsedClaimsKept(t *testing.T) {
	stream := &models.Stream{ID: uuid.New(), Name: "db"}
	setup := func(t *testing.T) *claimsFixture {
		f := newClaimsFixture(t)
		// 5432 is held by a route row, 5433 only by a pending approval.
		f.store.EXPECT().UsedPortsByStream(stream.ID, (*uuid.UUID)(nil)).
			Return([]services.PortUse{{Transport: "TCP", Port: 5432}, {Transport: "TCP", Port: 5433}}, nil)
		f.store.EXPECT().L4PortOwnersByStream(stream.ID).Return([]services.L4PortOwner{
			{Transport: "TCP", Port: 5432, RouteName: "primary", Status: "active"},
		}, nil).Maybe()
		f.store.EXPECT().PendingL4PortOwnersByStream(stream.ID).Return([]services.L4PortOwner{
			{Transport: "TCP", Port: 5433, RouteName: "cache", Status: "active", Pending: true},
		}, nil).Maybe()
		return f
	}

	t.Run("dropping a pending-held claim is refused", func(t *testing.T) {
		f := setup(t)
		err := services.CheckUsedClaimsKeptForTest(f.svc, stream, models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "TCP", Port: 9000}})
		require.ErrorIs(t, err, services.ErrStreamPortInUse)
		assert.Contains(t, err.Error(), "TCP/5433")
		assert.Contains(t, err.Error(), `"cache"`)
		assert.Contains(t, err.Error(), "pending")
	})

	t.Run("dropping a row-held claim names the route, not pending", func(t *testing.T) {
		f := setup(t)
		err := services.CheckUsedClaimsKeptForTest(f.svc, stream, models.StreamPorts{{Transport: "TCP", Port: 5433}})
		require.ErrorIs(t, err, services.ErrStreamPortInUse)
		assert.Contains(t, err.Error(), "TCP/5432")
		assert.Contains(t, err.Error(), `"primary"`)
		assert.NotContains(t, err.Error(), "pending")
	})

	t.Run("keeping every used claim passes", func(t *testing.T) {
		f := setup(t)
		require.NoError(t, services.CheckUsedClaimsKeptForTest(f.svc, stream,
			models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "TCP", Port: 5433}, {Transport: "UDP", Port: 53}}))
	})

	t.Run("transport is part of the claim", func(t *testing.T) {
		f := setup(t)
		err := services.CheckUsedClaimsKeptForTest(f.svc, stream, models.StreamPorts{{Transport: "UDP", Port: 5432}, {Transport: "UDP", Port: 5433}})
		require.ErrorIs(t, err, services.ErrStreamPortInUse)
	})

	t.Run("no used ports means nothing is held", func(t *testing.T) {
		f := newClaimsFixture(t)
		f.store.EXPECT().UsedPortsByStream(stream.ID, (*uuid.UUID)(nil)).Return(nil, nil)
		require.NoError(t, services.CheckUsedClaimsKeptForTest(f.svc, stream, models.StreamPorts{{Transport: "TCP", Port: 1}}))
	})

	t.Run("load error propagates", func(t *testing.T) {
		f := newClaimsFixture(t)
		f.store.EXPECT().UsedPortsByStream(stream.ID, (*uuid.UUID)(nil)).Return(nil, assert.AnError)
		require.ErrorIs(t, services.CheckUsedClaimsKeptForTest(f.svc, stream, nil), assert.AnError)
	})
}
