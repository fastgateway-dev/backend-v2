package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromClient_QueryRange_Success(t *testing.T) {
	var receivedQuery, receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.Query().Get("query")
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "matrix",
				"result": []map[string]interface{}{
					{
						"metric": map[string]string{"code": "200"},
						"values": [][]interface{}{
							{1712923200, "42"},
							{1712923230, "43"},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := NewPromClient(PromClientConfig{
		EndpointURL: server.URL,
		AuthType:    "bearer",
		Token:       "secret-token",
	})

	start := time.Unix(1712923200, 0)
	end := time.Unix(1712923260, 0)
	res, err := client.QueryRange(context.Background(), "sum(rate(x[1m]))", start, end, 30*time.Second)

	require.NoError(t, err)
	assert.Equal(t, "sum(rate(x[1m]))", receivedQuery)
	assert.Equal(t, "Bearer secret-token", receivedAuth)
	require.Len(t, res.Series, 1)
	assert.Equal(t, "200", res.Series[0].Labels["code"])
	require.Len(t, res.Series[0].Points, 2)
	assert.Equal(t, float64(42), res.Series[0].Points[0].Value)
}

func TestPromClient_QueryRange_BasicAuth(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   map[string]interface{}{"resultType": "matrix", "result": []interface{}{}},
		})
	}))
	defer server.Close()

	client := NewPromClient(PromClientConfig{
		EndpointURL: server.URL,
		AuthType:    "basic",
		Username:    "admin",
		Password:    "pass",
	})

	_, err := client.QueryRange(context.Background(), "up", time.Now(), time.Now().Add(time.Minute), 30*time.Second)
	require.NoError(t, err)
	// base64("admin:pass") = "YWRtaW46cGFzcw=="
	assert.Equal(t, "Basic YWRtaW46cGFzcw==", receivedAuth)
}

func TestPromClient_QueryRange_NoAuth(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   map[string]interface{}{"resultType": "matrix", "result": []interface{}{}},
		})
	}))
	defer server.Close()

	client := NewPromClient(PromClientConfig{
		EndpointURL: server.URL,
		AuthType:    "none",
	})

	_, err := client.QueryRange(context.Background(), "up", time.Now(), time.Now().Add(time.Minute), 30*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "", receivedAuth)
}

func TestPromClient_QueryRange_PromError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "error",
			"errorType": "bad_data",
			"error":     "parse error: unexpected identifier",
		})
	}))
	defer server.Close()

	client := NewPromClient(PromClientConfig{EndpointURL: server.URL, AuthType: "none"})
	_, err := client.QueryRange(context.Background(), "bad", time.Now(), time.Now().Add(time.Minute), 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse error")
}

func TestPromClient_QueryRange_HTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":"error","error":"unauthorized"}`))
	}))
	defer server.Close()

	client := NewPromClient(PromClientConfig{EndpointURL: server.URL, AuthType: "bearer", Token: "wrong"})
	_, err := client.QueryRange(context.Background(), "up", time.Now(), time.Now().Add(time.Minute), 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestPromClient_Query_Instant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/query", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "vector",
				"result": []map[string]interface{}{
					{"metric": map[string]string{}, "value": []interface{}{1712923200, "1"}},
				},
			},
		})
	}))
	defer server.Close()

	client := NewPromClient(PromClientConfig{EndpointURL: server.URL, AuthType: "none"})
	res, err := client.QueryInstant(context.Background(), "1")
	require.NoError(t, err)
	require.Len(t, res.Samples, 1)
	assert.Equal(t, float64(1), res.Samples[0].Value)
}
