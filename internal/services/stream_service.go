// StreamService: create/update/delete of L4 (TCP/UDP) Streams.
//
// A Stream is the L4 analogue of a Domain: one Stream maps to one Gateway
// and is created from a Gateway Template that has the stream capability
// (DomainTemplate.EnableStream). The referenced template is immutable after
// creation. Create eagerly deploys the Gateway (see Deploy).

package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/streamplan"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	// ErrTemplateNotStreamEnabled is returned by Create when the chosen
	// Gateway Template does not have the stream capability enabled.
	ErrTemplateNotStreamEnabled = errors.New("gateway template is not enabled for streams")
	// ErrStreamHasRoutes is returned by Delete while the stream still owns routes.
	ErrStreamHasRoutes = errors.New("stream has active routes; remove them before deleting")
	// ErrStreamTemplateImmutable is returned when a caller tries to change a
	// stream's Gateway Template after creation.
	ErrStreamTemplateImmutable = errors.New("stream gateway template cannot be changed after creation")
	// ErrStreamNotFound is returned when the stream does not exist.
	ErrStreamNotFound = errors.New("stream not found")
)

// streamGatewayPrefix namespaces Stream Gateways away from Domain Gateways
// (which are named from the hostname) so the two kinds can never collide.
const streamGatewayPrefix = "str-"

// Stream lifecycle statuses.
const (
	StreamStatusActive = "active"
	StreamStatusError  = "error"
)

// maxK8sNameLen is the DNS-label limit for a Kubernetes resource name.
const maxK8sNameLen = 63

// CreateStreamInput is the input for StreamService.Create.
type CreateStreamInput struct {
	Name              string
	Namespace         string
	GatewayTemplateID uuid.UUID
}

// UpdateStreamInput is the input for StreamService.Update. The Gateway
// Template is immutable, so only the name is updatable.
type UpdateStreamInput struct {
	Name *string
}

// StreamStore is the slice of StreamRepository that StreamService uses.
// *repository.StreamRepository satisfies it structurally.
type StreamStore interface {
	Create(stream *models.Stream) error
	GetByID(id uuid.UUID) (*models.Stream, error)
	Update(stream *models.Stream) error
	Delete(id uuid.UUID) error
}

// StreamTemplateReader resolves a Gateway Template.
// repository.DomainTemplateRepositoryInterface satisfies it structurally.
type StreamTemplateReader interface {
	GetByID(id uuid.UUID) (*models.DomainTemplate, error)
}

// StreamRouteCounter counts the routes attached to a stream.
// repository.RouteRepositoryInterface satisfies it structurally.
type StreamRouteCounter interface {
	CountByStreamID(streamID uuid.UUID) (int64, error)
}

// StreamService handles stream business logic.
type StreamService struct {
	streamRepo   StreamStore
	templateRepo StreamTemplateReader
	routeRepo    StreamRouteCounter
	k8sGateways  GatewayApplier
}

// NewStreamService builds a StreamService. It panics if a dependency is nil,
// matching NewDomainService.
func NewStreamService(streamRepo StreamStore, templateRepo StreamTemplateReader, routeRepo StreamRouteCounter, k8sGateways GatewayApplier) *StreamService {
	if streamRepo == nil || templateRepo == nil || routeRepo == nil || k8sGateways == nil {
		panic("services.NewStreamService: missing required dependency")
	}
	return &StreamService{streamRepo: streamRepo, templateRepo: templateRepo, routeRepo: routeRepo, k8sGateways: k8sGateways}
}

// StreamGatewayName returns the Kubernetes Gateway name for a stream: "str-"
// followed by the lowercased, DNS-label-safe stream name.
func StreamGatewayName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	rest := b.String()
	for strings.Contains(rest, "--") {
		rest = strings.ReplaceAll(rest, "--", "-")
	}
	rest = strings.Trim(rest, "-")
	if max := maxK8sNameLen - len(streamGatewayPrefix); len(rest) > max {
		rest = strings.TrimRight(rest[:max], "-")
	}
	return streamGatewayPrefix + rest
}

