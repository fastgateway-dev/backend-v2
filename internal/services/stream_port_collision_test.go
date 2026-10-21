package services_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

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

	t.Run("dropping a claim held only by a deployed version is refused", func(t *testing.T) {
		// An approved update moved route "pg" from 6432 to 6433 in its row
		// (pending_deploy); the cluster still serves its deployed version on
		// 6432, so dropping the 6432 claim would cut live traffic.
		f := newClaimsFixture(t)
		f.store.EXPECT().UsedPortsByStream(stream.ID, (*uuid.UUID)(nil)).
			Return([]services.PortUse{{Transport: "TCP", Port: 6433}, {Transport: "TCP", Port: 6432}}, nil)
		f.store.EXPECT().L4PortOwnersByStream(stream.ID).Return([]services.L4PortOwner{
			{Transport: "TCP", Port: 6433, RouteName: "pg", Status: "pending_deploy"},
		}, nil).Maybe()
		f.store.EXPECT().PendingL4PortOwnersByStream(stream.ID).Return(nil, nil).Maybe()
		f.store.EXPECT().DeployedL4PortOwnersByStream(stream.ID).Return([]services.L4PortOwner{
			{Transport: "TCP", Port: 6432, RouteName: "pg", Status: "pending_deploy"},
		}, nil).Maybe()

		err := services.CheckUsedClaimsKeptForTest(f.svc, stream, models.StreamPorts{{Transport: "TCP", Port: 6433}})
		require.ErrorIs(t, err, services.ErrStreamPortInUse)
		assert.Contains(t, err.Error(), "TCP/6432")
		assert.Contains(t, err.Error(), `"pg"`)
		assert.Contains(t, err.Error(), "deployed")
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

// ---------------------------------------------------------------------------
// Route port check (mock-based, no DB)
// ---------------------------------------------------------------------------

// routePortFixture is a StreamService over a mock stream store that claims
// TCP 5432 and TCP 5433.
type routePortFixture struct {
	svc    *services.StreamService
	store  *mocks.MockStreamPortStore
	stream *models.Stream
}

func newRoutePortFixture(t *testing.T) *routePortFixture {
	t.Helper()
	f := &routePortFixture{
		store: mocks.NewMockStreamPortStore(t),
		stream: &models.Stream{ID: uuid.New(), Name: "db",
			Ports: models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "TCP", Port: 5433}}},
	}
	streams := mocks.NewMockStreamStore(t)
	streams.EXPECT().GetByID(f.stream.ID).Return(f.stream, nil).Maybe()
	f.svc = services.NewStreamService(streams, &fakeTemplateReader{}, &fakeRouteCounter{},
		mocks.NewMockGatewayApplier(t), allowAllNamespaces{})
	f.svc.SetPortSources(f.store, mocks.NewMockDomainPortReader(t))
	return f
}

func TestCheckRoutePort(t *testing.T) {
	t.Run("unclaimed port lists the stream's claims for that transport", func(t *testing.T) {
		f := newRoutePortFixture(t)
		err := f.svc.CheckRoutePort(f.stream.ID, "TCP", 5434, nil)
		require.ErrorIs(t, err, services.ErrPortNotClaimed)
		assert.Contains(t, err.Error(), "TCP/5432, TCP/5433")
		assert.Contains(t, err.Error(), "TCP/5434")
	})

	t.Run("claimed under the other transport is unclaimed", func(t *testing.T) {
		f := newRoutePortFixture(t)
		err := f.svc.CheckRoutePort(f.stream.ID, "UDP", 5432, nil)
		require.ErrorIs(t, err, services.ErrPortNotClaimed)
		assert.Contains(t, err.Error(), "none")
	})

	t.Run("claimed port used by another route collides", func(t *testing.T) {
		f := newRoutePortFixture(t)
		f.store.EXPECT().UsedPortsByStream(f.stream.ID, (*uuid.UUID)(nil)).
			Return([]services.PortUse{{Transport: "TCP", Port: 5432}}, nil)
		err := f.svc.CheckRoutePort(f.stream.ID, "TCP", 5432, nil)
		require.ErrorIs(t, err, services.ErrPortCollision)
		assert.NotErrorIs(t, err, services.ErrPortNotClaimed)
	})

	t.Run("claimed port held only by a pending approval collides", func(t *testing.T) {
		f := newRoutePortFixture(t)
		// UsedPortsByStream folds pending create/update approvals into its rows.
		f.store.EXPECT().UsedPortsByStream(f.stream.ID, (*uuid.UUID)(nil)).
			Return([]services.PortUse{{Transport: "TCP", Port: 5433}}, nil)
		require.ErrorIs(t, f.svc.CheckRoutePort(f.stream.ID, "tcp", 5433, nil), services.ErrPortCollision)
	})

	t.Run("claimed and free passes", func(t *testing.T) {
		f := newRoutePortFixture(t)
		f.store.EXPECT().UsedPortsByStream(f.stream.ID, (*uuid.UUID)(nil)).
			Return([]services.PortUse{{Transport: "TCP", Port: 5432}}, nil)
		require.NoError(t, f.svc.CheckRoutePort(f.stream.ID, "TCP", 5433, nil))
	})

	t.Run("used-port lookup failure is not a collision", func(t *testing.T) {
		f := newRoutePortFixture(t)
		f.store.EXPECT().UsedPortsByStream(f.stream.ID, (*uuid.UUID)(nil)).Return(nil, assert.AnError)
		err := f.svc.CheckRoutePort(f.stream.ID, "TCP", 5432, nil)
		require.ErrorIs(t, err, assert.AnError)
		assert.NotErrorIs(t, err, services.ErrPortCollision)
	})

	t.Run("invalid transport", func(t *testing.T) {
		f := newRoutePortFixture(t)
		assert.Error(t, f.svc.CheckRoutePort(f.stream.ID, "HTTP", 5432, nil))
	})

	t.Run("unwired port store fails closed", func(t *testing.T) {
		f := newRoutePortFixture(t)
		svc := services.NewStreamService(mocks.NewMockStreamStore(t), &fakeTemplateReader{}, &fakeRouteCounter{},
			mocks.NewMockGatewayApplier(t), allowAllNamespaces{})
		assert.ErrorContains(t, svc.CheckRoutePort(f.stream.ID, "TCP", 5432, nil), "not configured")
	})
}

func TestCheckRoutePort_UnknownStream(t *testing.T) {
	f := newRoutePortFixture(t)
	streams := mocks.NewMockStreamStore(t)
	missing := uuid.New()
	streams.EXPECT().GetByID(missing).Return(nil, gorm.ErrRecordNotFound)
	svc := services.NewStreamService(streams, &fakeTemplateReader{}, &fakeRouteCounter{},
		mocks.NewMockGatewayApplier(t), allowAllNamespaces{})
	svc.SetPortSources(f.store, mocks.NewMockDomainPortReader(t))
	assert.ErrorIs(t, svc.CheckRoutePort(missing, "TCP", 5432, nil), services.ErrStreamNotFound)
}

func TestCheckRoutePort_UpdateKeepingOwnPortIsFree(t *testing.T) {
	f := newRoutePortFixture(t)
	routeA := uuid.New()
	// The store applies the exclusion: with route A excluded, nothing holds 5432.
	f.store.EXPECT().UsedPortsByStream(f.stream.ID, &routeA).Return(nil, nil)
	require.NoError(t, f.svc.CheckRoutePort(f.stream.ID, "TCP", 5432, &routeA))
}
