package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// TestStreamYAMLs_EqualWhatDeployL4Applies is the byte-identical guard for
// the view. The Gateway is the stream's, applied by StreamService.ApplyGateway
// (route deploy no longer writes it): capture the GatewayConfig ApplyGateway
// hands the cluster client. The TCPRoute and BackendTrafficPolicy are the
// route's: deploy an update through the real deployL4 path and capture what it
// hands the client. Render them with the same object builders the client uses
// and require GenerateStreamYAMLs to return exactly those bytes for the same
// stream and live route set.
func TestStreamYAMLs_EqualWhatDeployL4Applies(t *testing.T) {
	f := newL4Fixture(t)
	f.stream.Ports = models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "UDP", Port: 53}}
	current := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusPendingDeploy)
	other := f.route("dns", models.RouteProtocolUDP, 53, models.RouteStatusActive)
	f.withBTP(current, tcpBTPConfig())
	f.expectDeploy(current, models.ApprovalActionUpdate)
	f.btpRepo.On("GetByRouteID", other.ID).Return(nil, nil).Maybe()
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{*current, *other}, nil)
	f.routeRepo.On("Update", mock.Anything).Return(nil)

	var gw *kubernetes.GatewayConfig
	gateways := mocks.NewMockGatewayApplier(t)
	gateways.EXPECT().UpdateGateway(mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.GatewayConfig")).
		Run(func(_ context.Context, _ uuid.UUID, cfg *kubernetes.GatewayConfig) { gw = cfg }).Return(nil)
	streamStore := mocks.NewMockStreamStore(t)
	streamStore.EXPECT().Update(mock.Anything).Return(nil)
	streams := services.NewStreamService(streamStore, &fakeTemplateReader{}, &fakeRouteCounter{}, gateways, allowAllNamespaces{})
	require.NoError(t, streams.ApplyGateway(context.Background(), f.stream))
	require.NotNil(t, gw)

	var tcp *kubernetes.TCPRouteConfig
	f.k8s.On("UpdateTCPRoute", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.TCPRouteConfig"), kubernetes.TCPRouteGVR).
		Run(func(args mock.Arguments) { tcp = args.Get(2).(*kubernetes.TCPRouteConfig) }).Return(nil)
	var btp *kubernetes.BackendTrafficPolicyConfig
	f.k8s.On("UpdateBackendTrafficPolicy", mock.Anything, f.stream.ProjectID, mock.AnythingOfType("*kubernetes.BackendTrafficPolicyConfig")).
		Run(func(args mock.Arguments) { btp = args.Get(2).(*kubernetes.BackendTrafficPolicyConfig) }).Return(nil)

	_, err := f.svc.Deploy(current.ID, uuid.New())
	require.NoError(t, err)
	f.k8s.AssertNotCalled(t, "UpdateGateway", mock.Anything, mock.Anything, mock.Anything)
	require.NotNil(t, tcp)
	require.NotNil(t, btp)

	yamls, err := f.svc.GenerateStreamYAMLs(f.stream.ProjectID, f.stream.ID)
	require.NoError(t, err)

	wantGateway, err := yaml.Marshal(kubernetes.BuildGatewayObject(gw).Object)
	require.NoError(t, err)
	assert.Equal(t, string(wantGateway), yamls.GatewayYAML, "gatewayYaml must be the object the stream applies")

	require.Len(t, yamls.Routes, 2)
	assert.Equal(t, current.ID, yamls.Routes[0].RouteID)
	assert.Equal(t, "pg", yamls.Routes[0].Name)
	assert.Equal(t, models.RouteProtocolTCP, yamls.Routes[0].Protocol)
	wantRoute, err := yaml.Marshal(kubernetes.BuildTCPRouteObject(*tcp, kubernetes.TCPRouteGVR.GroupVersion().String()))
	require.NoError(t, err)
	assert.Equal(t, string(wantRoute), yamls.Routes[0].RouteYAML, "routeYaml must be the TCPRoute UpdateTCPRoute applied")
	wantBTP, err := yaml.Marshal(kubernetes.BuildBackendTrafficPolicy(btp).Object)
	require.NoError(t, err)
	assert.Equal(t, string(wantBTP), yamls.Routes[0].BackendTrafficPolicyYAML, "the policy must be the one deploy applied")

	assert.Equal(t, other.ID, yamls.Routes[1].RouteID)
	assert.Contains(t, yamls.Routes[1].RouteYAML, "kind: UDPRoute")
	assert.Empty(t, yamls.Routes[1].BackendTrafficPolicyYAML, "no policy, no policy YAML")
}

func TestGenerateStreamYAMLs_GatewayFromClaims(t *testing.T) {
	f := newL4Fixture(t)
	f.stream.Ports = models.StreamPorts{{Transport: "TCP", Port: 5432}, {Transport: "UDP", Port: 53}}
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{}, nil)

	yamls, err := f.svc.GenerateStreamYAMLs(f.stream.ProjectID, f.stream.ID)
	require.NoError(t, err)

	// The listeners are the claims, whether or not a route uses them.
	assert.Contains(t, yamls.GatewayYAML, "kind: Gateway")
	assert.Contains(t, yamls.GatewayYAML, "name: l4-tcp-5432")
	assert.Contains(t, yamls.GatewayYAML, "port: 5432")
	assert.Contains(t, yamls.GatewayYAML, "name: l4-udp-53")
	assert.Contains(t, yamls.GatewayYAML, "port: 53")
	assert.Equal(t, []services.StreamRouteYAMLs{}, yamls.Routes)
	raw, err := json.Marshal(yamls)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"routes":[]`)
}

func TestGenerateStreamYAMLs_NoClaimsNoGateway(t *testing.T) {
	f := newL4Fixture(t)
	f.stream.Ports = nil
	f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{}, nil)

	yamls, err := f.svc.GenerateStreamYAMLs(f.stream.ProjectID, f.stream.ID)
	require.NoError(t, err)
	assert.Equal(t, "", yamls.GatewayYAML, "no claims: no Gateway is deployed, so there is no YAML")
}

func TestStreamYAMLs_OtherProjectStreamIsNotFound(t *testing.T) {
	f := newL4Fixture(t)
	_, err := f.svc.GenerateStreamYAMLs(uuid.New(), f.stream.ID)
	assert.ErrorIs(t, err, services.ErrStreamNotFound)
}

func TestStreamYAMLs_ReadFailuresAreErrors(t *testing.T) {
	t.Run("live route listing", func(t *testing.T) {
		f := newL4Fixture(t)
		f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return(nil, errors.New("db down"))
		yamls, err := f.svc.GenerateStreamYAMLs(f.stream.ProjectID, f.stream.ID)
		assert.Nil(t, yamls)
		assert.ErrorContains(t, err, "db down")
	})
	t.Run("a route's policy", func(t *testing.T) {
		f := newL4Fixture(t)
		route := f.route("pg", models.RouteProtocolTCP, 5432, models.RouteStatusActive)
		f.routeRepo.On("ListActiveByStreamID", f.stream.ID).Return([]models.Route{*route}, nil)
		f.btpRepo.On("GetByRouteID", route.ID).Return(nil, errors.New("connection reset"))
		yamls, err := f.svc.GenerateStreamYAMLs(f.stream.ProjectID, f.stream.ID)
		assert.Nil(t, yamls, "never a partial manifest set")
		assert.ErrorContains(t, err, "connection reset")
	})
}

func TestStreamYAMLs_JSONContract(t *testing.T) {
	id := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	raw, err := json.Marshal(services.StreamYAMLs{
		GatewayYAML: "g",
		Routes: []services.StreamRouteYAMLs{{RouteID: id, Name: "postgres", Protocol: models.RouteProtocolTCP,
			RouteYAML: "r", BackendTrafficPolicyYAML: ""}},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"gatewayYaml":"g","routes":[{"routeId":"22222222-2222-2222-2222-222222222222",
		"name":"postgres","protocol":"tcp","routeYaml":"r","backendTrafficPolicyYaml":""}]}`, string(raw))
}