// Create persists a new stream in the project. The Gateway Template must
// belong to the project and have the stream capability; the Gateway class is
// copied from it. The Gateway is then deployed eagerly (placeholder listener
// until routes exist); like DomainService.Create, a deploy failure is recorded
// on the returned stream's status rather than returned as an error.
func (s *StreamService) Create(projectID uuid.UUID, in CreateStreamInput, user *models.User) (*models.Stream, error) {
	tmpl, err := s.templateRepo.GetByID(in.GatewayTemplateID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("gateway template not found")
		}
		return nil, fmt.Errorf("failed to load gateway template: %w", err)
	}
	if tmpl.ProjectID != projectID {
		return nil, errors.New("gateway template does not belong to this project")
	}
	if !tmpl.EnableStream {
		return nil, ErrTemplateNotStreamEnabled
	}

	stream := &models.Stream{
		ProjectID:         projectID,
		Name:              in.Name,
		Namespace:         in.Namespace,
		GatewayTemplateID: in.GatewayTemplateID,
		K8sGatewayName:    StreamGatewayName(in.Name),
		K8sGatewayClass:   tmpl.K8sGatewayClassName,
	}
	if user != nil {
		id := user.ID
		stream.CreatedBy = &id
	}
	if err := s.streamRepo.Create(stream); err != nil {
		return nil, err
	}
	if err := s.Deploy(context.Background(), stream); err != nil {
		log.Printf("Failed to deploy Stream Gateway %s/%s: %v", stream.Namespace, stream.K8sGatewayName, err)
	}
	return stream, nil
}

// Deploy builds the stream's Gateway and applies it to the project's cluster,
// then records the outcome on the stream (Status active, or error with the
// failure in StatusMessage).
//
// The GatewayConfig is built and handed to the applier entirely in-process:
// its L4 Listeners are json:"-" and would be lost if the config were
// serialized (e.g. queued as a job payload), silently degrading the Gateway
// to an HTTP listener. The applier renders it with kubernetes.BuildGatewayObject.
//
// No routes are passed yet, so the placeholder listener is emitted; the
// route-driven listener projection is wired in when routes attach.
func (s *StreamService) Deploy(ctx context.Context, stream *models.Stream) error {
	cfg := streamplan.BuildStreamGatewayConfig(*stream, nil)
	if err := s.k8sGateways.CreateGateway(ctx, stream.ProjectID, &cfg); err != nil {
		stream.Status = StreamStatusError
		stream.StatusMessage = fmt.Sprintf("Failed to create Gateway: %v", err)
		_ = s.streamRepo.Update(stream)
		return fmt.Errorf("failed to deploy stream gateway: %w", err)
	}
	stream.Status = StreamStatusActive
	stream.StatusMessage = "Gateway created successfully"
	if err := s.streamRepo.Update(stream); err != nil {
		return fmt.Errorf("failed to update stream status: %w", err)
	}
	return nil
}

// Update renames a stream. The Gateway Template is immutable, and the
// Kubernetes Gateway name is fixed at creation (renaming it would orphan the
// deployed Gateway), so only the display name changes.
func (s *StreamService) Update(id uuid.UUID, in UpdateStreamInput) (*models.Stream, error) {
	stream, err := s.getStream(id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		stream.Name = *in.Name
	}
	if err := s.streamRepo.Update(stream); err != nil {
		return nil, err
	}
	return stream, nil
}

// Delete removes a stream, refusing while it still has routes.
func (s *StreamService) Delete(id uuid.UUID) error {
	if _, err := s.getStream(id); err != nil {
		return err
	}
	n, err := s.routeRepo.CountByStreamID(id)
	if err != nil {
		return fmt.Errorf("failed to count stream routes: %w", err)
	}
	if n > 0 {
		return ErrStreamHasRoutes
	}
	return s.streamRepo.Delete(id)
}

func (s *StreamService) getStream(id uuid.UUID) (*models.Stream, error) {
	stream, err := s.streamRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrStreamNotFound
		}
		return nil, err
	}
	return stream, nil
}
