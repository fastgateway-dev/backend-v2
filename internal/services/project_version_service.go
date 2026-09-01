package services

import (
	"context"
	"sync"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/google/uuid"
)

// VersionInfo is the DTO the handler returns to the frontend.
type VersionInfo struct {
	Status         VersionStatus `json:"status"`
	EnvoyGateway   ProbeResult   `json:"envoyGateway"`
	GatewayAPI     ProbeResult   `json:"gatewayAPI"`
	SupportedPairs []VersionPair `json:"supportedPairs"`
	CheckedAt      time.Time     `json:"checkedAt"`
	CacheExpiresAt time.Time     `json:"cacheExpiresAt"`
}

// ProbeResult is the per-component half of VersionInfo.
type ProbeResult struct {
	Version  string  `json:"version"`
	Image    string  `json:"image,omitempty"`
	Source   string  `json:"source,omitempty"`
	Detected bool    `json:"detected"`
	Error    *string `json:"error"`
}

const (
	versionCacheTTL        = 5 * time.Minute
	versionCacheUnknownTTL = 1 * time.Minute
)

type versionCacheEntry struct {
	value     *VersionInfo
	expiresAt time.Time
}

// ProjectVersionService orchestrates DetectVersions + classification + in-memory cache.
type ProjectVersionService struct {
	k8s   VersionDetector
	cache sync.Map // map[uuid.UUID]*versionCacheEntry
	nowFn func() time.Time
}

// ProjectVersionServiceDeps carries everything ProjectVersionService needs.
// Every field is required unless its comment says otherwise.
type ProjectVersionServiceDeps struct {
	// K8s reads the installed versions out of the project's cluster.
	// DetectVersions is the only method this service calls on the cluster
	// client; before Phase 2E Task 7 this field named all 58.
	K8s VersionDetector

	// Now reads the clock. Optional: nil means time.Now. This is a genuine
	// determinism seam -- the cache TTL is computed from it -- so unlike the
	// other Phase 2E constructor parameters it is not required. It replaces
	// SetNowFunc.
	Now func() time.Time
}

// NewProjectVersionService builds a fully-wired ProjectVersionService. It
// panics if a required dependency is missing. Master design section 6.6.
func NewProjectVersionService(deps ProjectVersionServiceDeps) *ProjectVersionService {
	if deps.K8s == nil {
		panic("services.NewProjectVersionService: missing required dependency: K8s")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &ProjectVersionService{k8s: deps.K8s, nowFn: now}
}

// Get returns cached version info if fresh, otherwise detects and caches.
func (s *ProjectVersionService) Get(ctx context.Context, projectID uuid.UUID, forceRefresh bool) (*VersionInfo, error) {
	now := s.nowFn()
	if !forceRefresh {
		if v, ok := s.cache.Load(projectID); ok {
			entry := v.(*versionCacheEntry)
			if now.Before(entry.expiresAt) {
				return entry.value, nil
			}
		}
	}

	raw, err := s.k8s.DetectVersions(ctx, projectID)
	if err != nil {
		return nil, err
	}
	info := buildVersionInfo(raw, now)
	ttl := versionCacheTTL
	if info.Status == VersionStatusUnknown {
		ttl = versionCacheUnknownTTL
	}
	info.CacheExpiresAt = now.Add(ttl)
	s.cache.Store(projectID, &versionCacheEntry{value: info, expiresAt: info.CacheExpiresAt})
	return info, nil
}

// Invalidate drops the cached entry for the project. Idempotent.
func (s *ProjectVersionService) Invalidate(projectID uuid.UUID) {
	s.cache.Delete(projectID)
}

func buildVersionInfo(raw *cluster.RawVersions, now time.Time) *VersionInfo {
	return &VersionInfo{
		Status:         ClassifyVersionPair(raw.EGVersion, raw.GWVersion),
		EnvoyGateway:   buildProbe(raw.EGVersion, raw.EGImage, raw.EGSource, raw.EGError),
		GatewayAPI:     buildProbe(raw.GWVersion, "", raw.GWSource, raw.GWError),
		SupportedPairs: append([]VersionPair(nil), SupportedVersionPairs...),
		CheckedAt:      now,
	}
}

func buildProbe(version, image, source, errMsg string) ProbeResult {
	p := ProbeResult{Version: version, Image: image, Source: source, Detected: version != ""}
	if !p.Detected && errMsg != "" {
		msg := errMsg
		p.Error = &msg
	}
	return p
}
