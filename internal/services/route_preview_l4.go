package services

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// PreviewCreateForStream renders the TCPRoute/UDPRoute and BackendTrafficPolicy
// a create under streamID would produce, after running exactly the
// validation CreateForStream runs (validateCreate, then labels). It persists
// nothing; its errors are create's errors, so the handler maps them to the
// same 400/404/409.
func (w *routeWrite) PreviewCreateForStream(projectID, streamID uuid.UUID, input *CreateRouteInput) (*PreviewCreateResult, error) {
	in := *input
	in.StreamID = &streamID
	if _, err := w.validateCreate(uuid.Nil, &in, &projectID); err != nil {
		return nil, err
	}
	if in.Labels != nil {
		if err := models.ValidateLabels(in.Labels); err != nil {
			return nil, err
		}
	}
	stream, err := w.resolveStream(streamID, &projectID)
	if err != nil {
		return nil, err
	}

	// The real create mints the ID at persist time; the preview mints a
	// temporary one, as the domain PreviewCreate does.
	tempID := w.assembler.newID()
	route := &models.Route{
		ID:           tempID,
		StreamID:     &streamID,
		TeamID:       in.TeamID,
		Name:         in.Name,
		Description:  in.Description,
		Protocol:     in.Protocol,
		SecurityMode: models.SecurityModeGeneral,
		Config:       in.Config,
		K8sRouteName: kubernetes.RouteK8sName(in.Name, tempID.String()),
	}
	routeYAML, err := w.query.renderL4RouteYAML(route, stream)
	if err != nil {
		return nil, err
	}
	btpYAML, err := generateL4BackendTrafficPolicyYAML(route, stream, l4PolicyFromConfig(route, btpConfigFromInput(in.BackendTrafficPolicy)))
	if err != nil {
		return nil, err
	}
	return &PreviewCreateResult{ProposedYAML: routeYAML, ProposedBackendTrafficPolicyYAML: btpYAML}, nil
}

// PreviewUpdateForStream renders the current and proposed TCPRoute/UDPRoute
// and BackendTrafficPolicy of an L4 route, after running exactly the input
// validation Update runs (validateUpdate) - including the port re-check
// against the stream with the route itself excluded. It persists nothing. A
// route that is not L4 is ErrRouteNotFound.
func (w *routeWrite) PreviewUpdateForStream(routeID uuid.UUID, input *UpdateRouteInput) (*PreviewUpdateResult, error) {
	route, err := w.routeRepo.GetByID(routeID)
	if err != nil || route == nil || !route.IsL4() {
		return nil, ErrRouteNotFound
	}
	if _, err := w.validateUpdate(routeID, route, input); err != nil {
		return nil, err
	}
	return w.query.previewUpdateL4(route, input)
}

// PreviewCreateForStream previews an L4 route create under a stream with
// CreateForStream's validation (streamID must belong to projectID).
func (s *RouteService) PreviewCreateForStream(projectID, streamID uuid.UUID, input *CreateRouteInput) (*PreviewCreateResult, error) {
	return s.write.PreviewCreateForStream(projectID, streamID, input)
}

// PreviewUpdateForStream previews an L4 route update with Update's validation.
func (s *RouteService) PreviewUpdateForStream(routeID uuid.UUID, input *UpdateRouteInput) (*PreviewUpdateResult, error) {
	return s.write.PreviewUpdateForStream(routeID, input)
}
