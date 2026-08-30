//go:build e2e

// Package e2e contains end-to-end tests that require a live Kubernetes
// cluster with Envoy Gateway installed and a running FastGateway backend.
//
// These tests are guarded by the `e2e` build tag so that `go test ./...`
// does not attempt to run them. Run them with:
//
//	go test -tags e2e ./e2e/...
package e2e
