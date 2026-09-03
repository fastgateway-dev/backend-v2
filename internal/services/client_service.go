package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// ClientService handles client business logic
type ClientService struct {
	clientRepo           repository.ClientRepositoryInterface
	clientIPRepo         repository.ClientIPRepositoryInterface
	clientHeaderRepo     repository.ClientHeaderRepositoryInterface
	teamRepo             repository.TeamRepositoryInterface
	clientAttachmentRepo repository.ClientAttachmentRepositoryInterface
	routeRepo            repository.RouteRepositoryInterface

	// k8sSecrets deletes a client's mTLS CA Secret and k8sAPIKeys deletes its
	// API key Secret. Before Phase 2E Task 7 both were one field naming all
	// 58 cluster-client methods, of which this service calls two.
	k8sSecrets SecretWriter
	k8sAPIKeys APIKeySecretDeleter

	// state is the sole writer of route.Status. See route_state.go.
	// routeRepo is a constructor parameter, so state is built alongside it
	// in NewClientService.
	state *routeStateMachine
}

// ClientServiceDeps carries everything ClientService needs. Every field is
// required: before Phase 2E three of these arrived through setters, and a
// nil-guard existed to tolerate the ones that might not have been called
// (controller ruling R13, now resolved -- see cascadeToAttachedRoutes).
type ClientServiceDeps struct {
	ClientRepo           repository.ClientRepositoryInterface
	ClientIPRepo         repository.ClientIPRepositoryInterface
	ClientHeaderRepo     repository.ClientHeaderRepositoryInterface
	TeamRepo             repository.TeamRepositoryInterface
	ClientAttachmentRepo repository.ClientAttachmentRepositoryInterface
	RouteRepo            repository.RouteRepositoryInterface

	// K8sSecrets deletes a client's mTLS CA Secret; K8sAPIKeys deletes its
	// API key Secret. They replace SetKubernetesService, which handed over
	// all 58 cluster-client methods for these two calls. Required since
	// Task 9 deleted the two conditions that guarded them: whether a
	// client's Kubernetes material is cleaned up must depend on the client,
	// not on whether someone remembered to wire the field.
	K8sSecrets SecretWriter
	K8sAPIKeys APIKeySecretDeleter
}

// NewClientService builds a fully-wired ClientService. It panics if a
// required dependency is missing: before Phase 2E these arrived through
// setters after construction, so a forgotten wiring line degraded silently
// at runtime instead of failing at start-up. Master design section 6.6.
func NewClientService(deps ClientServiceDeps) *ClientService {
	var missing []string
	if deps.ClientRepo == nil {
		missing = append(missing, "ClientRepo")
	}
	if deps.ClientIPRepo == nil {
		missing = append(missing, "ClientIPRepo")
	}
	if deps.ClientHeaderRepo == nil {
		missing = append(missing, "ClientHeaderRepo")
	}
	if deps.TeamRepo == nil {
		missing = append(missing, "TeamRepo")
	}
	if deps.ClientAttachmentRepo == nil {
		missing = append(missing, "ClientAttachmentRepo")
	}
	if deps.RouteRepo == nil {
		missing = append(missing, "RouteRepo")
	}
	if deps.K8sSecrets == nil {
		missing = append(missing, "K8sSecrets")
	}
	if deps.K8sAPIKeys == nil {
		missing = append(missing, "K8sAPIKeys")
	}
	if len(missing) > 0 {
		panic("services.NewClientService: missing required dependency: " + strings.Join(missing, ", "))
	}

	svc := &ClientService{
		clientRepo:           deps.ClientRepo,
		clientIPRepo:         deps.ClientIPRepo,
		clientHeaderRepo:     deps.ClientHeaderRepo,
		teamRepo:             deps.TeamRepo,
		clientAttachmentRepo: deps.ClientAttachmentRepo,
		routeRepo:            deps.RouteRepo,
		k8sSecrets:           deps.K8sSecrets,
		k8sAPIKeys:           deps.K8sAPIKeys,
	}
	// routeRepo is already a constructor parameter, so the state machine
	// needs no setter of its own.
	svc.state = &routeStateMachine{repo: deps.RouteRepo}
	return svc
}

// CreateClientInput represents the input for creating a client
type CreateClientInput struct {
	Name               string    `json:"name" binding:"required"`
	Description        string    `json:"description"`
	TeamID             uuid.UUID `json:"teamId" binding:"required"`
	ContactName        string    `json:"contactName"`
	ContactEmail       string    `json:"contactEmail"`
	ClientIDHeaderName string    `json:"clientIdHeaderName"` // Header for client ID routing (default: x-client-id)
}

// UpdateClientInput represents the input for updating a client
type UpdateClientInput struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	ContactName        string `json:"contactName"`
	ContactEmail       string `json:"contactEmail"`
	ClientIDHeaderName string `json:"clientIdHeaderName"` // Header for client ID routing
}

