package certdist

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// =============================================================================
// ControlPlaneSourceReader
// =============================================================================

func newSecretObj(name string, data map[string]string) *unstructured.Unstructured {
	encoded := make(map[string]interface{}, len(data))
	for k, v := range data {
		encoded[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]interface{}{"name": name, "namespace": "fastgateway-system"},
		"data":       encoded,
	}}
}

func TestControlPlaneSourceReader_Found(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	cp := cluster.NewControlPlaneClient(dyn, "fastgateway-system")
	require.NoError(t, cp.ApplyNamespaced(context.Background(), kubernetes.CoreSecretGVR,
		newSecretObj("cert-abc", map[string]string{"tls.crt": "crt-bytes", "tls.key": "key-bytes"})))

	reader := &ControlPlaneSourceReader{ControlPlane: cp}
	crt, key, found, err := reader.ReadLeafSecret(context.Background(), "cert-abc")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, []byte("crt-bytes"), crt)
	assert.Equal(t, []byte("key-bytes"), key)
}

func TestControlPlaneSourceReader_NotFoundIsNotAnError(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	cp := cluster.NewControlPlaneClient(dyn, "fastgateway-system")

	reader := &ControlPlaneSourceReader{ControlPlane: cp}
	crt, key, found, err := reader.ReadLeafSecret(context.Background(), "does-not-exist")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, crt)
	assert.Nil(t, key)
}

func TestControlPlaneSourceReader_MissingKeyIsAnError(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	cp := cluster.NewControlPlaneClient(dyn, "fastgateway-system")
	require.NoError(t, cp.ApplyNamespaced(context.Background(), kubernetes.CoreSecretGVR,
		newSecretObj("cert-partial", map[string]string{"tls.crt": "crt-bytes"})))

	reader := &ControlPlaneSourceReader{ControlPlane: cp}
	_, _, found, err := reader.ReadLeafSecret(context.Background(), "cert-partial")
	assert.False(t, found)
	assert.Error(t, err)
}

// =============================================================================
// ManagedCertUpdater
// =============================================================================

type fakeManagedCertRepo struct {
	cert      *models.ManagedCertificate
	getErr    error
	updateErr error

	updated *models.ManagedCertificate
}

func (f *fakeManagedCertRepo) GetByID(id uuid.UUID) (*models.ManagedCertificate, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.cert, nil
}

func (f *fakeManagedCertRepo) Update(c *models.ManagedCertificate) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updated = c
	return nil
}

func TestManagedCertUpdater_SetIssuedMeta(t *testing.T) {
	certID := uuid.New()
	repo := &fakeManagedCertRepo{cert: &models.ManagedCertificate{
		ID:     certID,
		Status: models.ManagedCertStatusReady,
	}}
	updater := &ManagedCertUpdater{Repo: repo}

	notAfter := time.Now().Add(90 * 24 * time.Hour)
	require.NoError(t, updater.SetIssuedMeta(certID, "deadbeef", &notAfter))

	require.NotNil(t, repo.updated)
	assert.Equal(t, "deadbeef", repo.updated.Fingerprint)
	require.NotNil(t, repo.updated.NotAfter)
	assert.True(t, notAfter.Equal(*repo.updated.NotAfter))
	// Status must be left untouched by the distribution controller.
	assert.Equal(t, models.ManagedCertStatusReady, repo.updated.Status)
}

func TestManagedCertUpdater_GetError(t *testing.T) {
	repo := &fakeManagedCertRepo{getErr: errors.New("boom")}
	updater := &ManagedCertUpdater{Repo: repo}

	err := updater.SetIssuedMeta(uuid.New(), "fp", nil)
	assert.Error(t, err)
	assert.Nil(t, repo.updated)
}

func TestManagedCertUpdater_UpdateError(t *testing.T) {
	repo := &fakeManagedCertRepo{cert: &models.ManagedCertificate{ID: uuid.New()}, updateErr: errors.New("boom")}
	updater := &ManagedCertUpdater{Repo: repo}

	err := updater.SetIssuedMeta(uuid.New(), "fp", nil)
	assert.Error(t, err)
}
