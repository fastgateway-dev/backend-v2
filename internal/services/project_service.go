package services

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/crypto"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

// ProjectService handles project business logic
type ProjectService struct {
	projectRepo        repository.ProjectRepositoryInterface
	approvalPolicyRepo repository.ApprovalPolicyRepositoryInterface
	presetRepo         repository.PresetRepositoryInterface
	config             *config.Config
	k8sService         KubernetesServiceInterface
}

// NewProjectService creates a new project service
func NewProjectService(projectRepo repository.ProjectRepositoryInterface, cfg *config.Config) *ProjectService {
	return &ProjectService{
		projectRepo: projectRepo,
		config:      cfg,
	}
}

// SetKubernetesService sets the Kubernetes service (used to avoid circular dependency)
func (s *ProjectService) SetKubernetesService(k8sService KubernetesServiceInterface) {
	s.k8sService = k8sService
}

// SetApprovalPolicyRepository sets the approval policy repository (used to avoid circular dependency)
func (s *ProjectService) SetApprovalPolicyRepository(repo repository.ApprovalPolicyRepositoryInterface) {
	s.approvalPolicyRepo = repo
}

// SetPresetRepository sets the preset repository (used to avoid circular dependency)
func (s *ProjectService) SetPresetRepository(repo repository.PresetRepositoryInterface) {
	s.presetRepo = repo
}

// ConnectionType constants
const (
	ConnectionTypeInCluster  = "in_cluster"
	ConnectionTypeKubeconfig = "kubeconfig"
	ConnectionTypeAPIToken   = "api_token"
)

// TLSVerification constants
const (
	TLSVerificationSystemCA = "system_ca"
	TLSVerificationCustomCA = "custom_ca"
	TLSVerificationSkip     = "skip"
)

// CreateProjectInput represents input for creating a project
type CreateProjectInput struct {
	Name           string `json:"name" binding:"required"`
	Description    string `json:"description"`
	ConnectionType string `json:"connectionType"`

	// For kubeconfig type
	Kubeconfig string `json:"kubeconfig"`

	// For api_token type (also backward compatible)
	K8sAPIURL       string        `json:"k8sApiUrl"`
	K8sToken        string        `json:"k8sToken"`
	TLSVerification string        `json:"tlsVerification"`
	K8sCACert       string        `json:"k8sCaCert"`
	Labels          models.Labels `json:"labels,omitempty"`
}

