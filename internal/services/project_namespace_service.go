package services

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// ProjectNamespaceService handles project namespace business logic
type ProjectNamespaceService struct {
	nsRepo      repository.ProjectNamespaceRepositoryInterface
	projectRepo repository.ProjectRepositoryInterface
	domainRepo  repository.DomainRepositoryInterface
	k8sService  KubernetesServiceInterface
}

// NewProjectNamespaceService creates a new project namespace service
func NewProjectNamespaceService(nsRepo repository.ProjectNamespaceRepositoryInterface, projectRepo repository.ProjectRepositoryInterface, domainRepo repository.DomainRepositoryInterface, k8sService KubernetesServiceInterface) *ProjectNamespaceService {
	return &ProjectNamespaceService{
		nsRepo:      nsRepo,
		projectRepo: projectRepo,
		domainRepo:  domainRepo,
		k8sService:  k8sService,
	}
}

// getDomainNamespaces returns unique namespaces used by domains in a project.
// Always includes FastGatewayNamespace.
func (s *ProjectNamespaceService) getDomainNamespaces(projectID uuid.UUID) []string {
	domains, _, err := s.domainRepo.ListByProjectID(projectID, 1, 10000, "", "", nil)
	if err != nil {
		log.Printf("Failed to list domains for ReferenceGrant sync: %v", err)
		return []string{FastGatewayNamespace}
	}

	seen := map[string]bool{FastGatewayNamespace: true}
	namespaces := []string{FastGatewayNamespace}
	for _, d := range domains {
		if !seen[d.Namespace] {
			namespaces = append(namespaces, d.Namespace)
			seen[d.Namespace] = true
		}
	}
	return namespaces
}

// CreateProjectNamespaceInput represents input for adding a namespace to a project
type CreateProjectNamespaceInput struct {
	Namespace    string   `json:"namespace" binding:"required"`
	Capabilities []string `json:"capabilities" binding:"required"`
}

// UpdateProjectNamespaceInput represents input for updating a project namespace
type UpdateProjectNamespaceInput struct {
	Capabilities []string `json:"capabilities" binding:"required"`
}

