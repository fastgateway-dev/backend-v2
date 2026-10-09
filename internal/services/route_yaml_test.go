package services_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// l4YAMLFixture wires a RouteService whose stream reader is a mock and whose
// domain repository is bare: an L4 route has no DomainID, so any domainRepo
// call during an L4 preview panics (mock with no expectation) - the
// nil-DomainID deref guard.
type l4YAMLFixture struct {
	svc        *services.RouteService
	routeRepo  *mocks.MockRouteRepository
	streamRepo *mocks.MockStreamReader
	stream     *models.Stream
}

func newL4YAMLFixture(t *testing.T) *l4YAMLFixture {
	t.Helper()
	f := &l4YAMLFixture{streamRepo: new(mocks.MockStreamReader)}
	f.svc, f.routeRepo, _, _, _, _ = newTestRouteServiceWith(func(d *services.RouteServiceDeps) {
		d.Streams = f.streamRepo
	})
	f.stream = &models.Stream{
		ID: uuid.New(), ProjectID: uuid.New(), Name: "db", Namespace: "fastgateway-system",
		K8sGatewayName: "str-db", K8sGatewayClass: "public-lb",
	}
	f.streamRepo.On("GetByID", f.stream.ID).Return(f.stream, nil)
	return f
}

func (f *l4YAMLFixture) route(proto models.RouteProtocol, port int) *models.Route {
	sid := f.stream.ID
	r := &models.Route{
		ID: uuid.New(), StreamID: &sid, Name: "pg", K8sRouteName: "pg-abcd1234",
		Protocol: proto,
		Config: models.RouteConfig{
			ListenerPort: port,
			Backends:     []models.RouteBackend{{Type: models.BackendTypeKubernetes, Service: "pg", Namespace: "default", Port: 5432}},
		},
	}
	f.routeRepo.On("GetByID", r.ID).Return(r, nil)
	return r
}

func TestRouteYAML_L4_TCP_GenerateYAMLs(t *testing.T) {
	f := newL4YAMLFixture(t)
	route := f.route(models.RouteProtocolTCP, 5432)

	yamls, err := f.svc.GenerateYAMLs(route.ID)
	require.NoError(t, err)

	assert.Contains(t, yamls.HTTPRouteYAML, "kind: TCPRoute")
	assert.Contains(t, yamls.HTTPRouteYAML, "apiVersion: gateway.networking.k8s.io/v1alpha2")
	assert.Contains(t, yamls.HTTPRouteYAML, "sectionName: l4-tcp-5432")
	assert.Contains(t, yamls.HTTPRouteYAML, "name: str-db")
	assert.Empty(t, yamls.SecurityPolicyYAML)
	assert.Empty(t, yamls.BackendTrafficPolicyYAML)
	assert.Empty(t, yamls.APIKeyClientResources)
}

func TestRouteYAML_L4_UDP_GenerateYAML(t *testing.T) {
	f := newL4YAMLFixture(t)
	route := f.route(models.RouteProtocolUDP, 5353)

	out, err := f.svc.GenerateYAML(route.ID)
	require.NoError(t, err)

	assert.Contains(t, out, "kind: UDPRoute")
	assert.Contains(t, out, "apiVersion: gateway.networking.k8s.io/v1alpha2")
	assert.Contains(t, out, "sectionName: l4-udp-5353")
}

func TestRouteYAML_L4_PreviewDelete(t *testing.T) {
	f := newL4YAMLFixture(t)
	route := f.route(models.RouteProtocolTCP, 5432)

	res, err := f.svc.PreviewDelete(route.ID)
	require.NoError(t, err)

	assert.Contains(t, res.CurrentYAML, "kind: TCPRoute")
	assert.Contains(t, res.CurrentYAML, "sectionName: l4-tcp-5432")
}

func TestRouteYAML_L4_PreviewUpdate_ShowsCurrentAndProposed(t *testing.T) {
	f := newL4YAMLFixture(t)
	route := f.route(models.RouteProtocolTCP, 5432)

	newCfg := route.Config
	newCfg.ListenerPort = 6432
	res, err := f.svc.PreviewUpdate(route.ID, &services.UpdateRouteInput{Config: newCfg})
	require.NoError(t, err)

	assert.Contains(t, res.CurrentYAML, "sectionName: l4-tcp-5432")
	assert.Contains(t, res.ProposedYAML, "kind: TCPRoute")
	assert.Contains(t, res.ProposedYAML, "sectionName: l4-tcp-6432")
}
