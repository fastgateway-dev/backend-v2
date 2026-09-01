package services_test

import (
	"context"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newPVS(k8s *mocks.MockKubernetesService, now func() time.Time) *services.ProjectVersionService {
	// Phase 2E Task 6: Now replaced SetNowFunc. It stays optional -- it is a
	// genuine determinism seam, not a wiring dependency.
	return services.NewProjectVersionService(services.ProjectVersionServiceDeps{K8s: k8s, Now: now})
}

func TestNewProjectVersionService_RequiresK8sAndNowIsOptional(t *testing.T) {
	assert.PanicsWithValue(t,
		"services.NewProjectVersionService: missing required dependency: K8s",
		func() { services.NewProjectVersionService(services.ProjectVersionServiceDeps{}) })

	assert.NotPanics(t, func() {
		services.NewProjectVersionService(services.ProjectVersionServiceDeps{
			K8s: new(mocks.MockKubernetesService),
		})
	}, "Now is an optional determinism seam; nil means time.Now")
}

func TestProjectVersionService_CacheMiss(t *testing.T) {
	k8s := new(mocks.MockKubernetesService)
	pid := uuid.New()
	k8s.On("DetectVersions", mock.Anything, pid).Return(&cluster.RawVersions{
		EGVersion: "1.7.0", EGImage: "envoyproxy/gateway:v1.7.0", EGSource: "deployment/envoy-gateway",
		GWVersion: "1.4.1", GWSource: "crd/gateways.gateway.networking.k8s.io",
	}, nil).Once()

	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	svc := newPVS(k8s, func() time.Time { return now })

	got, err := svc.Get(context.Background(), pid, false)
	require.NoError(t, err)
	assert.Equal(t, services.VersionStatusSupported, got.Status)
	assert.Equal(t, "1.7.0", got.EnvoyGateway.Version)
	assert.Equal(t, "1.4.1", got.GatewayAPI.Version)
	k8s.AssertExpectations(t)
}

func TestProjectVersionService_CacheHit(t *testing.T) {
	k8s := new(mocks.MockKubernetesService)
	pid := uuid.New()
	k8s.On("DetectVersions", mock.Anything, pid).Return(&cluster.RawVersions{
		EGVersion: "1.7.0", GWVersion: "1.4.1",
	}, nil).Once()

	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	svc := newPVS(k8s, func() time.Time { return now })

	_, _ = svc.Get(context.Background(), pid, false)
	_, _ = svc.Get(context.Background(), pid, false)
	k8s.AssertNumberOfCalls(t, "DetectVersions", 1)
}

func TestProjectVersionService_CacheExpiry(t *testing.T) {
	k8s := new(mocks.MockKubernetesService)
	pid := uuid.New()
	k8s.On("DetectVersions", mock.Anything, pid).Return(&cluster.RawVersions{
		EGVersion: "1.7.0", GWVersion: "1.4.1",
	}, nil).Twice()

	current := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	svc := newPVS(k8s, func() time.Time { return current })

	_, _ = svc.Get(context.Background(), pid, false)
	current = current.Add(6 * time.Minute)
	_, _ = svc.Get(context.Background(), pid, false)
	k8s.AssertNumberOfCalls(t, "DetectVersions", 2)
}

func TestProjectVersionService_ForceRefresh(t *testing.T) {
	k8s := new(mocks.MockKubernetesService)
	pid := uuid.New()
	k8s.On("DetectVersions", mock.Anything, pid).Return(&cluster.RawVersions{
		EGVersion: "1.7.0", GWVersion: "1.4.1",
	}, nil).Twice()

	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	svc := newPVS(k8s, func() time.Time { return now })

	_, _ = svc.Get(context.Background(), pid, false)
	_, _ = svc.Get(context.Background(), pid, true)
	k8s.AssertNumberOfCalls(t, "DetectVersions", 2)
}

func TestProjectVersionService_UnknownShortTTL(t *testing.T) {
	k8s := new(mocks.MockKubernetesService)
	pid := uuid.New()
	k8s.On("DetectVersions", mock.Anything, pid).Return(&cluster.RawVersions{
		Errors: []string{"forbidden"},
	}, nil).Twice()

	current := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	svc := newPVS(k8s, func() time.Time { return current })

	first, _ := svc.Get(context.Background(), pid, false)
	require.Equal(t, services.VersionStatusUnknown, first.Status)
	current = current.Add(90 * time.Second)
	_, _ = svc.Get(context.Background(), pid, false)
	k8s.AssertNumberOfCalls(t, "DetectVersions", 2)
}

func TestProjectVersionService_Classification(t *testing.T) {
	cases := []struct {
		name       string
		eg, gw     string
		wantStatus services.VersionStatus
	}{
		{"supported", "1.7.0", "1.4.1", services.VersionStatusSupported},
		{"untested", "1.8.0", "1.4.1", services.VersionStatusUntested},
		{"unknown-no-eg", "", "1.4.1", services.VersionStatusUnknown},
		{"unknown-no-gw", "1.7.0", "", services.VersionStatusUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k8s := new(mocks.MockKubernetesService)
			pid := uuid.New()
			k8s.On("DetectVersions", mock.Anything, pid).Return(&cluster.RawVersions{
				EGVersion: c.eg, GWVersion: c.gw,
			}, nil).Once()
			now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
			svc := newPVS(k8s, func() time.Time { return now })
			got, err := svc.Get(context.Background(), pid, false)
			require.NoError(t, err)
			assert.Equal(t, c.wantStatus, got.Status)
		})
	}
}

func TestProjectVersionService_Invalidate(t *testing.T) {
	k8s := new(mocks.MockKubernetesService)
	pid := uuid.New()
	k8s.On("DetectVersions", mock.Anything, pid).Return(&cluster.RawVersions{
		EGVersion: "1.7.0", GWVersion: "1.4.1",
	}, nil).Twice()

	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	svc := newPVS(k8s, func() time.Time { return now })

	_, _ = svc.Get(context.Background(), pid, false)
	svc.Invalidate(pid)
	_, _ = svc.Get(context.Background(), pid, false)
	k8s.AssertNumberOfCalls(t, "DetectVersions", 2)
}
