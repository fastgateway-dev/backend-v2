package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

func handleAuth(w http.ResponseWriter, r *http.Request) {
	// Log all incoming headers for debugging
	log.Printf("Auth request: %s %s", r.Method, r.URL.Path)
	for name, values := range r.Header {
		for _, value := range values {
			log.Printf("  Header: %s = %s", name, value)
		}
	}

	// Check x-ext-auth-allow header
	// This custom header requires headersToExtAuth configuration in SecurityPolicy
	// because Envoy only forwards default headers (Host, Method, Path, Content-Length, Authorization) by default
	allowHeader := r.Header.Get("x-ext-auth-allow")
	timestamp := time.Now().UTC().Format(time.RFC3339)

	if allowHeader == "true" {
		w.Header().Set("x-auth-decision", "allowed")
		w.Header().Set("x-auth-timestamp", timestamp)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":    "allowed",
			"timestamp": timestamp,
		})
		log.Printf("Auth decision: ALLOWED")
	} else {
		w.Header().Set("x-auth-decision", "denied")
		w.Header().Set("x-auth-timestamp", timestamp)
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{
			"status":    "denied",
			"reason":    "x-ext-auth-allow header not set to 'true'",
			"timestamp": timestamp,
		})
		log.Printf("Auth decision: DENIED (x-ext-auth-allow=%q)", allowHeader)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9001"
	}

	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/", handleAuth)

	log.Printf("External-auth test server starting on :%s", port)
	log.Printf("  Auth endpoint:   http://localhost:%s/ (any path)", port)
	log.Printf("  Health endpoint: http://localhost:%s/health", port)
	log.Printf("  Set 'x-ext-auth-allow: true' header to allow requests")

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