// Create creates a new client
func (s *ClientService) Create(input *CreateClientInput, createdBy uuid.UUID) (*models.Client, error) {
	// Validate team exists
	_, err := s.teamRepo.GetByID(input.TeamID)
	if err != nil {
		return nil, errors.New("team not found")
	}

	// Check name uniqueness
	exists, err := s.clientRepo.ExistsByName(input.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("client name already exists")
	}

	// Set default client ID header name if not provided
	clientIDHeaderName := input.ClientIDHeaderName
	if clientIDHeaderName == "" {
		clientIDHeaderName = "x-client-id"
	}

	client := &models.Client{
		TeamID:             input.TeamID,
		Name:               input.Name,
		Description:        input.Description,
		ContactName:        input.ContactName,
		ContactEmail:       input.ContactEmail,
		ClientIDHeaderName: clientIDHeaderName,
		CreatedBy:          createdBy,
	}

	if err := s.clientRepo.Create(client); err != nil {
		return nil, err
	}

	// Reload with relationships
	return s.clientRepo.GetByID(client.ID)
}

// GetByID returns a client by ID
func (s *ClientService) GetByID(id uuid.UUID) (*models.Client, error) {
	return s.clientRepo.GetByID(id)
}

// Update updates a client
func (s *ClientService) Update(id uuid.UUID, input *UpdateClientInput) (*models.Client, error) {
	client, err := s.clientRepo.GetByID(id)
	if err != nil {
		return nil, errors.New("client not found")
	}

	// Check name uniqueness if name is being changed
	if input.Name != "" && input.Name != client.Name {
		exists, err := s.clientRepo.ExistsByNameExcluding(input.Name, id)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, errors.New("client name already exists")
		}
		client.Name = input.Name
	}

	if input.Description != "" || input.Description == "" {
		client.Description = input.Description
	}
	if input.ContactName != "" || input.ContactName == "" {
		client.ContactName = input.ContactName
	}
	if input.ContactEmail != "" || input.ContactEmail == "" {
		client.ContactEmail = input.ContactEmail
	}
	if input.ClientIDHeaderName != "" {
		client.ClientIDHeaderName = input.ClientIDHeaderName
	}

	if err := s.clientRepo.Update(client); err != nil {
		return nil, err
	}

	return s.clientRepo.GetByID(id)
}

// Delete deletes a client and cleans up associated K8s resources
func (s *ClientService) Delete(ctx context.Context, id uuid.UUID) error {
	client, err := s.clientRepo.GetByID(id)
	if err != nil {
		return errors.New("client not found")
	}

	// Clean up K8s secrets (API key + mTLS CA)
	// Derive project IDs from the client's team (not attachments, which may already be gone)
	if client.APIKeyEnabled || client.MTLSEnabled {
		projectIDs := make(map[uuid.UUID]bool)
		teamProjects, err := s.teamRepo.ListTeamProjects(client.TeamID)
		if err != nil {
			log.Printf("Warning: failed to list team projects for client %s cleanup: %v", id, err)
		}
		for _, tp := range teamProjects {
			if tp.ProjectID != uuid.Nil {
				projectIDs[tp.ProjectID] = true
			}
		}
		for pid := range projectIDs {
			if client.APIKeyEnabled {
				if err := s.k8sAPIKeys.DeleteAPIKeySecret(ctx, pid, id); err != nil {
					log.Printf("Warning: failed to delete API key secret for client %s in project %s: %v", id, pid, err)
				}
			}
			if client.MTLSEnabled && client.MTLSCASecret != "" {
				if err := s.k8sSecrets.DeleteSecret(ctx, pid, kubernetes.FastGatewayNamespace, client.MTLSCASecret); err != nil {
					log.Printf("Warning: failed to delete mTLS CA secret for client %s in project %s: %v", id, pid, err)
				}
			}
		}
	}

	return s.clientRepo.Delete(id)
}

// List returns paginated clients
func (s *ClientService) List(page, limit int, teamID *uuid.UUID) ([]models.Client, int64, error) {
	return s.clientRepo.List(page, limit, teamID)
}

// SetAllowedMethods sets the allowed HTTP methods for a client
func (s *ClientService) SetAllowedMethods(clientID uuid.UUID, methods []string) (*models.Client, error) {
	client, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	// Validate methods
	validMethods := map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true, "OPTIONS": true}
	for _, m := range methods {
		if !validMethods[strings.ToUpper(m)] {
			return nil, fmt.Errorf("invalid HTTP method: %s", m)
		}
	}

	// Normalize to uppercase and deduplicate
	seen := make(map[string]bool)
	var normalized []string
	for _, m := range methods {
		upper := strings.ToUpper(m)
		if !seen[upper] {
			seen[upper] = true
			normalized = append(normalized, upper)
		}
	}

	if len(normalized) == 0 {
		client.AllowedMethods = nil
	} else {
		client.AllowedMethods = models.StringList(normalized)
	}

	if err := s.clientRepo.Update(client); err != nil {
		return nil, err
	}

	// Cascade: mark affected routes as pending_deploy.
	// RETURN: the success value is the client record, already persisted and
	// re-readable with Get.
	if err := s.cascadeToAttachedRoutes(clientID, s.allAttachments,
		"client allowed methods changed"); err != nil {
		return nil, err
	}

	return s.clientRepo.GetByID(clientID)
}
