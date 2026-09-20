package clients_test

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services/clients"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// fakeCAReader is a hand-written stand-in for clients.CAReader -- the real
// implementation (*cluster.ControlPlaneClient) talks to a dynamic
// Kubernetes client, which these unit tests don't stand up. It serves a
// single canned Secret-shaped unstructured object keyed by name, mirroring
// how a real core/v1 Secret's "data" field holds base64-encoded strings.
type fakeCAReader struct {
	secrets map[string]*unstructured.Unstructured
	err     error
}

func newFakeCASecret(name string, data map[string]string) *unstructured.Unstructured {
	encoded := make(map[string]interface{}, len(data))
	for k, v := range data {
		encoded[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name": name,
			},
			"data": encoded,
		},
	}
}

func (f *fakeCAReader) Get(_ context.Context, _ schema.GroupVersionResource, name string, _ bool) (*unstructured.Unstructured, error) {
	if f.err != nil {
		return nil, f.err
	}
	obj, ok := f.secrets[name]
	if !ok {
		return nil, errors.New("secret not found: " + name)
	}
	return obj, nil
}

// testDeps bundles the mocks + fake CAReader for one test, plus the
// service under test built from them.
type testDeps struct {
	clientRepo *mocks.MockClientRepository
	certRepo   *mocks.MockManagedCertificateRepository
	issuerRepo *mocks.MockCertificateIssuerRepository
	teamRepo   *mocks.MockTeamRepository
	caReader   *fakeCAReader
	svc        *clients.ClientCertificateService
}

func newTestDeps() *testDeps {
	d := &testDeps{
		clientRepo: new(mocks.MockClientRepository),
		certRepo:   new(mocks.MockManagedCertificateRepository),
		issuerRepo: new(mocks.MockCertificateIssuerRepository),
		teamRepo:   new(mocks.MockTeamRepository),
		caReader:   &fakeCAReader{secrets: map[string]*unstructured.Unstructured{}},
	}
	d.svc = clients.NewClientCertificateService(clients.ClientCertificateServiceDeps{
		ClientRepo: d.clientRepo,
		CertRepo:   d.certRepo,
		IssuerRepo: d.issuerRepo,
		TeamRepo:   d.teamRepo,
		CAReader:   d.caReader,
	})
	return d
}

func readyClientCert(id, projectID, issuerID uuid.UUID) *models.ManagedCertificate {
	return &models.ManagedCertificate{
		ID:        id,
		ProjectID: projectID,
		IssuerID:  issuerID,
		Name:      "client-cert-1",
		Usage:     models.ManagedCertUsageClient,
		Status:    models.ManagedCertStatusReady,
		Config: models.ManagedCertConfig{
			DNSNames: []string{"client1.example.com"},
			URISANs:  []string{"spiffe://example/client1"},
		},
	}
}

func caIssuer(id uuid.UUID, secretName string) *models.CertificateIssuer {
	return &models.CertificateIssuer{
		ID:   id,
		Name: "self-signed-ca",
		Type: models.IssuerTypeSelfSignedCA,
		Config: models.IssuerConfig{
			CASecretName: secretName,
		},
	}
}

func TestClientCertificateService_AttachCertificate_HappyPath(t *testing.T) {
	d := newTestDeps()

	clientID := uuid.New()
	certID := uuid.New()
	teamID := uuid.New()
	projectID := uuid.New()
	issuerID := uuid.New()
	actingUser := uuid.New()

	cert := readyClientCert(certID, projectID, issuerID)
	client := &models.Client{ID: clientID, TeamID: teamID, Name: "acme-client"}
	issuer := caIssuer(issuerID, "ca-issuer-secret")
	d.caReader.secrets["ca-issuer-secret"] = newFakeCASecret("ca-issuer-secret", map[string]string{
		"ca.crt": "fake-ca-pem-bytes",
	})

	d.certRepo.On("GetByID", certID).Return(cert, nil)
	d.clientRepo.On("GetByID", clientID).Return(client, nil)
	d.teamRepo.On("ListTeamProjects", teamID).Return([]models.ProjectTeamRole{{ProjectID: projectID, TeamID: teamID}}, nil)
	d.clientRepo.On("GetByManagedCertificateID", certID).Return(nil, gorm.ErrRecordNotFound)
	d.issuerRepo.On("GetByID", issuerID).Return(issuer, nil)
	d.clientRepo.On("Update", mock.MatchedBy(func(c *models.Client) bool { return c.ID == clientID })).Return(nil)

	got, err := d.svc.AttachCertificate(context.Background(), clientID, certID, actingUser)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.True(t, got.MTLSEnabled)
	assert.Equal(t, "client-cert-1", got.MTLSCAName)
	assert.Equal(t, "fastgateway-client-"+clientID.String()[:8]+"-mtls-ca", got.MTLSCASecret)
	assert.Equal(t, "ca.crt", got.MTLSCASecretKey)
	assert.Equal(t, "fake-ca-pem-bytes", got.MTLSCAPem)
	assert.Equal(t, models.MTLSSANList{
		{Type: "DNS", Value: "client1.example.com"},
		{Type: "URI", Value: "spiffe://example/client1"},
	}, got.MTLSSANs)
	assert.Nil(t, got.MTLSHashes)
	require.NotNil(t, got.ManagedCertificateID)
	assert.Equal(t, certID, *got.ManagedCertificateID)
	require.NotNil(t, got.MTLSCreatedBy)
	assert.Equal(t, actingUser, *got.MTLSCreatedBy)
	require.NotNil(t, got.MTLSCreatedAt)
	assert.WithinDuration(t, time.Now(), *got.MTLSCreatedAt, 5*time.Second)

	d.clientRepo.AssertExpectations(t)
	d.certRepo.AssertExpectations(t)
	d.issuerRepo.AssertExpectations(t)
	d.teamRepo.AssertExpectations(t)
}

