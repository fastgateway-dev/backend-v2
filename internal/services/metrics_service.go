package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/config"
	"github.com/fastgateway-dev/backend-v2/internal/crypto"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// promClientFactory builds a PromClient from a Project. Overridable in tests.
type promClientFactory func(p *models.Project, encryptionKey string) (PromQueryClient, error)

// PromQueryClient is the subset of PromClient used by MetricsService.
// Defined as an interface so tests can swap in a fake.
type PromQueryClient interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*PromRangeResult, error)
	QueryInstant(ctx context.Context, query string) (*PromInstantResult, error)
}

// TestConnectionResult is returned by MetricsService.TestConnection.
type TestConnectionResult struct {
	OK      bool   `json:"ok"`
	Version string `json:"prometheusVersion,omitempty"`
	Error   string `json:"error,omitempty"`
}

// MetricsTimeRange describes the resolved time range for a query.
type MetricsTimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Step  string    `json:"step"`
}

// RpsByClass holds 2xx/3xx/4xx/5xx time series.
type RpsByClass struct {
	Class2xx []PromPoint `json:"2xx"`
	Class3xx []PromPoint `json:"3xx"`
	Class4xx []PromPoint `json:"4xx"`
	Class5xx []PromPoint `json:"5xx"`
}

// LatencyPercentiles holds p50/p95/p99 time series.
type LatencyPercentiles struct {
	P50 []PromPoint `json:"p50"`
	P95 []PromPoint `json:"p95"`
	P99 []PromPoint `json:"p99"`
}

// RouteMetricsResult is the response for GetRouteMetrics.
type RouteMetricsResult struct {
	TimeRange        MetricsTimeRange   `json:"timeRange"`
	TotalRequests    float64            `json:"totalRequests"`
	ErrorRatePercent float64            `json:"errorRatePercent"`
	Rps              RpsByClass         `json:"rps"`
	Latency          LatencyPercentiles `json:"latency"`
}

// TopRouteEntry is one row of a top-5 table.
type TopRouteEntry struct {
	RouteID   uuid.UUID `json:"routeId"`
	RouteName string    `json:"routeName"`
	Value     float64   `json:"value"`
}

// DomainMetricsResult is the response for GetDomainMetrics.
type DomainMetricsResult struct {
	TimeRange            MetricsTimeRange   `json:"timeRange"`
	TotalRequests        float64            `json:"totalRequests"`
	ErrorRatePercent     float64            `json:"errorRatePercent"`
	Rps                  RpsByClass         `json:"rps"`
	Latency              LatencyPercentiles `json:"latency"`
	TopRoutesByRps       []TopRouteEntry    `json:"topRoutesByRps"`
	TopRoutesByErrorRate []TopRouteEntry    `json:"topRoutesByErrorRate"`
}

// MetricsService composes PromQL queries and normalizes results for the UI.
type MetricsService struct {
	projectRepo   repository.ProjectRepositoryInterface
	routeRepo     repository.RouteRepositoryInterface
	domainRepo    repository.DomainRepositoryInterface
	config        *config.Config
	clientFactory promClientFactory
}

// NewMetricsService constructs a MetricsService using real PromClients.
func NewMetricsService(
	projectRepo repository.ProjectRepositoryInterface,
	routeRepo repository.RouteRepositoryInterface,
	domainRepo repository.DomainRepositoryInterface,
	cfg *config.Config,
) *MetricsService {
	return &MetricsService{
		projectRepo:   projectRepo,
		routeRepo:     routeRepo,
		domainRepo:    domainRepo,
		config:        cfg,
		clientFactory: defaultPromClientFactory,
	}
}

