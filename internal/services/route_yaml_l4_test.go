package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newL4YAMLFixtureWithBTP is newL4YAMLFixture with a BackendTrafficPolicy
// repository the test controls.
func newL4YAMLFixtureWithBTP(t *testing.T, btpRepo *mocks.MockBackendTrafficPolicyRepository) *l4YAMLFixture {
	t.Helper()
	f := &l4YAMLFixture{streamRepo: new(mocks.MockStreamReader)}
	f.svc, f.routeRepo, _, _, _, _ = newTestRouteServiceWith(func(d *services.RouteServiceDeps) {
		d.Streams = f.streamRepo
		d.BackendTrafficPolicyRepo = btpRepo
	})
	f.stream = &models.Stream{
		ID: uuid.New(), ProjectID: uuid.New(), Name: "db", Namespace: "fastgateway-system",
		K8sGatewayName: "str-db", K8sGatewayClass: "public-lb",
	}
	f.streamRepo.On("GetByID", f.stream.ID).Return(f.stream, nil)
	return f
}

func TestRouteYAML_L4_GenerateYAMLs_IncludesBackendTrafficPolicy(t *testing.T) {
	btpRepo := new(mocks.MockBackendTrafficPolicyRepository)
	f := newL4YAMLFixtureWithBTP(t, btpRepo)
	route := f.route(models.RouteProtocolTCP, 5432)
	btpRepo.On("GetByRouteID", route.ID).Return(&models.BackendTrafficPolicy{ID: uuid.New(), RouteID: &route.ID, Config: tcpBTPConfig()}, nil)

	yamls, err := f.svc.GenerateYAMLs(route.ID)
	require.NoError(t, err)

	assert.Contains(t, yamls.HTTPRouteYAML, "kind: TCPRoute", "the route stays in httpRouteYaml")
	assert.Contains(t, yamls.BackendTrafficPolicyYAML, "kind: BackendTrafficPolicy")
	assert.Contains(t, yamls.BackendTrafficPolicyYAML, "kind: TCPRoute", "targetRef is the TCPRoute")
	assert.Contains(t, yamls.BackendTrafficPolicyYAML, "name: pg-abcd1234")
	assert.Contains(t, yamls.BackendTrafficPolicyYAML, "namespace: fastgateway-system", "the policy lives in the stream namespace")
	assert.Contains(t, yamls.BackendTrafficPolicyYAML, "maxConnections: 100")
	assert.Empty(t, yamls.SecurityPolicyYAML)
}

func TestRouteYAML_L4_GenerateYAMLs_NoPolicyRecordIsEmpty(t *testing.T) {
	btpRepo := new(mocks.MockBackendTrafficPolicyRepository)
	f := newL4YAMLFixtureWithBTP(t, btpRepo)
	route := f.route(models.RouteProtocolUDP, 5353)
	btpRepo.On("GetByRouteID", route.ID).Return(nil, gorm.ErrRecordNotFound)

	yamls, err := f.svc.GenerateYAMLs(route.ID)
	require.NoError(t, err)
	assert.Contains(t, yamls.HTTPRouteYAML, "kind: UDPRoute")
	assert.Empty(t, yamls.BackendTrafficPolicyYAML)
}

func TestRouteYAML_L4_GenerateYAMLs_PolicyLookupFailureIsAnError(t *testing.T) {
	btpRepo := new(mocks.MockBackendTrafficPolicyRepository)
	f := newL4YAMLFixtureWithBTP(t, btpRepo)
	route := f.route(models.RouteProtocolTCP, 5432)
	btpRepo.On("GetByRouteID", route.ID).Return(nil, errors.New("connection reset"))

	yamls, err := f.svc.GenerateYAMLs(route.ID)
	assert.Nil(t, yamls, "never a manifest set with the policy silently missing")
	assert.ErrorContains(t, err, "connection reset")
}

func TestRouteService_RenderL4BackendTrafficPolicyYAML(t *testing.T) {
	svc, _, _, _, _, _ := newTestRouteService()
	stream := &models.Stream{ID: uuid.New(), Namespace: "edge", K8sGatewayName: "str-edge"}
	route := &models.Route{ID: uuid.New(), Name: "pg", K8sRouteName: "pg-abcd1234", Protocol: models.RouteProtocolTCP}

	none, err := svc.RenderL4BackendTrafficPolicyYAML(route, stream, nil)
	require.NoError(t, err)
	assert.Empty(t, none)

	empty, err := svc.RenderL4BackendTrafficPolicyYAML(route, stream, &models.BackendTrafficPolicyConfig{})
	require.NoError(t, err)
	assert.Empty(t, empty, "an empty policy is not applied, so it is not shown")

	cfg := tcpBTPConfig()
	out, err := svc.RenderL4BackendTrafficPolicyYAML(route, stream, &cfg)
	require.NoError(t, err)
	assert.Contains(t, out, "namespace: edge")
	assert.Contains(t, out, "kind: TCPRoute")

	routeYAML, err := svc.RenderL4RouteYAML(route, stream)
	require.NoError(t, err)
	assert.Contains(t, routeYAML, "kind: TCPRoute")
	assert.Contains(t, routeYAML, "name: str-edge")
}
