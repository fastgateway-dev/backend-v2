package services

import (
	"context"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/capabilities"
)

// versionInfoGetter is the slice of ProjectVersionService that CapabilityService
// needs: the cached version read. *ProjectVersionService satisfies it.
type versionInfoGetter interface {
	Get(ctx context.Context, projectID uuid.UUID, forceRefresh bool) (*VersionInfo, error)
}

// CapabilityService answers version-derived capability questions per project,
// reading the cached detected versions. It never errors: a detection failure
// surfaces as unknown versions, so every capability falls back to its
// DefaultWhenUnknown.
type CapabilityService struct {
	versions versionInfoGetter
}

// NewCapabilityService builds a CapabilityService. Panics if versions is nil.
func NewCapabilityService(versions versionInfoGetter) *CapabilityService {
	if versions == nil {
		panic("services.NewCapabilityService: missing required dependency: versions")
	}
	return &CapabilityService{versions: versions}
}

func (s *CapabilityService) versionsFor(ctx context.Context, projectID uuid.UUID) capabilities.Versions {
	info, err := s.versions.Get(ctx, projectID, false)
	if err != nil || info == nil {
		return capabilities.Versions{}
	}
	return capabilities.Versions{
		EnvoyGateway: info.EnvoyGateway.Version,
		GatewayAPI:   info.GatewayAPI.Version,
	}
}

// Has reports a single capability for the project.
func (s *CapabilityService) Has(ctx context.Context, projectID uuid.UUID, name string) bool {
	return capabilities.Evaluate(s.versionsFor(ctx, projectID))[name]
}

// Evaluate returns only the Exposed capabilities for the project (API DTO use).
func (s *CapabilityService) Evaluate(ctx context.Context, projectID uuid.UUID) map[string]bool {
	all := capabilities.Evaluate(s.versionsFor(ctx, projectID))
	out := make(map[string]bool)
	for _, c := range capabilities.Registry {
		if c.Exposed {
			out[c.Name] = all[c.Name]
		}
	}
	return out
}