// defaultPromClientFactory builds a real PromClient from a project's stored config.
func defaultPromClientFactory(p *models.Project, encryptionKey string) (PromQueryClient, error) {
	if p.MetricsEndpointURL == "" {
		return nil, errors.New("metrics endpoint not configured for project")
	}

	cfg := PromClientConfig{
		EndpointURL:   p.MetricsEndpointURL,
		AuthType:      p.MetricsAuthType,
		Username:      p.MetricsUsername,
		TLSSkipVerify: p.MetricsTLSSkipVerify,
		CACert:        p.MetricsCACert,
	}

	switch p.MetricsAuthType {
	case "bearer":
		if p.MetricsTokenEncrypted != "" {
			tok, err := crypto.Decrypt(p.MetricsTokenEncrypted, encryptionKey)
			if err != nil {
				return nil, fmt.Errorf("decrypt metrics token: %w", err)
			}
			cfg.Token = tok
		}
	case "basic":
		if p.MetricsPasswordEncrypted != "" {
			pw, err := crypto.Decrypt(p.MetricsPasswordEncrypted, encryptionKey)
			if err != nil {
				return nil, fmt.Errorf("decrypt metrics password: %w", err)
			}
			cfg.Password = pw
		}
	}

	return NewPromClient(cfg), nil
}

// TestConnection verifies the project's metrics endpoint is reachable and authenticated.
func (s *MetricsService) TestConnection(ctx context.Context, projectID uuid.UUID) (*TestConnectionResult, error) {
	project, err := s.projectRepo.GetByID(projectID)
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}

	if project.MetricsEndpointURL == "" {
		return &TestConnectionResult{OK: false, Error: "metrics endpoint not configured for project"}, nil
	}

	client, err := s.clientFactory(project, s.config.EncryptionKey)
	if err != nil {
		return &TestConnectionResult{OK: false, Error: err.Error()}, nil
	}

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if _, err := client.QueryInstant(testCtx, "1"); err != nil {
		return &TestConnectionResult{OK: false, Error: err.Error()}, nil
	}

	return &TestConnectionResult{OK: true}, nil
}

// GetRouteMetrics returns Tier A panels for a single route.
func (s *MetricsService) GetRouteMetrics(ctx context.Context, projectID, routeID uuid.UUID, rangeSpec string) (*RouteMetricsResult, error) {
	start, end, step, stepStr, err := resolveTimeRange(rangeSpec)
	if err != nil {
		return nil, err
	}

	project, err := s.projectRepo.GetByID(projectID)
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	if project.MetricsEndpointURL == "" {
		return nil, errors.New("metrics endpoint not configured for project")
	}

	route, err := s.routeRepo.GetByID(routeID)
	if err != nil {
		return nil, fmt.Errorf("get route: %w", err)
	}

	domain, err := s.domainRepo.GetByID(route.DomainID)
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}

	client, err := s.clientFactory(project, s.config.EncryptionKey)
	if err != nil {
		return nil, err
	}

	selector := buildRouteClusterSelector(domain.Namespace, route.Name)
	queries := buildRouteQueries(selector)

	results, err := fanOutQueries(ctx, client, queries, start, end, step)
	if err != nil {
		return nil, err
	}

	return &RouteMetricsResult{
		TimeRange: MetricsTimeRange{Start: start, End: end, Step: stepStr},
		TotalRequests: sumAllPoints(
			results["rps_2xx"], results["rps_3xx"], results["rps_4xx"], results["rps_5xx"],
		) * step.Seconds(),
		ErrorRatePercent: errorRatePercent(
			results["rps_2xx"], results["rps_3xx"], results["rps_4xx"], results["rps_5xx"],
		),
		Rps: RpsByClass{
			Class2xx: firstSeriesPoints(results["rps_2xx"]),
			Class3xx: firstSeriesPoints(results["rps_3xx"]),
			Class4xx: firstSeriesPoints(results["rps_4xx"]),
			Class5xx: firstSeriesPoints(results["rps_5xx"]),
		},
		Latency: LatencyPercentiles{
			P50: firstSeriesPoints(results["p50"]),
			P95: firstSeriesPoints(results["p95"]),
			P99: firstSeriesPoints(results["p99"]),
		},
	}, nil
}