// UpdateProjectInput represents input for updating a project
type UpdateProjectInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`

	// Connection settings (kubeconfig/api_token only)
	Kubeconfig          string        `json:"kubeconfig"`
	K8sAPIURL           string        `json:"k8sApiUrl"`
	K8sToken            string        `json:"k8sToken"`
	TLSVerification     string        `json:"tlsVerification"`
	K8sCACert           string        `json:"k8sCaCert"`
	Labels              models.Labels `json:"labels,omitempty"`
	ApprovalEnabled     *bool         `json:"approvalEnabled,omitempty"`
	SelfApprovalAllowed *bool         `json:"selfApprovalAllowed,omitempty"`

	// Metrics (observability) — pointers so "omit" is distinguishable from "clear"
	MetricsEndpointURL   *string `json:"metricsEndpointUrl,omitempty"`
	MetricsAuthType      *string `json:"metricsAuthType,omitempty"` // "none" | "bearer" | "basic"
	MetricsUsername      *string `json:"metricsUsername,omitempty"`
	MetricsPassword      *string `json:"metricsPassword,omitempty"` // plaintext in, encrypted at rest
	MetricsToken         *string `json:"metricsToken,omitempty"`    // plaintext in, encrypted at rest
	MetricsTLSSkipVerify *bool   `json:"metricsTlsSkipVerify,omitempty"`
	MetricsCACert        *string `json:"metricsCaCert,omitempty"`
}

// Create creates a new project
func (s *ProjectService) Create(input *CreateProjectInput, createdBy uuid.UUID) (*models.Project, error) {
	// Default to api_token for backward compatibility
	connectionType := input.ConnectionType
	if connectionType == "" {
		connectionType = ConnectionTypeAPIToken
	}

	// Validate labels
	if input.Labels != nil {
		if err := models.ValidateLabels(input.Labels); err != nil {
			return nil, err
		}
	}

	// Validate connection type specific requirements
	project := &models.Project{
		Name:           input.Name,
		Description:    input.Description,
		ConnectionType: connectionType,
		CreatedBy:      createdBy,
		Labels:         input.Labels,
	}

	switch connectionType {
	case ConnectionTypeInCluster:
		if err := s.validateInCluster(); err != nil {
			return nil, err
		}
		// In-cluster uses pod's service account, no stored credentials needed
		project.K8sTLSSkipVerify = false

	case ConnectionTypeKubeconfig:
		if err := s.processKubeconfig(input, project); err != nil {
			return nil, err
		}

	case ConnectionTypeAPIToken:
		if err := s.processAPIToken(input, project); err != nil {
			return nil, err
		}

	default:
		return nil, errors.New("invalid connection type")
	}

	// Validate Kubernetes prerequisites (skip for in_cluster - will validate on first use)
	if s.k8sService != nil && connectionType != ConnectionTypeInCluster {
		ctx := context.Background()
		token := ""
		if project.K8sTokenEncrypted != "" {
			var err error
			token, err = s.decryptToken(project.K8sTokenEncrypted)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt token: %w", err)
			}
		}
		prereqCheck, err := s.k8sService.ValidatePrerequisites(ctx, project.K8sAPIURL, token)
		if err != nil {
			return nil, fmt.Errorf("failed to validate Kubernetes cluster: %w", err)
		}

		if !prereqCheck.NamespaceExists || !prereqCheck.GatewayCRDExists || !prereqCheck.HTTPRouteCRDExists {
			return nil, errors.New(prereqCheck.ErrorMessage)
		}
	}

	if err := s.projectRepo.Create(project); err != nil {
		return nil, err
	}

	// Add creator as admin
	if err := s.projectRepo.AddAdmin(project.ID, createdBy); err != nil {
		return nil, err
	}

	// Seed default approval policies
	if s.approvalPolicyRepo != nil {
		if err := s.approvalPolicyRepo.SeedDefaults(project.ID); err != nil {
			log.Printf("Warning: failed to seed approval policies: %v", err)
		}
	}

	// Seed built-in permission presets
	if s.presetRepo != nil {
		if err := s.presetRepo.SeedBuiltinPresets(project.ID); err != nil {
			log.Printf("Warning: failed to seed permission presets: %v", err)
		}
	}

	return project, nil
}

// validateInCluster validates in-cluster connection requirements
func (s *ProjectService) validateInCluster() error {
	// Check if we're running in Kubernetes
	if !IsRunningInCluster() {
		return errors.New("FastGateway is not running inside Kubernetes. Use Kubeconfig or API URL + Token instead")
	}

	// Check if an in-cluster project already exists
	existing, err := s.projectRepo.FindByConnectionType(ConnectionTypeInCluster)
	if err != nil {
		return fmt.Errorf("failed to check existing in-cluster project: %w", err)
	}
	if existing != nil {
		return errors.New("an in-cluster project already exists. Only one is allowed")
	}

	return nil
}

// processKubeconfig processes kubeconfig input and populates project fields
func (s *ProjectService) processKubeconfig(input *CreateProjectInput, project *models.Project) error {
	if input.Kubeconfig == "" {
		return errors.New("kubeconfig is required for kubeconfig connection type")
	}

	parsed, err := ParseKubeconfig(input.Kubeconfig)
	if err != nil {
		return err
	}

	project.K8sAPIURL = parsed.APIUrl
	project.K8sTLSSkipVerify = parsed.SkipTLS

	if len(parsed.CACert) > 0 {
		project.K8sCACert = string(parsed.CACert)
	}

	if parsed.Token != "" {
		encryptedToken, err := s.encryptToken(parsed.Token)
		if err != nil {
			return fmt.Errorf("failed to encrypt token: %w", err)
		}
		project.K8sTokenEncrypted = encryptedToken
	}

	if len(parsed.ClientCert) > 0 {
		project.K8sClientCert = string(parsed.ClientCert)
	}

	if len(parsed.ClientKey) > 0 {
		encryptedKey, err := s.encryptToken(string(parsed.ClientKey))
		if err != nil {
			return fmt.Errorf("failed to encrypt client key: %w", err)
		}
		project.K8sClientKeyEncrypted = encryptedKey
	}

	return nil
}

// processAPIToken processes API token input and populates project fields
func (s *ProjectService) processAPIToken(input *CreateProjectInput, project *models.Project) error {
	if input.K8sAPIURL == "" {
		return errors.New("k8sApiUrl is required for api_token connection type")
	}
	if input.K8sToken == "" {
		return errors.New("k8sToken is required for api_token connection type")
	}

	project.K8sAPIURL = input.K8sAPIURL

	// Encrypt the K8s token
	encryptedToken, err := s.encryptToken(input.K8sToken)
	if err != nil {
		return fmt.Errorf("failed to encrypt token: %w", err)
	}
	project.K8sTokenEncrypted = encryptedToken

	// Handle TLS verification
	switch input.TLSVerification {
	case TLSVerificationSkip, "":
		project.K8sTLSSkipVerify = true
	case TLSVerificationCustomCA:
		if input.K8sCACert == "" {
			return errors.New("k8sCaCert is required when tlsVerification is 'custom_ca'")
		}
		project.K8sTLSSkipVerify = false
		project.K8sCACert = input.K8sCACert
	case TLSVerificationSystemCA:
		project.K8sTLSSkipVerify = false
	default:
		return errors.New("invalid tlsVerification value")
	}

	return nil
}

// GetByID gets a project by ID
func (s *ProjectService) GetByID(id uuid.UUID) (*models.Project, error) {
	return s.projectRepo.GetByIDWithCounts(id)
}

// List lists projects with pagination
func (s *ProjectService) List(userID uuid.UUID, userRole models.UserRole, page, limit int, search string, labels map[string]string) ([]models.Project, int64, error) {
	return s.projectRepo.ListByUserAccess(userID, userRole, page, limit, search, labels)
}

// Update updates a project
func (s *ProjectService) Update(id uuid.UUID, input *UpdateProjectInput) (*models.Project, error) {
	project, err := s.projectRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Update name and description (always allowed)
	if input.Name != "" {
		project.Name = input.Name
	}
	if input.Description != "" {
		project.Description = input.Description
	}
	if input.Labels != nil {
		if err := models.ValidateLabels(input.Labels); err != nil {
			return nil, err
		}
		project.Labels = input.Labels
	}

	// Update approval settings
	if input.ApprovalEnabled != nil || input.SelfApprovalAllowed != nil {
		if input.ApprovalEnabled != nil {
			project.ApprovalEnabled = *input.ApprovalEnabled
		}
		if input.SelfApprovalAllowed != nil {
			project.SelfApprovalAllowed = *input.SelfApprovalAllowed
		}
	}

	// Check if trying to update connection settings for in_cluster project
	if project.ConnectionType == ConnectionTypeInCluster {
		hasConnectionUpdate := input.Kubeconfig != "" || input.K8sAPIURL != "" ||
			input.K8sToken != "" || input.TLSVerification != "" || input.K8sCACert != ""
		if hasConnectionUpdate {
			return nil, errors.New("in-cluster project connection settings cannot be modified")
		}
	} else if project.ConnectionType == ConnectionTypeKubeconfig {
		// Update kubeconfig if provided
		if input.Kubeconfig != "" {
			parsed, err := ParseKubeconfig(input.Kubeconfig)
			if err != nil {
				return nil, err
			}

			project.K8sAPIURL = parsed.APIUrl
			project.K8sTLSSkipVerify = parsed.SkipTLS

			if len(parsed.CACert) > 0 {
				project.K8sCACert = string(parsed.CACert)
			}

			if parsed.Token != "" {
				encryptedToken, err := s.encryptToken(parsed.Token)
				if err != nil {
					return nil, err
				}
				project.K8sTokenEncrypted = encryptedToken
			}

			if len(parsed.ClientCert) > 0 {
				project.K8sClientCert = string(parsed.ClientCert)
			}

			if len(parsed.ClientKey) > 0 {
				encryptedKey, err := s.encryptToken(string(parsed.ClientKey))
				if err != nil {
					return nil, err
				}
				project.K8sClientKeyEncrypted = encryptedKey
			}
		}
	} else {
		// api_token type - update individual fields
		if input.K8sAPIURL != "" {
			project.K8sAPIURL = input.K8sAPIURL
		}

		if input.K8sToken != "" {
			encryptedToken, err := s.encryptToken(input.K8sToken)
			if err != nil {
				return nil, err
			}
			project.K8sTokenEncrypted = encryptedToken
		}

		if input.TLSVerification != "" {
			switch input.TLSVerification {
			case TLSVerificationSkip:
				project.K8sTLSSkipVerify = true
				project.K8sCACert = ""
			case TLSVerificationCustomCA:
				if input.K8sCACert == "" && project.K8sCACert == "" {
					return nil, errors.New("k8sCaCert is required when tlsVerification is 'custom_ca'")
				}
				project.K8sTLSSkipVerify = false
				if input.K8sCACert != "" {
					project.K8sCACert = input.K8sCACert
				}
			case TLSVerificationSystemCA:
				project.K8sTLSSkipVerify = false
				project.K8sCACert = ""
			}
		}
	}

	// Metrics (observability) fields
	if input.MetricsEndpointURL != nil {
		project.MetricsEndpointURL = *input.MetricsEndpointURL
	}
	if input.MetricsAuthType != nil {
		switch *input.MetricsAuthType {
		case "none", "bearer", "basic":
			project.MetricsAuthType = *input.MetricsAuthType
		default:
			return nil, fmt.Errorf("invalid metricsAuthType: %q (allowed: none, bearer, basic)", *input.MetricsAuthType)
		}
	}
	if input.MetricsUsername != nil {
		project.MetricsUsername = *input.MetricsUsername
	}
	if input.MetricsToken != nil {
		if *input.MetricsToken == "" {
			project.MetricsTokenEncrypted = ""
		} else {
			enc, err := s.encryptToken(*input.MetricsToken)
			if err != nil {
				return nil, fmt.Errorf("encrypt metrics token: %w", err)
			}
			project.MetricsTokenEncrypted = enc
		}
	}
	if input.MetricsPassword != nil {
		if *input.MetricsPassword == "" {
			project.MetricsPasswordEncrypted = ""
		} else {
			enc, err := s.encryptToken(*input.MetricsPassword)
			if err != nil {
				return nil, fmt.Errorf("encrypt metrics password: %w", err)
			}
			project.MetricsPasswordEncrypted = enc
		}
	}
	if input.MetricsTLSSkipVerify != nil {
		project.MetricsTLSSkipVerify = *input.MetricsTLSSkipVerify
	}
	if input.MetricsCACert != nil {
		project.MetricsCACert = *input.MetricsCACert
	}

	if err := s.projectRepo.Update(project); err != nil {
		return nil, err
	}

	return project, nil
}

// Delete deletes a project
func (s *ProjectService) Delete(id uuid.UUID) error {
	return s.projectRepo.Delete(id)
}

// TestConnection tests the Kubernetes connection by making an actual API call
func (s *ProjectService) TestConnection(id uuid.UUID) (bool, string, string, error) {
	project, err := s.projectRepo.GetByID(id)
	if err != nil {
		return false, "Project not found", "", err
	}

	// Build rest.Config based on connection type
	var k8sConfig *rest.Config

	switch project.ConnectionType {
	case ConnectionTypeInCluster:
		if !IsRunningInCluster() {
			return false, "Not running in Kubernetes cluster", "", errors.New("not in cluster")
		}
		k8sConfig, err = rest.InClusterConfig()
		if err != nil {
			return false, "Failed to get in-cluster config", "", err
		}

	case ConnectionTypeKubeconfig, ConnectionTypeAPIToken, "":
		// Validate credentials exist
		if project.K8sTokenEncrypted == "" && project.K8sClientCert == "" {
			return false, "No credentials configured", "", errors.New("no credentials")
		}

		k8sConfig = &rest.Config{
			Host: project.K8sAPIURL,
		}

		// Auth: token or client cert
		if project.K8sTokenEncrypted != "" {
			token, err := s.decryptToken(project.K8sTokenEncrypted)
			if err != nil {
				return false, "Failed to decrypt token", "", err
			}
			if token == "" {
				return false, "Empty token", "", errors.New("empty token")
			}
			k8sConfig.BearerToken = token
		} else if project.K8sClientCert != "" {
			k8sConfig.TLSClientConfig.CertData = []byte(project.K8sClientCert)
			if project.K8sClientKeyEncrypted != "" {
				clientKey, err := s.decryptToken(project.K8sClientKeyEncrypted)
				if err != nil {
					return false, "Failed to decrypt client key", "", err
				}
				k8sConfig.TLSClientConfig.KeyData = []byte(clientKey)
			}
		}

		// TLS verification
		if project.K8sTLSSkipVerify {
			k8sConfig.TLSClientConfig.Insecure = true
		} else if project.K8sCACert != "" {
			k8sConfig.TLSClientConfig.CAData = []byte(project.K8sCACert)
		}
		// else: use system CA bundle (default behavior)

	default:
		return false, "Unknown connection type", "", fmt.Errorf("unknown connection type: %s", project.ConnectionType)
	}

	// Actually test the connection by getting server version
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(k8sConfig)
	if err != nil {
		return false, "Failed to create Kubernetes client", "", err
	}

	serverVersion, err := discoveryClient.ServerVersion()
	if err != nil {
		project.IsConnected = false
		_ = s.projectRepo.Update(project) // Best effort update
		return false, "Failed to connect to Kubernetes API", "", err
	}

	// Connection successful - update status
	project.IsConnected = true
	if err := s.projectRepo.Update(project); err != nil {
		return false, "Failed to update project status", "", err
	}

	return true, "Connection successful", serverVersion.GitVersion, nil
}

// GetDecryptedToken gets the decrypted K8s token for a project
func (s *ProjectService) GetDecryptedToken(id uuid.UUID) (string, error) {
	project, err := s.projectRepo.GetByID(id)
	if err != nil {
		return "", err
	}

	if project.K8sTokenEncrypted == "" {
		return "", nil
	}

	return s.decryptToken(project.K8sTokenEncrypted)
}

// GetDecryptedClientKey gets the decrypted K8s client key for a project
func (s *ProjectService) GetDecryptedClientKey(id uuid.UUID) (string, error) {
	project, err := s.projectRepo.GetByID(id)
	if err != nil {
		return "", err
	}

	if project.K8sClientKeyEncrypted == "" {
		return "", nil
	}

	return s.decryptToken(project.K8sClientKeyEncrypted)
}

// AddAdmin adds an admin to a project
func (s *ProjectService) AddAdmin(projectID, userID uuid.UUID) error {
	return s.projectRepo.AddAdmin(projectID, userID)
}

// RemoveAdmin removes an admin from a project
func (s *ProjectService) RemoveAdmin(projectID, userID uuid.UUID) error {
	return s.projectRepo.RemoveAdmin(projectID, userID)
}

// ListAdmins lists admins of a project
func (s *ProjectService) ListAdmins(projectID uuid.UUID) ([]models.User, error) {
	return s.projectRepo.ListAdmins(projectID)
}

// IsAdmin checks if a user is an admin of a project
func (s *ProjectService) IsAdmin(projectID, userID uuid.UUID) (bool, error) {
	return s.projectRepo.IsAdmin(projectID, userID)
}

func (s *ProjectService) encryptToken(plaintext string) (string, error) {
	return crypto.Encrypt(plaintext, s.config.EncryptionKey)
}

func (s *ProjectService) decryptToken(ciphertext string) (string, error) {
	return crypto.Decrypt(ciphertext, s.config.EncryptionKey)
}
