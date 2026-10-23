// StreamService: create/update/delete of L4 (TCP/UDP) Streams.
//
// A Stream is the L4 analogue of a Domain: one Stream maps to one Gateway
// and is created from a Gateway Template that has the stream capability
// (it declares a TCP/UDP listener range). The referenced template is immutable after
// creation. A stream owns its Gateway: the listeners are its claimed ports,
// and create/update/delete write (or delete) the Gateway (see ApplyGateway).

package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/capabilities"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/streamplan"
	"github.com/google/uuid"
	"gorm.io/gorm"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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
	// ErrStreamTemplateNotFound is returned by Create when the chosen Gateway
	// Template does not exist.
	ErrStreamTemplateNotFound = errors.New("gateway template not found")
	// ErrStreamTemplateWrongProject is returned by Create when the chosen
	// Gateway Template belongs to a different project. Callers should report
	// it as not-found so template existence is not leaked across projects.
	ErrStreamTemplateWrongProject = errors.New("gateway template does not belong to this project")
	// ErrStreamNameTaken is returned when another stream in the project
	// already has the requested name.
	ErrStreamNameTaken = errors.New("a stream with this name already exists in this project")
	// ErrStreamNamespaceNotRegistered is returned by Create when the namespace
	// is not whitelisted for the project (Project Settings > Namespaces).
	ErrStreamNamespaceNotRegistered = errors.New("namespace is not registered for this project")
	// ErrStreamNamespaceNotDeployable is returned by Create when the namespace
	// is whitelisted but lacks the deploy-gateway capability.
	ErrStreamNamespaceNotDeployable = errors.New("namespace is not enabled for gateway deployment")
	// ErrInvalidStreamName is returned (wrapped) by ValidateStreamName.
	ErrInvalidStreamName = errors.New("invalid stream name")
)

// maxStreamNameLen keeps "str-" + name within the 63-char DNS-label limit.
const maxStreamNameLen = 63 - len(streamGatewayPrefix)

// streamNameRE: lowercase alphanumerics separated by single dashes, starting
// and ending alphanumeric. Single dashes only, so StreamGatewayName (which
// collapses dash runs) is injective over valid names.
var streamNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidateStreamName checks that name is a DNS-label-safe stream name, so the
// derived Gateway name is never just "str-" and two distinct valid names can
// never map to the same Gateway.
func ValidateStreamName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidStreamName)
	}
	if len(name) > maxStreamNameLen {
		return fmt.Errorf("%w: must be at most %d characters", ErrInvalidStreamName, maxStreamNameLen)
	}
	if !streamNameRE.MatchString(name) {
		return fmt.Errorf("%w: must be lowercase alphanumerics and single dashes, starting and ending with an alphanumeric", ErrInvalidStreamName)
	}
	return nil
}

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
	Ports             models.StreamPorts
}

// UpdateStreamInput is the input for StreamService.Update. The Gateway
// Template is immutable, so only the name and the claimed ports are updatable.
// A nil Ports leaves the claims (and the Gateway) untouched.
type UpdateStreamInput struct {
	Name  *string
	Ports *models.StreamPorts
}

// StreamStore is the slice of StreamRepository that StreamService uses.
// *repository.StreamRepository satisfies it structurally.
type StreamStore interface {
	Create(stream *models.Stream) error
	GetByID(id uuid.UUID) (*models.Stream, error)
	ListByProjectID(projectID uuid.UUID) ([]models.Stream, error)
	ListAll() ([]models.Stream, error)
	ExistsByName(projectID uuid.UUID, name string) (bool, error)
	Update(stream *models.Stream) error
	// UpdateStatus writes only the status columns of an existing row; it never
	// inserts, so a stream deleted concurrently is not resurrected.
	UpdateStatus(id uuid.UUID, status, message string) error
	Delete(id uuid.UUID) error
}

// StreamTemplateReader resolves a Gateway Template.
// repository.DomainTemplateRepositoryInterface satisfies it structurally.
type StreamTemplateReader interface {
	GetByID(id uuid.UUID) (*models.DomainTemplate, error)
}