// GetDomainMetrics returns Tier A panels plus top-5 tables for a domain.
func (s *MetricsService) GetDomainMetrics(ctx context.Context, projectID, domainID uuid.UUID, rangeSpec string) (*DomainMetricsResult, error) {
	start, end, step, stepStr, err := resolveTimeRange(rangeSpec)
	if err != nil {
		return nil, err
	}

	project, err := s.projectRepo.GetByID(projectID)
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	if project.MetricsEndpointURL == "" {
		return nil, errors.New("metrics endpoint not configured for project")
	}

	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}

	routes, _, err := s.routeRepo.ListByDomainID(domainID, 1, 10000, nil, "", "", "", nil)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}

	client, err := s.clientFactory(project, s.config.EncryptionKey)
	if err != nil {
		return nil, err
	}

	selector := buildDomainClusterSelector(domain.Namespace)
	rangeQueries := buildRouteQueries(selector)

	results, err := fanOutQueries(ctx, client, rangeQueries, start, end, step)
	if err != nil {
		return nil, err
	}

	// Top-5 tables use instant queries (topk) since we only need current values.
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	topRpsQL := fmt.Sprintf(
		`topk(5, sum by (envoy_cluster_name) (rate(envoy_cluster_upstream_rq_xx{%s}[5m])))`,
		selector,
	)
	topRps, err := client.QueryInstant(queryCtx, topRpsQL)
	if err != nil {
		return nil, fmt.Errorf("top rps query: %w", err)
	}

	topErrQL := fmt.Sprintf(
		`topk(5, 100 * sum by (envoy_cluster_name) (rate(envoy_cluster_upstream_rq_xx{%s,envoy_response_code_class=~"4|5"}[5m])) / sum by (envoy_cluster_name) (rate(envoy_cluster_upstream_rq_xx{%s}[5m])))`,
		selector, selector,
	)
	topErr, err := client.QueryInstant(queryCtx, topErrQL)
	if err != nil {
		return nil, fmt.Errorf("top error rate query: %w", err)
	}

	nameByCluster := make(map[string]models.Route, len(routes))
	for _, r := range routes {
		cluster := fmt.Sprintf("httproute/%s/%s/rule/0", domain.Namespace, r.Name)
		nameByCluster[cluster] = r
	}

	return &DomainMetricsResult{
		TimeRange: MetricsTimeRange{Start: start, End: end, Step: stepStr},
		TotalRequests: sumAllPoints(
			results["rps_2xx"], results["rps_3xx"], results["rps_4xx"], results["rps_5xx"],
		) * step.Seconds(),
		ErrorRatePercent: errorRatePercent(
			results["rps_2xx"], results["rps_3xx"], results["rps_4xx"], results["rps_5xx"],
		),
		Rps: RpsByClass{
			Class2xx: firstSeriesPoints(results["rps_2xx"]),
			Class3xx: firstSeriesPoints(results["rps_3xx"]),
			Class4xx: firstSeriesPoints(results["rps_4xx"]),
			Class5xx: firstSeriesPoints(results["rps_5xx"]),
		},
		Latency: LatencyPercentiles{
			P50: firstSeriesPoints(results["p50"]),
			P95: firstSeriesPoints(results["p95"]),
			P99: firstSeriesPoints(results["p99"]),
		},
		TopRoutesByRps:       mapTopSamples(topRps, nameByCluster),
		TopRoutesByErrorRate: mapTopSamples(topErr, nameByCluster),
	}, nil
}

func mapTopSamples(res *PromInstantResult, lookup map[string]models.Route) []TopRouteEntry {
	if res == nil {
		return []TopRouteEntry{}
	}
	out := make([]TopRouteEntry, 0, len(res.Samples))
	for _, s := range res.Samples {
		clusterName := s.Labels["envoy_cluster_name"]
		if route, ok := lookup[clusterName]; ok {
			out = append(out, TopRouteEntry{
				RouteID:   route.ID,
				RouteName: route.Name,
				Value:     s.Value,
			})
		}
	}
	return out
}

// resolveTimeRange turns "15m"/"1h"/"6h"/"24h"/"7d" into (start, end, step).
func resolveTimeRange(spec string) (time.Time, time.Time, time.Duration, string, error) {
	now := time.Now()
	switch spec {
	case "", "1h":
		return now.Add(-1 * time.Hour), now, 30 * time.Second, "30s", nil
	case "15m":
		return now.Add(-15 * time.Minute), now, 15 * time.Second, "15s", nil
	case "6h":
		return now.Add(-6 * time.Hour), now, 2 * time.Minute, "120s", nil
	case "24h":
		return now.Add(-24 * time.Hour), now, 5 * time.Minute, "300s", nil
	case "7d":
		return now.Add(-7 * 24 * time.Hour), now, 30 * time.Minute, "1800s", nil
	default:
		return time.Time{}, time.Time{}, 0, "", fmt.Errorf("invalid range: %q (allowed: 15m,1h,6h,24h,7d)", spec)
	}
}