func TestClientCertificateService_AttachCertificate_FallsBackToTLSCrt(t *testing.T) {
	d := newTestDeps()

	clientID := uuid.New()
	certID := uuid.New()
	teamID := uuid.New()
	projectID := uuid.New()
	issuerID := uuid.New()

	cert := readyClientCert(certID, projectID, issuerID)
	client := &models.Client{ID: clientID, TeamID: teamID}
	issuer := caIssuer(issuerID, "ca-issuer-secret")
	d.caReader.secrets["ca-issuer-secret"] = newFakeCASecret("ca-issuer-secret", map[string]string{
		"tls.crt": "fallback-ca-pem",
	})

	d.certRepo.On("GetByID", certID).Return(cert, nil)
	d.clientRepo.On("GetByID", clientID).Return(client, nil)
	d.teamRepo.On("ListTeamProjects", teamID).Return([]models.ProjectTeamRole{{ProjectID: projectID}}, nil)
	d.clientRepo.On("GetByManagedCertificateID", certID).Return(nil, gorm.ErrRecordNotFound)
	d.issuerRepo.On("GetByID", issuerID).Return(issuer, nil)
	d.clientRepo.On("Update", mock.Anything).Return(nil)

	got, err := d.svc.AttachCertificate(context.Background(), clientID, certID, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, "fallback-ca-pem", got.MTLSCAPem)
}