// StreamNamespaceReader resolves a project's whitelisted namespace.
// repository.ProjectNamespaceRepositoryInterface satisfies it structurally.
type StreamNamespaceReader interface {
	GetByProjectAndNamespace(projectID uuid.UUID, namespace string) (*models.ProjectNamespace, error)
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
	namespaces   StreamNamespaceReader

	// Port sources for the claim and route-port checks; set via SetPortSources.
	portStore   StreamPortStore
	domainPorts DomainPortReader

	// capabilities gates stream creation on the cluster's Envoy Gateway
	// version; set via SetCapabilities. Optional: nil skips the guard.
	capabilities StreamCapabilityChecker
}

// ErrStreamsUnsupported is returned when stream creation is attempted on a
// cluster whose Envoy Gateway version is positively detected below 1.8.
var ErrStreamsUnsupported = errors.New("streams require Envoy Gateway >= 1.8")

// StreamCapabilityChecker reports whether the project's cluster supports a
// capability. *CapabilityService satisfies it.
type StreamCapabilityChecker interface {
	Has(ctx context.Context, projectID uuid.UUID, name string) bool
}

// SetCapabilities wires the capability checker post-construction (mirrors
// SetPortSources). Optional: when nil, the stream guard is skipped.
func (s *StreamService) SetCapabilities(c StreamCapabilityChecker) { s.capabilities = c }

// NewStreamService builds a StreamService. It panics if a dependency is nil,
// matching NewDomainService.
func NewStreamService(streamRepo StreamStore, templateRepo StreamTemplateReader, routeRepo StreamRouteCounter, k8sGateways GatewayApplier, namespaces StreamNamespaceReader) *StreamService {
	if streamRepo == nil || templateRepo == nil || routeRepo == nil || k8sGateways == nil || namespaces == nil {
		panic("services.NewStreamService: missing required dependency")
	}
	return &StreamService{streamRepo: streamRepo, templateRepo: templateRepo, routeRepo: routeRepo, k8sGateways: k8sGateways, namespaces: namespaces}
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
// copied from it. The claimed ports are validated against the template and
// persisted normalized, and the Gateway is then applied with one listener per
// claim; like DomainService.Create, an apply failure is recorded on the
// returned stream's status rather than returned as an error.
func (s *StreamService) Create(projectID uuid.UUID, in CreateStreamInput, user *models.User) (*models.Stream, error) {
	if s.capabilities != nil && !s.capabilities.Has(context.Background(), projectID, capabilities.CapStreams) {
		return nil, ErrStreamsUnsupported
	}
	tmpl, err := s.templateRepo.GetByID(in.GatewayTemplateID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrStreamTemplateNotFound
		}
		return nil, fmt.Errorf("failed to load gateway template: %w", err)
	}
	if tmpl.ProjectID != projectID {
		return nil, ErrStreamTemplateWrongProject
	}
	if _, _, ok := templatePortRange(tmpl); !ok {
		return nil, ErrTemplateNotStreamEnabled
	}

	// Streams deploy a Gateway into the namespace, so it must be one the
	// project manages (same rule as DomainService.Create).
	if in.Namespace != kubernetes.FastGatewayNamespace {
		ns, err := s.namespaces.GetByProjectAndNamespace(projectID, in.Namespace)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("%w: '%s'", ErrStreamNamespaceNotRegistered, in.Namespace)
			}
			return nil, fmt.Errorf("failed to validate namespace: %w", err)
		}
		if !ns.HasCapability(models.NamespaceCapabilityDeployGateway) {
			return nil, fmt.Errorf("%w: '%s' (missing capability '%s')", ErrStreamNamespaceNotDeployable, in.Namespace, models.NamespaceCapabilityDeployGateway)
		}
	}

	exists, err := s.streamRepo.ExistsByName(projectID, in.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to check stream name: %w", err)
	}
	if exists {
		return nil, ErrStreamNameTaken
	}

	ports, err := s.validateStreamPorts(tmpl, nil, in.Ports)
	if err != nil {
		return nil, err
	}

	stream := &models.Stream{
		ProjectID:         projectID,
		Name:              in.Name,
		Namespace:         in.Namespace,
		GatewayTemplateID: in.GatewayTemplateID,
		K8sGatewayName:    StreamGatewayName(in.Name),
		K8sGatewayClass:   tmpl.K8sGatewayClassName,
		Ports:             ports,
	}
	if user != nil {
		id := user.ID
		stream.CreatedBy = &id
	}
	if err := s.streamRepo.Create(stream); err != nil {
		return nil, mapStreamWriteError(err)
	}
	if err := s.ApplyGateway(context.Background(), stream); err != nil {
		log.Printf("Failed to apply Stream Gateway %s/%s: %v", stream.Namespace, stream.K8sGatewayName, err)
	}
	return stream, nil
}