// validateCapabilities returns a deduped list or an error if any capability is unknown.
func validateCapabilities(caps []string) ([]string, error) {
	if len(caps) == 0 {
		return nil, errors.New("at least one capability is required")
	}
	allowed := map[string]bool{}
	for _, c := range models.AllowedNamespaceCapabilities {
		allowed[c] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		if !allowed[c] {
			return nil, fmt.Errorf("unknown capability '%s'", c)
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out, nil
}

// generateReferenceGrantName generates a unique ReferenceGrant name for a project namespace
func generateReferenceGrantName(projectID uuid.UUID, namespace string) string {
	// Use a prefix + short project ID + namespace to make it unique
	shortID := projectID.String()[:8]
	return fmt.Sprintf("fastgateway-%s-%s", shortID, namespace)
}

// Create adds a namespace to a project and creates the corresponding ReferenceGrant
func (s *ProjectNamespaceService) Create(projectID uuid.UUID, input *CreateProjectNamespaceInput) (*models.ProjectNamespace, error) {
	// Validate project exists
	project, err := s.projectRepo.GetByID(projectID)
	if err != nil {
		return nil, errors.New("project not found")
	}

	caps, err := validateCapabilities(input.Capabilities)
	if err != nil {
		return nil, err
	}

	// Check if namespace already exists for this project
	exists, err := s.nsRepo.ExistsByProjectAndNamespace(projectID, input.Namespace)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("namespace already added to this project")
	}

	// Validate that the namespace exists in the Kubernetes cluster
	ctx := context.Background()
	namespaces, err := s.k8sService.ListNamespaces(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to validate namespace: %w", err)
	}

	namespaceExists := false
	for _, ns := range namespaces {
		if ns == input.Namespace {
			namespaceExists = true
			break
		}
	}

	if !namespaceExists {
		return nil, fmt.Errorf("namespace '%s' does not exist in the Kubernetes cluster", input.Namespace)
	}

	// Create project namespace record
	ns := &models.ProjectNamespace{
		ProjectID:             projectID,
		Namespace:             input.Namespace,
		Capabilities:          caps,
		ReferenceGrantCreated: false,
	}

	if err := s.nsRepo.Create(ns); err != nil {
		return nil, err
	}

	// Create ReferenceGrant only if the namespace can be referenced into.
	toKinds := models.ReferenceGrantKindsForCapabilities(caps)
	if len(toKinds) > 0 {
		rgConfig := &ReferenceGrantConfig{
			Name:           generateReferenceGrantName(projectID, input.Namespace),
			FromNamespaces: s.getDomainNamespaces(projectID),
			ToNamespace:    input.Namespace,
			ToKinds:        toKinds,
		}

		if err := s.k8sService.CreateReferenceGrant(ctx, project.ID, rgConfig); err != nil {
			log.Printf("Failed to create ReferenceGrant in Kubernetes: %v", err)
			return ns, nil
		}

		ns.ReferenceGrantCreated = true
		if err := s.nsRepo.Update(ns); err != nil {
			log.Printf("Failed to update project namespace record: %v", err)
		}
	}

	return ns, nil
}

// Update changes the capabilities of an existing project namespace and re-syncs
// the ReferenceGrant accordingly.
func (s *ProjectNamespaceService) Update(id uuid.UUID, input *UpdateProjectNamespaceInput) (*models.ProjectNamespace, error) {
	ns, err := s.nsRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	caps, err := validateCapabilities(input.Capabilities)
	if err != nil {
		return nil, err
	}
	ns.Capabilities = caps

	ctx := context.Background()
	rgName := generateReferenceGrantName(ns.ProjectID, ns.Namespace)
	toKinds := models.ReferenceGrantKindsForCapabilities(caps)

	if len(toKinds) == 0 {
		// No referenceable kinds; tear down any existing RG.
		if ns.ReferenceGrantCreated {
			_ = s.k8sService.DeleteReferenceGrant(ctx, ns.ProjectID, ns.Namespace, rgName)
			ns.ReferenceGrantCreated = false
		}
	} else {
		rgConfig := &ReferenceGrantConfig{
			Name:           rgName,
			FromNamespaces: s.getDomainNamespaces(ns.ProjectID),
			ToNamespace:    ns.Namespace,
			ToKinds:        toKinds,
		}
		if err := s.k8sService.RecreateReferenceGrant(ctx, ns.ProjectID, rgConfig); err != nil {
			log.Printf("Failed to recreate ReferenceGrant: %v", err)
			ns.ReferenceGrantCreated = false
		} else {
			ns.ReferenceGrantCreated = true
		}
	}

	if err := s.nsRepo.Update(ns); err != nil {
		return nil, err
	}
	return ns, nil
}

// GetByID gets a project namespace by ID
func (s *ProjectNamespaceService) GetByID(id uuid.UUID) (*models.ProjectNamespace, error) {
	return s.nsRepo.GetByID(id)
}

// GetByProjectAndNamespace gets a project namespace by project ID and namespace name
func (s *ProjectNamespaceService) GetByProjectAndNamespace(projectID uuid.UUID, namespace string) (*models.ProjectNamespace, error) {
	return s.nsRepo.GetByProjectAndNamespace(projectID, namespace)
}

// ListByProjectID lists all namespaces for a project
func (s *ProjectNamespaceService) ListByProjectID(projectID uuid.UUID) ([]models.ProjectNamespace, error) {
	return s.nsRepo.ListByProjectID(projectID)
}

// ListByCapability lists project namespaces for a project that have the given capability.
func (s *ProjectNamespaceService) ListByCapability(projectID uuid.UUID, capability string) ([]models.ProjectNamespace, error) {
	return s.nsRepo.ListByCapability(projectID, capability)
}

// Delete removes a namespace from a project and deletes the corresponding ReferenceGrant
func (s *ProjectNamespaceService) Delete(id uuid.UUID) error {
	ns, err := s.nsRepo.GetByID(id)
	if err != nil {
		return err
	}

	// Delete ReferenceGrant from Kubernetes if it was created
	if ns.ReferenceGrantCreated {
		ctx := context.Background()
		rgName := generateReferenceGrantName(ns.ProjectID, ns.Namespace)
		if err := s.k8sService.DeleteReferenceGrant(ctx, ns.ProjectID, ns.Namespace, rgName); err != nil {
			log.Printf("Failed to delete ReferenceGrant from Kubernetes: %v", err)
			// Continue with database deletion even if K8s deletion fails
		}
	}

	return s.nsRepo.Delete(id)
}

// IsNamespaceManaged checks if a namespace is managed by a project
func (s *ProjectNamespaceService) IsNamespaceManaged(projectID uuid.UUID, namespace string) (bool, error) {
	// The gateway namespace (fastgateway-system) is always implicitly managed
	if namespace == FastGatewayNamespace || namespace == "" {
		return true, nil
	}
	return s.nsRepo.ExistsByProjectAndNamespace(projectID, namespace)
}

// EnsureReferenceGrant ensures the ReferenceGrant exists for a project namespace
// (or that no stale RG remains when capabilities require none).
func (s *ProjectNamespaceService) EnsureReferenceGrant(id uuid.UUID) error {
	ns, err := s.nsRepo.GetByID(id)
	if err != nil {
		return err
	}

	ctx := context.Background()
	rgName := generateReferenceGrantName(ns.ProjectID, ns.Namespace)
	toKinds := models.ReferenceGrantKindsForCapabilities(ns.Capabilities)

	if len(toKinds) == 0 {
		// Capabilities require no RG; clean up any stale one.
		_ = s.k8sService.DeleteReferenceGrant(ctx, ns.ProjectID, ns.Namespace, rgName)
		if ns.ReferenceGrantCreated {
			ns.ReferenceGrantCreated = false
			return s.nsRepo.Update(ns)
		}
		return nil
	}

	exists, err := s.k8sService.ReferenceGrantExists(ctx, ns.ProjectID, ns.Namespace, rgName)
	if err != nil {
		return fmt.Errorf("failed to check ReferenceGrant existence: %w", err)
	}

	if exists {
		if !ns.ReferenceGrantCreated {
			ns.ReferenceGrantCreated = true
			return s.nsRepo.Update(ns)
		}
		return nil
	}

	rgConfig := &ReferenceGrantConfig{
		Name:           rgName,
		FromNamespaces: s.getDomainNamespaces(ns.ProjectID),
		ToNamespace:    ns.Namespace,
		ToKinds:        toKinds,
	}

	if err := s.k8sService.CreateReferenceGrant(ctx, ns.ProjectID, rgConfig); err != nil {
		return fmt.Errorf("failed to create ReferenceGrant: %w", err)
	}

	ns.ReferenceGrantCreated = true
	return s.nsRepo.Update(ns)
}
