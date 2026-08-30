package services

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTimeout   = 10 * time.Second
	maxResponseBytes = 50 * 1024 * 1024
)

// PromClientConfig configures a PromClient instance.
type PromClientConfig struct {
	EndpointURL   string
	AuthType      string // "none" | "bearer" | "basic"
	Username      string
	Password      string
	Token         string
	TLSSkipVerify bool
	CACert        string // PEM-encoded
	// Timeout is the per-request timeout. Applies in addition to any
	// deadline on the caller-provided context — whichever fires first wins.
	Timeout time.Duration
}

// PromClient is a minimal Prometheus-compatible query client.
// Works against Prometheus and VictoriaMetrics (shared query API).
type PromClient struct {
	cfg    PromClientConfig
	client *http.Client
}

// PromPoint is a single (timestamp, value) sample.
// JSON tags are explicit because PromPoint is serialized in HTTP responses.
type PromPoint struct {
	Time  time.Time `json:"time"`
	Value float64   `json:"value"`
}

// PromSeries is a labeled time series. Internal to the service layer
// (not serialized directly in HTTP responses).
type PromSeries struct {
	Labels map[string]string
	Points []PromPoint
}

// PromRangeResult is the normalized output of a QueryRange call. Internal.
type PromRangeResult struct {
	Series []PromSeries
}

// PromSample is a single instant-query result. Internal.
type PromSample struct {
	Labels map[string]string
	Time   time.Time
	Value  float64
}

// PromInstantResult is the normalized output of a QueryInstant call. Internal.
type PromInstantResult struct {
	Samples []PromSample
}

// NewPromClient constructs a PromClient from config.
func NewPromClient(cfg PromClientConfig) *PromClient {
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}

	tlsCfg := &tls.Config{InsecureSkipVerify: cfg.TLSSkipVerify}
	if cfg.CACert != "" && !cfg.TLSSkipVerify {
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM([]byte(cfg.CACert)) {
			tlsCfg.RootCAs = pool
		} else {
			// Invalid PEM falls through to system roots. Upstream validation
			// (in MetricsService) should reject invalid PEM at config time;
			// this log line exists as a last-resort diagnostic.
			log.Printf("prom_client: failed to parse CACert PEM, falling back to system roots")
		}
	}

	return &PromClient{
		cfg: cfg,
		client: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}
}

// QueryRange fires a range query against /api/v1/query_range.
func (c *PromClient) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*PromRangeResult, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", strconv.FormatInt(start.Unix(), 10))
	params.Set("end", strconv.FormatInt(end.Unix(), 10))
	params.Set("step", strconv.FormatFloat(step.Seconds(), 'f', -1, 64)+"s")

	body, err := c.doRequest(ctx, "/api/v1/query_range", params)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Values [][]interface{}   `json:"values"`
			} `json:"result"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if parsed.Status != "success" {
		return nil, fmt.Errorf("prom error: %s", parsed.Error)
	}

	out := &PromRangeResult{Series: make([]PromSeries, 0, len(parsed.Data.Result))}
	for _, r := range parsed.Data.Result {
		series := PromSeries{Labels: r.Metric, Points: make([]PromPoint, 0, len(r.Values))}
		for _, v := range r.Values {
			if len(v) != 2 {
				continue
			}
			tsFloat, _ := v[0].(float64)
			strVal, _ := v[1].(string)
			val, err := strconv.ParseFloat(strVal, 64)
			if err != nil {
				continue
			}
			sec := int64(tsFloat)
			nsec := int64((tsFloat - float64(sec)) * 1e9)
			series.Points = append(series.Points, PromPoint{
				Time:  time.Unix(sec, nsec),
				Value: val,
			})
		}
		out.Series = append(out.Series, series)
	}
	return out, nil
}

// QueryInstant fires an instant query against /api/v1/query.
func (c *PromClient) QueryInstant(ctx context.Context, query string) (*PromInstantResult, error) {
	params := url.Values{}
	params.Set("query", query)

	body, err := c.doRequest(ctx, "/api/v1/query", params)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"`
			} `json:"result"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if parsed.Status != "success" {
		return nil, fmt.Errorf("prom error: %s", parsed.Error)
	}

	out := &PromInstantResult{Samples: make([]PromSample, 0, len(parsed.Data.Result))}
	for _, r := range parsed.Data.Result {
		if len(r.Value) != 2 {
			continue
		}
		tsFloat, _ := r.Value[0].(float64)
		strVal, _ := r.Value[1].(string)
		val, err := strconv.ParseFloat(strVal, 64)
		if err != nil {
			continue
		}
		sec := int64(tsFloat)
		nsec := int64((tsFloat - float64(sec)) * 1e9)
		out.Samples = append(out.Samples, PromSample{
			Labels: r.Metric,
			Time:   time.Unix(sec, nsec),
			Value:  val,
		})
	}
	return out, nil
}

func (c *PromClient) doRequest(ctx context.Context, path string, params url.Values) ([]byte, error) {
	base := strings.TrimRight(c.cfg.EndpointURL, "/")
	fullURL := base + path + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	switch c.cfg.AuthType {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	case "basic":
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("prom response exceeds %d bytes", maxResponseBytes)
	}

	if resp.StatusCode >= 400 {
		// Try to extract Prom error body for clearer message.
		var parsed struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &parsed)
		if parsed.Error != "" {
			return nil, fmt.Errorf("prom http %d: %s", resp.StatusCode, parsed.Error)
		}
		return nil, fmt.Errorf("prom http %d", resp.StatusCode)
	}

	return body, nil
}