// buildRouteClusterSelector returns the Envoy cluster selector for a single route.
// VERIFICATION TASK: confirm the label name (envoy_cluster_name) matches what
// Envoy Gateway emits. If different (cluster_name / cluster / etc.),
// adjust here — the rest of the fanout architecture is unchanged.
func buildRouteClusterSelector(namespace, routeName string) string {
	return fmt.Sprintf(`envoy_cluster_name=~"httproute/%s/%s/rule/.*"`, namespace, routeName)
}

// buildDomainClusterSelector returns the selector for all routes in a domain namespace.
func buildDomainClusterSelector(namespace string) string {
	return fmt.Sprintf(`envoy_cluster_name=~"httproute/%s/.*/rule/.*"`, namespace)
}

// buildRouteQueries constructs the seven PromQL queries needed for Tier A panels.
func buildRouteQueries(selector string) map[string]string {
	return map[string]string{
		"rps_2xx": fmt.Sprintf(`sum(rate(envoy_cluster_upstream_rq_xx{%s,envoy_response_code_class="2"}[1m]))`, selector),
		"rps_3xx": fmt.Sprintf(`sum(rate(envoy_cluster_upstream_rq_xx{%s,envoy_response_code_class="3"}[1m]))`, selector),
		"rps_4xx": fmt.Sprintf(`sum(rate(envoy_cluster_upstream_rq_xx{%s,envoy_response_code_class="4"}[1m]))`, selector),
		"rps_5xx": fmt.Sprintf(`sum(rate(envoy_cluster_upstream_rq_xx{%s,envoy_response_code_class="5"}[1m]))`, selector),
		"p50":     fmt.Sprintf(`histogram_quantile(0.5, sum(rate(envoy_cluster_upstream_rq_time_bucket{%s}[1m])) by (le))`, selector),
		"p95":     fmt.Sprintf(`histogram_quantile(0.95, sum(rate(envoy_cluster_upstream_rq_time_bucket{%s}[1m])) by (le))`, selector),
		"p99":     fmt.Sprintf(`histogram_quantile(0.99, sum(rate(envoy_cluster_upstream_rq_time_bucket{%s}[1m])) by (le))`, selector),
	}
}

// fanOutQueries runs all queries concurrently with a shared 10s timeout.
func fanOutQueries(ctx context.Context, client PromQueryClient, queries map[string]string, start, end time.Time, step time.Duration) (map[string]*PromRangeResult, error) {
	type result struct {
		key string
		res *PromRangeResult
		err error
	}
	ch := make(chan result, len(queries))

	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for k, q := range queries {
		go func(key, query string) {
			res, err := client.QueryRange(queryCtx, query, start, end, step)
			ch <- result{key: key, res: res, err: err}
		}(k, q)
	}

	out := make(map[string]*PromRangeResult, len(queries))
	for i := 0; i < len(queries); i++ {
		r := <-ch
		if r.err != nil {
			return nil, fmt.Errorf("query %s: %w", r.key, r.err)
		}
		out[r.key] = r.res
	}
	return out, nil
}

func firstSeriesPoints(r *PromRangeResult) []PromPoint {
	if r == nil || len(r.Series) == 0 {
		return []PromPoint{}
	}
	return r.Series[0].Points
}

func sumAllPoints(results ...*PromRangeResult) float64 {
	var total float64
	for _, r := range results {
		if r == nil {
			continue
		}
		for _, s := range r.Series {
			for _, p := range s.Points {
				total += p.Value
			}
		}
	}
	return total
}

func errorRatePercent(r2, r3, r4, r5 *PromRangeResult) float64 {
	total := sumAllPoints(r2, r3, r4, r5)
	if total == 0 {
		return 0
	}
	errorsCount := sumAllPoints(r4, r5)
	return (errorsCount / total) * 100
}
