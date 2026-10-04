package services_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// fakeDNSCredential is the plaintext credential data a fakeDNSCredentialReader
// returns for a seeded id.
type fakeDNSCredential struct {
	providerType string
	creds        map[string]string
}

// fakeDNSCredentialReader is a minimal stand-in for *services.DNSCredentialService
// that satisfies services.DNSCredentialReader, so SetActiveCredential can be
// tested without a real repo/encryption key.
type fakeDNSCredentialReader struct {
	byID map[uuid.UUID]fakeDNSCredential
}

func newFakeDNSCredentialReader() *fakeDNSCredentialReader {
	return &fakeDNSCredentialReader{byID: map[uuid.UUID]fakeDNSCredential{}}
}

func (f *fakeDNSCredentialReader) seedCloudflareCredential(t *testing.T, apiToken string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	f.byID[id] = fakeDNSCredential{providerType: "cloudflare", creds: map[string]string{"apiToken": apiToken}}
	return id
}

func (f *fakeDNSCredentialReader) DecryptedCredentials(id uuid.UUID) (string, map[string]string, error) {
	c, ok := f.byID[id]
	if !ok {
		return "", nil, errors.New("dns credential not found")
	}
	return c.providerType, c.creds, nil
}

// fakeSystemSettingsStore is a minimal stand-in for *services.SystemSettingsService
// that satisfies services.SystemSettingsStore, so SetActiveCredential/
// GetActiveCredentialID can be tested without a real DB-backed singleton row.
type fakeSystemSettingsStore struct {
	activeID *uuid.UUID
}

func (f *fakeSystemSettingsStore) GetActiveDNSCredentialID() (*uuid.UUID, error) {
	return f.activeID, nil
}

func (f *fakeSystemSettingsStore) SetActiveDNSCredentialID(id *uuid.UUID) error {
	f.activeID = id
	return nil
}

// testDNSInfraDeps bundles the fakes/mocks newTestDNSInfraService wires up,
// so tests can seed credentials, inspect what was applied, and read back the
// persisted active-credential id.
type testDNSInfraDeps struct {
	creds    *fakeDNSCredentialReader
	settings *fakeSystemSettingsStore
	applier  *mocks.MockCertInfraApplier

	appliedByGVR map[schema.GroupVersionResource]*unstructured.Unstructured
}

func (d *testDNSInfraDeps) seedCloudflareCredential(t *testing.T, apiToken string) uuid.UUID {
	return d.creds.seedCloudflareCredential(t, apiToken)
}

func (d *testDNSInfraDeps) lastApplied(gvr schema.GroupVersionResource) *unstructured.Unstructured {
	return d.appliedByGVR[gvr]
}

// newTestDNSInfraService builds a services.DNSInfraService wired to fakes for
// the credential reader and the system-settings store, and a mocked
// CertInfraApplier (the narrow control-plane role already reused by the
// certificate-infra services) that records the last object applied per GVR,
// following the fake/mock style used throughout internal/services/*_test.go
// (see managed_certificate_service_test.go for the control-plane fake this
// copies).
func newTestDNSInfraService(t *testing.T) (*services.DNSInfraService, *testDNSInfraDeps) {
	t.Helper()

	deps := &testDNSInfraDeps{
		creds:        newFakeDNSCredentialReader(),
		settings:     &fakeSystemSettingsStore{},
		applier:      new(mocks.MockCertInfraApplier),
		appliedByGVR: map[schema.GroupVersionResource]*unstructured.Unstructured{},
	}

	deps.applier.On("ApplyNamespaced", mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		Run(func(args mock.Arguments) {
			gvr := args.Get(1).(schema.GroupVersionResource)
			obj := args.Get(2).(*unstructured.Unstructured)
			deps.appliedByGVR[gvr] = obj
		}).
		Return(nil)

	svc := services.NewDNSInfraService(services.DNSInfraServiceDeps{
		Creds:        deps.creds,
		Settings:     deps.settings,
		ControlPlane: deps.applier,
	})

	return svc, deps
}

func TestDNSInfraService_SetActiveCredential_RendersSecret(t *testing.T) {
	svc, deps := newTestDNSInfraService(t)
	credID := deps.seedCloudflareCredential(t, "tok")

	err := svc.SetActiveCredential(credID)
	require.NoError(t, err)

	applied := deps.lastApplied(kubernetes.SecretGVR)
	require.NotNil(t, applied, "no Secret applied")
	require.Equal(t, "Secret", applied.GetKind())

	data, found, err := unstructured.NestedStringMap(applied.Object, "stringData")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "tok", data["apiToken"])

	got, err := svc.GetActiveCredentialID()
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, credID, *got)

	deps.applier.AssertNumberOfCalls(t, "ApplyNamespaced", 1)
}

func TestDNSInfraService_SetActiveCredential_UnknownCredential(t *testing.T) {
	svc, deps := newTestDNSInfraService(t)

	err := svc.SetActiveCredential(uuid.New())
	require.Error(t, err)

	deps.applier.AssertNotCalled(t, "ApplyNamespaced", mock.Anything, mock.Anything, mock.Anything)

	got, err := svc.GetActiveCredentialID()
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestDNSInfraService_SetActiveCredential_UnsupportedProvider(t *testing.T) {
	svc, deps := newTestDNSInfraService(t)
	id := uuid.New()
	deps.creds.byID[id] = fakeDNSCredential{providerType: "not-a-real-provider", creds: map[string]string{}}

	err := svc.SetActiveCredential(id)
	require.Error(t, err)

	deps.applier.AssertNotCalled(t, "ApplyNamespaced", mock.Anything, mock.Anything, mock.Anything)
}

func TestDNSInfraService_SetActiveCredential_ApplyFails(t *testing.T) {
	svc, deps := newTestDNSInfraService(t)
	credID := deps.seedCloudflareCredential(t, "tok")

	deps.applier.ExpectedCalls = nil
	deps.applier.On("ApplyNamespaced", mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(errors.New("apply failed"))

	err := svc.SetActiveCredential(credID)
	require.Error(t, err)

	// The setting must not be persisted when the apply fails.
	got, err := svc.GetActiveCredentialID()
	require.NoError(t, err)
	require.Nil(t, got)
}
