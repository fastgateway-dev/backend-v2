package services

import (
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/streamplan"
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"
)

// StreamYAMLs is GET /projects/:projectId/streams/:streamId/yamls: the
// stream's Gateway and every live L4 route's manifests.
type StreamYAMLs struct {
	GatewayYAML string             `json:"gatewayYaml"`
	Routes      []StreamRouteYAMLs `json:"routes"`
}

// StreamRouteYAMLs is one live L4 route's manifests. BackendTrafficPolicyYAML
// is "" when the route has no policy.
type StreamRouteYAMLs struct {
	RouteID                  uuid.UUID            `json:"routeId"`
	Name                     string               `json:"name"`
	Protocol                 models.RouteProtocol `json:"protocol"`
	RouteYAML                string               `json:"routeYaml"`
	BackendTrafficPolicyYAML string               `json:"backendTrafficPolicyYaml"`
}

// GenerateStreamYAMLs renders the stream's manifests from what is applied: the
// Gateway from the stream's claims (streamplan.BuildStreamGatewayConfig, the
// object builder UpdateGateway applies: kubernetes.BuildGatewayObject) and
// each live route (routeRepo.ListActiveByStreamID). A stream with no claims has
// no Gateway on the cluster, so gatewayYaml is "". Any read failure is an
// error, never a partial manifest set.
func (q *routeQuery) GenerateStreamYAMLs(projectID, streamID uuid.UUID) (*StreamYAMLs, error) {
	stream, err := q.streamInProject(projectID, streamID)
	if err != nil {
		return nil, err
	}
	live, err := q.routeRepo.ListActiveByStreamID(stream.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list active routes for stream: %w", err)
	}

	var gwYAML []byte
	if len(stream.Ports) > 0 {
		gwConfig := streamplan.BuildStreamGatewayConfig(*stream)
		gwYAML, err = yaml.Marshal(kubernetes.BuildGatewayObject(&gwConfig).Object)
		if err != nil {
			return nil, fmt.Errorf("marshal stream Gateway YAML: %w", err)
		}
	}

	out := &StreamYAMLs{GatewayYAML: string(gwYAML), Routes: make([]StreamRouteYAMLs, 0, len(live))}
	for i := range live {
		route := &live[i]
		routeYAML, err := q.renderL4RouteYAML(route, stream)
		if err != nil {
			return nil, err
		}
		policy, err := q.storedBackendTrafficPolicy(route.ID)
		if err != nil {
			return nil, err
		}
		btpYAML, err := generateL4BackendTrafficPolicyYAML(route, stream, policy)
		if err != nil {
			return nil, err
		}
		out.Routes = append(out.Routes, StreamRouteYAMLs{
			RouteID:                  route.ID,
			Name:                     route.Name,
			Protocol:                 route.Protocol,
			RouteYAML:                routeYAML,
			BackendTrafficPolicyYAML: btpYAML,
		})
	}
	return out, nil
}

// GenerateStreamYAMLs renders a stream's Gateway and live L4 route manifests
// (streamID must belong to projectID, else ErrStreamNotFound).
func (s *RouteService) GenerateStreamYAMLs(projectID, streamID uuid.UUID) (*StreamYAMLs, error) {
	return s.query.GenerateStreamYAMLs(projectID, streamID)
}