// ApplyGateway makes the cluster match the stream's claims, then records the
// outcome on the stream (Status active, or error with the failure in
// StatusMessage).
//
// With claims, the Gateway (one listener per claim) is applied with
// UpdateGateway, which creates it when missing. With none there is nothing to
// listen on and a Gateway needs at least one listener, so the Gateway is
// deleted instead; one that is already gone counts as success.
//
// The GatewayConfig is built and handed to the applier entirely in-process:
// its L4 Listeners are json:"-" and would be lost if the config were
// serialized (e.g. queued as a job payload), silently degrading the Gateway
// to an HTTP listener. The applier renders it with kubernetes.BuildGatewayObject.
func (s *StreamService) ApplyGateway(ctx context.Context, stream *models.Stream) error {
	var applyErr error
	var message string
	if len(stream.Ports) == 0 {
		applyErr = s.k8sGateways.DeleteGateway(ctx, stream.ProjectID, stream.Namespace, stream.K8sGatewayName)
		if k8serrors.IsNotFound(applyErr) {
			applyErr = nil
		}
		message = "No ports claimed; no Gateway deployed"
	} else {
		cfg := streamplan.BuildStreamGatewayConfig(*stream)
		applyErr = s.k8sGateways.UpdateGateway(ctx, stream.ProjectID, &cfg)
		message = "Gateway applied successfully"
	}
	if applyErr != nil {
		stream.Status = StreamStatusError
		stream.StatusMessage = fmt.Sprintf("Failed to apply Gateway: %v", applyErr)
		err := fmt.Errorf("failed to apply stream gateway: %w", applyErr)
		// If the error status cannot be saved either, the row keeps its old
		// status; surface that alongside the apply failure so the caller logs both.
		if uerr := s.streamRepo.UpdateStatus(stream.ID, stream.Status, stream.StatusMessage); uerr != nil {
			err = errors.Join(err, fmt.Errorf("failed to record error status: %w", uerr))
		}
		return err
	}
	stream.Status = StreamStatusActive
	stream.StatusMessage = message
	if err := s.streamRepo.UpdateStatus(stream.ID, stream.Status, stream.StatusMessage); err != nil {
		return fmt.Errorf("failed to update stream status: %w", err)
	}
	return nil
}

// ReconcileGateways re-applies every stream's Gateway from its port claims.
// It runs once at startup so streams created before streams owned their
// Gateway (empty streams carried a placeholder listener) converge on the
// claim-built shape. Apply is idempotent, so a re-run is harmless. An error is
// returned when the streams cannot be listed or ctx is cancelled mid-run; a
// per-stream failure is logged and recorded on that stream's status, and the
// rest still proceed. Each stream is re-read just before it is applied, and
// only its status columns are written back.
func (s *StreamService) ReconcileGateways(ctx context.Context) error {
	listed, err := s.streamRepo.ListAll()
	if err != nil {
		return fmt.Errorf("failed to list streams: %w", err)
	}
	for _, snapshot := range listed {
		if err := ctx.Err(); err != nil {
			return err
		}
		// The listing can be stale by the time this stream's turn comes (the
		// router is already serving), so re-read it: apply from its current
		// claims, and skip it if it was deleted meanwhile.
		stream, err := s.streamRepo.GetByID(snapshot.ID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("stream gateway reconcile: stream %s (%s) was deleted; skipping", snapshot.ID, snapshot.Name)
			} else {
				log.Printf("stream gateway reconcile: stream %s (%s): failed to reload: %v", snapshot.ID, snapshot.Name, err)
			}
			continue
		}
		if err := s.ApplyGateway(ctx, stream); err != nil {
			log.Printf("stream gateway reconcile: stream %s (%s): %v", stream.ID, stream.Name, err)
		}
	}
	return ctx.Err()
}