func TestClientCertificateService_AttachCertificate_WrongUsage(t *testing.T) {
	d := newTestDeps()
	certID := uuid.New()
	cert := readyClientCert(certID, uuid.New(), uuid.New())
	cert.Usage = models.ManagedCertUsageServer
	d.certRepo.On("GetByID", certID).Return(cert, nil)

	_, err := d.svc.AttachCertificate(context.Background(), uuid.New(), certID, uuid.New())
	assert.ErrorIs(t, err, clients.ErrNotClientCert)

	d.clientRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestClientCertificateService_AttachCertificate_NotReady(t *testing.T) {
	d := newTestDeps()
	certID := uuid.New()
	cert := readyClientCert(certID, uuid.New(), uuid.New())
	cert.Status = models.ManagedCertStatusIssuing
	d.certRepo.On("GetByID", certID).Return(cert, nil)

	_, err := d.svc.AttachCertificate(context.Background(), uuid.New(), certID, uuid.New())
	assert.ErrorIs(t, err, clients.ErrCertNotReadyForAttach)

	d.clientRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestClientCertificateService_AttachCertificate_ProjectNotAccessible(t *testing.T) {
	d := newTestDeps()
	clientID := uuid.New()
	certID := uuid.New()
	teamID := uuid.New()
	projectID := uuid.New()
	cert := readyClientCert(certID, projectID, uuid.New())
	client := &models.Client{ID: clientID, TeamID: teamID}

	d.certRepo.On("GetByID", certID).Return(cert, nil)
	d.clientRepo.On("GetByID", clientID).Return(client, nil)
	// The team has roles, but none in the certificate's project.
	d.teamRepo.On("ListTeamProjects", teamID).Return([]models.ProjectTeamRole{{ProjectID: uuid.New()}}, nil)

	_, err := d.svc.AttachCertificate(context.Background(), clientID, certID, uuid.New())
	assert.ErrorIs(t, err, clients.ErrCertProjectNotAccessible)

	d.clientRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestClientCertificateService_AttachCertificate_AlreadyAttachedToAnotherClient(t *testing.T) {
	d := newTestDeps()
	clientID := uuid.New()
	otherClientID := uuid.New()
	certID := uuid.New()
	teamID := uuid.New()
	projectID := uuid.New()
	cert := readyClientCert(certID, projectID, uuid.New())
	client := &models.Client{ID: clientID, TeamID: teamID}

	d.certRepo.On("GetByID", certID).Return(cert, nil)
	d.clientRepo.On("GetByID", clientID).Return(client, nil)
	d.teamRepo.On("ListTeamProjects", teamID).Return([]models.ProjectTeamRole{{ProjectID: projectID}}, nil)
	d.clientRepo.On("GetByManagedCertificateID", certID).Return(&models.Client{ID: otherClientID}, nil)

	_, err := d.svc.AttachCertificate(context.Background(), clientID, certID, uuid.New())
	assert.ErrorIs(t, err, clients.ErrCertAlreadyAttached)

	d.clientRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestClientCertificateService_AttachCertificate_AlreadyAttachedToSameClientIsNotAnError(t *testing.T) {
	// Re-attaching a cert to the very same client it's already attached to
	// should not trip ErrCertAlreadyAttached -- only client.ManagedCertificateID
	// being non-nil (checked next) governs that case, and here the client's
	// own FK is nil (simulating a state where GetByManagedCertificateID's
	// index found a row the client's own FK read hasn't caught up to would
	// be unusual, but the same-ID branch must not itself error).
	d := newTestDeps()
	clientID := uuid.New()
	certID := uuid.New()
	teamID := uuid.New()
	projectID := uuid.New()
	issuerID := uuid.New()
	cert := readyClientCert(certID, projectID, issuerID)
	client := &models.Client{ID: clientID, TeamID: teamID}
	issuer := caIssuer(issuerID, "ca-secret")
	d.caReader.secrets["ca-secret"] = newFakeCASecret("ca-secret", map[string]string{"ca.crt": "pem"})

	d.certRepo.On("GetByID", certID).Return(cert, nil)
	d.clientRepo.On("GetByID", clientID).Return(client, nil)
	d.teamRepo.On("ListTeamProjects", teamID).Return([]models.ProjectTeamRole{{ProjectID: projectID}}, nil)
	d.clientRepo.On("GetByManagedCertificateID", certID).Return(&models.Client{ID: clientID}, nil)
	d.issuerRepo.On("GetByID", issuerID).Return(issuer, nil)
	d.clientRepo.On("Update", mock.Anything).Return(nil)

	_, err := d.svc.AttachCertificate(context.Background(), clientID, certID, uuid.New())
	assert.NoError(t, err)
}

func TestClientCertificateService_AttachCertificate_ClientAlreadyHasManagedCert(t *testing.T) {
	d := newTestDeps()
	clientID := uuid.New()
	certID := uuid.New()
	teamID := uuid.New()
	projectID := uuid.New()
	existingCertID := uuid.New()
	cert := readyClientCert(certID, projectID, uuid.New())
	client := &models.Client{ID: clientID, TeamID: teamID, ManagedCertificateID: &existingCertID}

	d.certRepo.On("GetByID", certID).Return(cert, nil)
	d.clientRepo.On("GetByID", clientID).Return(client, nil)
	d.teamRepo.On("ListTeamProjects", teamID).Return([]models.ProjectTeamRole{{ProjectID: projectID}}, nil)
	d.clientRepo.On("GetByManagedCertificateID", certID).Return(nil, gorm.ErrRecordNotFound)

	_, err := d.svc.AttachCertificate(context.Background(), clientID, certID, uuid.New())
	assert.ErrorIs(t, err, clients.ErrClientHasManagedCert)

	d.clientRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestClientCertificateService_AttachCertificate_CertNotFound(t *testing.T) {
	d := newTestDeps()
	certID := uuid.New()
	d.certRepo.On("GetByID", certID).Return(nil, gorm.ErrRecordNotFound)

	_, err := d.svc.AttachCertificate(context.Background(), uuid.New(), certID, uuid.New())
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	d.clientRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestClientCertificateService_AttachCertificate_ClientNotFound(t *testing.T) {
	d := newTestDeps()
	clientID := uuid.New()
	certID := uuid.New()
	cert := readyClientCert(certID, uuid.New(), uuid.New())
	d.certRepo.On("GetByID", certID).Return(cert, nil)
	d.clientRepo.On("GetByID", clientID).Return(nil, gorm.ErrRecordNotFound)

	_, err := d.svc.AttachCertificate(context.Background(), clientID, certID, uuid.New())
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	d.clientRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestClientCertificateService_DetachCertificate_ClearsFields(t *testing.T) {
	d := newTestDeps()
	clientID := uuid.New()
	certID := uuid.New()
	sans := models.MTLSSANList{{Type: "DNS", Value: "x"}}
	client := &models.Client{
		ID:                   clientID,
		ManagedCertificateID: &certID,
		MTLSEnabled:          true,
		MTLSCAName:           "some-ca",
		MTLSCASecret:         "fastgateway-client-xxxx-mtls-ca",
		MTLSCASecretKey:      "ca.crt",
		MTLSCAPem:            "pem-data",
		MTLSSANs:             sans,
	}
	d.clientRepo.On("GetByID", clientID).Return(client, nil)
	d.clientRepo.On("Update", mock.MatchedBy(func(c *models.Client) bool {
		return c.ManagedCertificateID == nil &&
			!c.MTLSEnabled &&
			c.MTLSCAName == "" &&
			c.MTLSCASecret == "" &&
			c.MTLSCASecretKey == "" &&
			c.MTLSCAPem == "" &&
			c.MTLSSANs == nil &&
			c.MTLSHashes == nil
	})).Return(nil)

	got, err := d.svc.DetachCertificate(context.Background(), clientID, uuid.New())
	require.NoError(t, err)
	assert.False(t, got.MTLSEnabled)
	assert.Nil(t, got.ManagedCertificateID)

	d.clientRepo.AssertExpectations(t)
}

func TestClientCertificateService_AttachableCertificates_PassesTeamProjectIDsThrough(t *testing.T) {
	d := newTestDeps()
	clientID := uuid.New()
	teamID := uuid.New()
	projectA := uuid.New()
	projectB := uuid.New()
	client := &models.Client{ID: clientID, TeamID: teamID}
	want := []models.ManagedCertificate{{ID: uuid.New()}, {ID: uuid.New()}}

	d.clientRepo.On("GetByID", clientID).Return(client, nil)
	d.teamRepo.On("ListTeamProjects", teamID).Return([]models.ProjectTeamRole{
		{ProjectID: projectA, TeamID: teamID},
		{ProjectID: projectB, TeamID: teamID},
	}, nil)
	d.certRepo.On("ListAttachableClientCerts", mock.MatchedBy(func(ids []uuid.UUID) bool {
		return len(ids) == 2 &&
			((ids[0] == projectA && ids[1] == projectB) || (ids[0] == projectB && ids[1] == projectA))
	})).Return(want, nil)

	got, err := d.svc.AttachableCertificates(clientID)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	d.clientRepo.AssertExpectations(t)
	d.teamRepo.AssertExpectations(t)
	d.certRepo.AssertExpectations(t)
}

func TestClientCertificateService_AttachableCertificates_NoTeamProjectsReturnsEmptyWithoutQuerying(t *testing.T) {
	d := newTestDeps()
	clientID := uuid.New()
	teamID := uuid.New()
	client := &models.Client{ID: clientID, TeamID: teamID}

	d.clientRepo.On("GetByID", clientID).Return(client, nil)
	d.teamRepo.On("ListTeamProjects", teamID).Return([]models.ProjectTeamRole{}, nil)

	got, err := d.svc.AttachableCertificates(clientID)
	require.NoError(t, err)
	assert.Empty(t, got)

	d.certRepo.AssertNotCalled(t, "ListAttachableClientCerts", mock.Anything)
}

func TestClientCertificateService_AttachableCertificates_ClientNotFound(t *testing.T) {
	d := newTestDeps()
	clientID := uuid.New()
	d.clientRepo.On("GetByID", clientID).Return(nil, gorm.ErrRecordNotFound)

	_, err := d.svc.AttachableCertificates(clientID)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	d.teamRepo.AssertNotCalled(t, "ListTeamProjects", mock.Anything)
}

func TestClientCertificateService_DetachCertificate_NoOpWhenNotAttached(t *testing.T) {
	d := newTestDeps()
	clientID := uuid.New()
	client := &models.Client{ID: clientID}
	d.clientRepo.On("GetByID", clientID).Return(client, nil)

	got, err := d.svc.DetachCertificate(context.Background(), clientID, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, client, got)

	d.clientRepo.AssertNotCalled(t, "Update", mock.Anything)
}
