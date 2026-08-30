// Command mock-prometheus is a deterministic stand-in for a Prometheus
// HTTP API, used by the platform suite's observability tests.
//
// It exists because those four tests skipped on every run: without a
// metrics backend the project's metricsEndpointURL cannot be pointed at
// anything, so GetRouteMetrics / GetDomainMetrics / TestConnection had no
// end-to-end coverage at all. A real Prometheus would be the wrong
// fixture -- it would need scrape targets, a settling period, and its
// numbers would drift between runs, forcing exactly the "assert the shape,
// not the value" tests this suite exists to eliminate.
//
// This serves the two endpoints internal/services/prom_client.go actually
// calls, /api/v1/query and /api/v1/query_range, with FIXED data, so tests
// can assert real values. It also records every query it receives and
// exposes them at /__queries, which lets a test verify that the backend
// issued the PromQL it was supposed to -- something no amount of
// response-shape checking can establish.
//
// It runs on the CI runner alongside the backend rather than inside the
// cluster, because the BACKEND is the client here (the test process never
// talks to it), and the backend runs on the runner too.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// InstantValues are the values /api/v1/query answers carry, in order, one
// per configured cluster. Descending, so a topk-style panel has something
// to rank and a test can assert the ORDER rather than merely
// non-emptiness.
var InstantValues = []float64{30, 20, 10}

// clusters holds the envoy_cluster_name label values returned by
// /api/v1/query, settable at runtime via POST /__set-clusters.
//
// This is what lets a test prove the backend's cluster-name lookup is
// correct rather than merely well-formed. MetricsService maps an instant
// sample back to a route by matching envoy_cluster_name against a key it
// builds itself, so a test can create a real route, read the real
// HTTPRoute object name out of Kubernetes, hand THAT cluster name to this
// mock, and require the route to come back in topRoutesByRps. A hardcoded
// placeholder here could never catch a mismatch, because it would never
// match anything either way.
type clusterStore struct {
	mu    sync.Mutex
	names []string
}

func (c *clusterStore) set(names []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.names = names
}

func (c *clusterStore) get() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.names) == 0 {
		return []string{"httproute/unset/mock-cluster/rule/0"}
	}
	out := make([]string, len(c.names))
	copy(out, c.names)
	return out
}

// RangeValue is the value every point of every /api/v1/query_range series
// carries. Fixed, so a test can assert the number itself.
const RangeValue = 42

type recorder struct {
	mu      sync.Mutex
	queries []queryRecord
}

type queryRecord struct {
	Path  string `json:"path"`
	Query string `json:"query"`
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
	Step  string `json:"step,omitempty"`
}

func (r *recorder) record(rec queryRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Bounded: a long run must not grow this without limit.
	if len(r.queries) >= 500 {
		r.queries = r.queries[1:]
	}
	r.queries = append(r.queries, rec)
}

func (r *recorder) snapshot() []queryRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]queryRecord, len(r.queries))
	copy(out, r.queries)
	return out
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9091"
	}

	rec := &recorder{}
	clusters := &clusterStore{}
	mux := http.NewServeMux()

	// /api/v1/query -- instant vector.
	mux.HandleFunc("/api/v1/query", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query().Get("query")
		rec.record(queryRecord{Path: "/api/v1/query", Query: q})

		now := float64(time.Now().Unix())
		names := clusters.get()
		result := make([]map[string]any, 0, len(names))
		for i, name := range names {
			value := InstantValues[len(InstantValues)-1]
			if i < len(InstantValues) {
				value = InstantValues[i]
			}
			result = append(result, map[string]any{
				"metric": map[string]string{"envoy_cluster_name": name},
				"value":  []any{now, strconv.FormatFloat(value, 'f', -1, 64)},
			})
		}
		writeJSON(w, map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "vector", "result": result},
		})
	})

	// /api/v1/query_range -- matrix. One point per step across the
	// requested window, so a test can assert the point COUNT the backend's
	// own range resolution implies.
	mux.HandleFunc("/api/v1/query_range", func(w http.ResponseWriter, req *http.Request) {
		params := req.URL.Query()
		q := params.Get("query")
		startStr, endStr, stepStr := params.Get("start"), params.Get("end"), params.Get("step")
		rec.record(queryRecord{Path: "/api/v1/query_range", Query: q, Start: startStr, End: endStr, Step: stepStr})

		start, err1 := strconv.ParseInt(startStr, 10, 64)
		end, err2 := strconv.ParseInt(endStr, 10, 64)
		step, err3 := time.ParseDuration(stepStr)
		if err1 != nil || err2 != nil || err3 != nil || step <= 0 || end < start {
			writeJSON(w, map[string]any{
				"status": "error",
				"error":  fmt.Sprintf("bad range params start=%q end=%q step=%q", startStr, endStr, stepStr),
			})
			return
		}

		values := make([]any, 0, 128)
		for ts := start; ts <= end; ts += int64(step.Seconds()) {
			values = append(values, []any{float64(ts), strconv.Itoa(RangeValue)})
		}
		writeJSON(w, map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []map[string]any{{
					"metric": map[string]string{"envoy_cluster_name": clusters.get()[0]},
					"values": values,
				}},
			},
		})
	})

	// /__set-clusters -- replace the envoy_cluster_name labels returned by
	// /api/v1/query. Body: {"clusters": ["httproute/ns/name-abcd1234/rule/0"]}.
	mux.HandleFunc("/__set-clusters", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Clusters []string `json:"clusters"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, fmt.Sprintf("decode body: %v", err), http.StatusBadRequest)
			return
		}
		clusters.set(body.Clusters)
		w.WriteHeader(http.StatusNoContent)
	})

	// /__queries -- everything asked of this server so far, so a test can
	// assert the backend issued the PromQL it claims to.
	mux.HandleFunc("/__queries", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, rec.snapshot())
	})

	// /__reset -- clear the recorded queries, so one test's assertions are
	// not confused by another's traffic.
	mux.HandleFunc("/__reset", func(w http.ResponseWriter, _ *http.Request) {
		rec.mu.Lock()
		rec.queries = nil
		rec.mu.Unlock()
		clusters.set(nil)
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("mock-prometheus listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("mock-prometheus: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("mock-prometheus: encode response: %v", err)
	}
}
