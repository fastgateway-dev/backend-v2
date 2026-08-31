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
	k8s   KubernetesServiceInterface
	cache sync.Map // map[uuid.UUID]*versionCacheEntry
	nowFn func() time.Time
}

func NewProjectVersionService(k8s KubernetesServiceInterface) *ProjectVersionService {
	return &ProjectVersionService{k8s: k8s, nowFn: time.Now}
}

// SetNowFunc overrides the clock; intended for tests.
func (s *ProjectVersionService) SetNowFunc(fn func() time.Time) { s.nowFn = fn }

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
