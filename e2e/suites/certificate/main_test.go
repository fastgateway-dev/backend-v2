//go:build e2e

// Package certificate exercises FastGateway's managed-certificate feature
// end-to-end: issuer creation, issuer grants, certificate issuance
// (including its approval gate), attachment to a domain, cert-manager
// distribution to the cluster, and -- the part none of that machinery
// alone proves -- that the Envoy Gateway data plane actually serves the
// resulting leaf on TLS connections for the certificate's hostname.
//
// This file holds the shared TestMain bootstrap; sibling test files in
// this same package (server_managed_cert_test.go and, per the e2e-coverage
// plan, later Tier 2/3 files covering client-usage certificates, csr key
// mode, export, and re-issuance) all share the single *harness.Env built
// here. Mirrors e2e/suites/security/main_test.go's TestMain pattern (the
// simplest of the existing suites' bootstraps, since this package needs
// no package-level fixtures beyond the harness env itself).
package certificate

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
)

// env is the shared harness environment: authenticated API clients, the
// gateway data-plane client, and the Kubernetes client. Built once in
// TestMain and reused by every test in this package.
var env *harness.Env

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	env, err = harness.NewEnv(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "certificate e2e: build harness env: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
