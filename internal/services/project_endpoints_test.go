package services_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// apiTokenProject is an api_token project with a stored (encrypted) token.
func apiTokenProject(id uuid.UUID) *models.Project {
	return &models.Project{
		ID:                id,
		Name:              "p",
		ConnectionType:    "api_token",
		K8sAPIURL:         "https://cluster.example:6443",
		K8sTokenEncrypted: "stored-encrypted-token",
	}
}

func strp(s string) *string { return &s }

// Changing the API URL without a new token would send the stored token to the
// new host on the next test-connection or deploy. It is rejected and nothing
// is saved.
func TestProjectService_Update_K8sAPIURLChange_WithoutToken_Rejected(t *testing.T) {
	svc, repo := newTestProjectService()
	id := uuid.New()
	repo.On("GetByID", id).Return(apiTokenProject(id), nil)

	_, err := svc.Update(id, &services.UpdateProjectInput{K8sAPIURL: "https://attacker.example:6443"})

	require.ErrorIs(t, err, services.ErrCredentialRebindRequired)
	repo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestProjectService_Update_K8sAPIURLChange_WithToken_ReplacesStoredToken(t *testing.T) {
	svc, repo := newTestProjectService()
	id := uuid.New()
	repo.On("GetByID", id).Return(apiTokenProject(id), nil)
	repo.On("Update", mock.MatchedBy(func(p *models.Project) bool {
		return p.K8sAPIURL == "https://new-cluster.example:6443" &&
			p.K8sTokenEncrypted != "" && p.K8sTokenEncrypted != "stored-encrypted-token"
	})).Return(nil)

	_, err := svc.Update(id, &services.UpdateProjectInput{
		K8sAPIURL: "https://new-cluster.example:6443",
		K8sToken:  "new-token",
	})

	require.NoError(t, err)
	repo.AssertExpectations(t)
}

// Re-sending the same URL (as a full-form save does) needs no token.
func TestProjectService_Update_K8sAPIURLUnchanged_KeepsStoredToken(t *testing.T) {
	svc, repo := newTestProjectService()
	id := uuid.New()
	repo.On("GetByID", id).Return(apiTokenProject(id), nil)
	repo.On("Update", mock.MatchedBy(func(p *models.Project) bool {
		return p.K8sTokenEncrypted == "stored-encrypted-token"
	})).Return(nil)

	_, err := svc.Update(id, &services.UpdateProjectInput{K8sAPIURL: "https://cluster.example:6443"})

	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestProjectService_Update_RejectsInvalidEndpointURLs(t *testing.T) {
	for _, bad := range []string{
		"file:///etc/passwd",
		"gopher://cluster.example:6443",
		"cluster.example:6443",
		"https://",
		"https://user:pass@cluster.example:6443",
	} {
		t.Run("k8s "+bad, func(t *testing.T) {
			svc, repo := newTestProjectService()
			id := uuid.New()
			repo.On("GetByID", id).Return(apiTokenProject(id), nil)

			_, err := svc.Update(id, &services.UpdateProjectInput{K8sAPIURL: bad, K8sToken: "t"})

			require.ErrorIs(t, err, services.ErrInvalidEndpointURL)
			repo.AssertNotCalled(t, "Update", mock.Anything)
		})
		t.Run("metrics "+bad, func(t *testing.T) {
			svc, repo := newTestProjectService()
			id := uuid.New()
			repo.On("GetByID", id).Return(&models.Project{ID: id, Name: "p"}, nil)

			_, err := svc.Update(id, &services.UpdateProjectInput{
				MetricsEndpointURL: strp(bad),
				MetricsAuthType:    strp("none"),
			})

			require.ErrorIs(t, err, services.ErrInvalidEndpointURL)
			repo.AssertNotCalled(t, "Update", mock.Anything)
		})
	}
}

func TestProjectService_Update_MetricsURLChange_Basic_WithoutPassword_Rejected(t *testing.T) {
	svc, repo := newTestProjectService()
	id := uuid.New()
	repo.On("GetByID", id).Return(&models.Project{
		ID: id, Name: "p",
		MetricsEndpointURL: "https://prom.example", MetricsAuthType: "basic",
		MetricsUsername: "u", MetricsPasswordEncrypted: "stored-encrypted-password",
	}, nil)

	_, err := svc.Update(id, &services.UpdateProjectInput{MetricsEndpointURL: strp("https://elsewhere.example")})

	require.ErrorIs(t, err, services.ErrCredentialRebindRequired)
	repo.AssertNotCalled(t, "Update", mock.Anything)
}

// With a new token the move is allowed, and the old endpoint's secrets do not
// survive it (the stored basic password is cleared).
func TestProjectService_Update_MetricsURLChange_WithToken_ClearsOldSecrets(t *testing.T) {
	svc, repo := newTestProjectService()
	id := uuid.New()
	repo.On("GetByID", id).Return(&models.Project{
		ID: id, Name: "p",
		MetricsEndpointURL: "https://prom.example", MetricsAuthType: "bearer",
		MetricsTokenEncrypted: "stored-encrypted-token", MetricsPasswordEncrypted: "stale-password",
	}, nil)
	repo.On("Update", mock.MatchedBy(func(p *models.Project) bool {
		return p.MetricsEndpointURL == "https://prom2.example" &&
			p.MetricsTokenEncrypted != "" && p.MetricsTokenEncrypted != "stored-encrypted-token" &&
			p.MetricsPasswordEncrypted == ""
	})).Return(nil)

	_, err := svc.Update(id, &services.UpdateProjectInput{
		MetricsEndpointURL: strp("https://prom2.example"),
		MetricsToken:       strp("new-token"),
	})

	require.NoError(t, err)
	repo.AssertExpectations(t)
}

// Switching to an endpoint without auth needs no secret, and the stored ones
// are dropped rather than kept for the new host.
func TestProjectService_Update_MetricsURLChange_AuthNone_ClearsSecrets(t *testing.T) {
	svc, repo := newTestProjectService()
	id := uuid.New()
	repo.On("GetByID", id).Return(&models.Project{
		ID: id, Name: "p",
		MetricsEndpointURL: "https://prom.example", MetricsAuthType: "bearer",
		MetricsTokenEncrypted: "stored-encrypted-token",
	}, nil)
	repo.On("Update", mock.MatchedBy(func(p *models.Project) bool {
		return p.MetricsAuthType == "none" && p.MetricsTokenEncrypted == "" && p.MetricsPasswordEncrypted == ""
	})).Return(nil)

	_, err := svc.Update(id, &services.UpdateProjectInput{
		MetricsEndpointURL: strp("http://prom.internal:9090"),
		MetricsAuthType:    strp("none"),
	})

	require.NoError(t, err)
	repo.AssertExpectations(t)
}

const tokenOnlyKubeconfig = `apiVersion: v1
kind: Config
current-context: c
clusters:
- name: k
  cluster:
    server: https://new-cluster.example:6443
contexts:
- name: c
  context:
    cluster: k
    user: u
users:
- name: u
  user:
    token: new-kubeconfig-token
`

// A kubeconfig for a different server replaces the stored credentials; the
// old server's client cert, key and CA are not kept for the new one.
func TestProjectService_Update_KubeconfigNewServer_ClearsOldCredentials(t *testing.T) {
	svc, repo := newTestProjectService()
	id := uuid.New()
	repo.On("GetByID", id).Return(&models.Project{
		ID: id, Name: "p", ConnectionType: "kubeconfig",
		K8sAPIURL:             "https://old-cluster.example:6443",
		K8sClientCert:         "old-client-cert",
		K8sClientKeyEncrypted: "old-encrypted-key",
		K8sCACert:             "old-ca",
	}, nil)
	var saved *models.Project
	repo.On("Update", mock.AnythingOfType("*models.Project")).Run(func(args mock.Arguments) {
		saved = args.Get(0).(*models.Project)
	}).Return(nil)

	_, err := svc.Update(id, &services.UpdateProjectInput{Kubeconfig: tokenOnlyKubeconfig})

	require.NoError(t, err)
	require.NotNil(t, saved)
	assert.Equal(t, "https://new-cluster.example:6443", saved.K8sAPIURL)
	assert.NotEmpty(t, saved.K8sTokenEncrypted)
	assert.Empty(t, saved.K8sClientCert)
	assert.Empty(t, saved.K8sClientKeyEncrypted)
	assert.Empty(t, saved.K8sCACert)
}