// Update renames a stream and/or changes its claimed ports. The Gateway
// Template is immutable, and the Kubernetes Gateway name is fixed at creation
// (renaming it would orphan the deployed Gateway), so only the display name
// changes.
//
// A new claim set is validated against the template, must keep every port a
// route (or pending route approval) still uses, and is persisted normalized;
// the Gateway is then re-applied. As in Create, an apply failure is recorded
// on the returned stream's status rather than returned as an error. A
// name-only update never touches the cluster.
func (s *StreamService) Update(id uuid.UUID, in UpdateStreamInput) (*models.Stream, error) {
	stream, err := s.getStream(id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil && *in.Name != stream.Name {
		exists, err := s.streamRepo.ExistsByName(stream.ProjectID, *in.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to check stream name: %w", err)
		}
		if exists {
			return nil, ErrStreamNameTaken
		}
		stream.Name = *in.Name
	}
	if in.Ports != nil {
		tmpl, err := s.templateRepo.GetByID(stream.GatewayTemplateID)
		if err != nil {
			return nil, fmt.Errorf("failed to load gateway template: %w", err)
		}
		ports, err := s.validateStreamPorts(tmpl, &stream.ID, *in.Ports)
		if err != nil {
			return nil, err
		}
		if err := s.checkUsedClaimsKept(stream, ports); err != nil {
			return nil, err
		}
		stream.Ports = ports
	}
	if err := s.streamRepo.Update(stream); err != nil {
		return nil, mapStreamWriteError(err)
	}
	if in.Ports != nil {
		if err := s.ApplyGateway(context.Background(), stream); err != nil {
			log.Printf("Failed to apply Stream Gateway %s/%s: %v", stream.Namespace, stream.K8sGatewayName, err)
		}
	}
	return stream, nil
}

// Get returns a stream by ID, or ErrStreamNotFound.
func (s *StreamService) Get(id uuid.UUID) (*models.Stream, error) {
	return s.getStream(id)
}

// List returns all streams in a project, ordered by name.
func (s *StreamService) List(projectID uuid.UUID) ([]models.Stream, error) {
	return s.streamRepo.ListByProjectID(projectID)
}

// Delete removes a stream, refusing while it still has routes. Its Gateway is
// deleted first; a failure there is logged, not returned, so the row can still
// be removed (the Gateway is then an orphan an operator can clean up).
func (s *StreamService) Delete(id uuid.UUID) error {
	stream, err := s.getStream(id)
	if err != nil {
		return err
	}
	n, err := s.routeRepo.CountByStreamID(id)
	if err != nil {
		return fmt.Errorf("failed to count stream routes: %w", err)
	}
	if n > 0 {
		return ErrStreamHasRoutes
	}
	if err := s.k8sGateways.DeleteGateway(context.Background(), stream.ProjectID, stream.Namespace, stream.K8sGatewayName); err != nil && !k8serrors.IsNotFound(err) {
		log.Printf("Failed to delete Stream Gateway %s/%s: %v", stream.Namespace, stream.K8sGatewayName, err)
	}
	return s.streamRepo.Delete(id)
}

// mapStreamWriteError turns a unique-violation on (project_id, name) into
// ErrStreamNameTaken, covering the race between the ExistsByName pre-check and
// the write. It matches gorm.ErrDuplicatedKey (when gorm error translation is
// on) and Postgres SQLSTATE 23505 (pgconn.PgError implements SQLState()).
func mapStreamWriteError(err error) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrStreamNameTaken
	}
	var sqlErr interface{ SQLState() string }
	if errors.As(err, &sqlErr) && sqlErr.SQLState() == "23505" {
		return ErrStreamNameTaken
	}
	return err
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
