//go:build e2e

package harness

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	APIURL        string
	GatewayIP     string
	GatewayDomain string
	GatewayPort   int

	AdminUser, AdminPass       string
	EditorUser, EditorPass     string
	ApproverUser, ApproverPass string

	ProjectName string
	DomainName  string
	Namespace   string

	KubeContext  string
	JWTServerURL string
	MockPromURL  string

	// EnvoyGatewayVersion is the Envoy Gateway release under test, from
	// ENVOY_GATEWAY_VERSION (the same job-level variable the CI workflow
	// passes to `helm install --version`). It is "" when the suite runs
	// against a cluster whose Envoy Gateway version nobody declared.
	//
	// Tests must not branch on this to weaken an assertion. It is for
	// naming the release in a failure or skip message, so a report says
	// WHICH Envoy Gateway produced the behaviour -- see
	// grpcroute/features_mirror_test.go.
	EnvoyGatewayVersion string
}

func FromEnv() (*Config, error) {
	c := &Config{
		APIURL:    env("FASTGATEWAY_API_URL", "http://localhost:8081/api/v1"),
		GatewayIP: env("GATEWAY_IP", ""),
		// GATEWAY_DOMAIN falls back to DOMAIN_NAME (the seeder's own var
		// for the same hostname) before the literal default, so setting
		// only DOMAIN_NAME cannot leave SNI/Host pointing at a stale
		// hostname while the seeded domain itself moved on.
		GatewayDomain: envFallback("GATEWAY_DOMAIN", "DOMAIN_NAME", "api.fastgateway.local"),
		// The server (internal/config/config.go) reads ADMIN_USERNAME /
		// ADMIN_PASSWORD -- and ADMIN_PASSWORD has no default there, it is
		// a hard error if unset. Prefer the server's own var names first
		// so anyone who sets ADMIN_PASSWORD (as the server's own error
		// message instructs) doesn't get a 401 here; ADMIN_USER/ADMIN_PASS
		// remain the fallback for compatibility with existing CI config.
		AdminUser:  envFallback("ADMIN_USERNAME", "ADMIN_USER", "admin"),
		AdminPass:  envFallback("ADMIN_PASSWORD", "ADMIN_PASS", "admin123"),
		EditorUser: env("EDITOR_USER", "dev1"),
		// cmd/e2e-seed writes every seeded user's password from
		// SEED_USER_PASS, not EDITOR_PASS/APPROVER_PASS -- those only
		// worked here because their defaults happen to match
		// SEED_USER_PASS's default. Fall back to SEED_USER_PASS so a
		// custom seed password doesn't silently desync from what the
		// harness logs in with.
		EditorPass:   envFallback("EDITOR_PASS", "SEED_USER_PASS", "password123"),
		ApproverUser: env("APPROVER_USER", "sec1"),
		ApproverPass: envFallback("APPROVER_PASS", "SEED_USER_PASS", "password123"),
		ProjectName:  env("PROJECT_NAME", "e2e"),
		DomainName:   env("DOMAIN_NAME", "api.fastgateway.local"),
		Namespace:    env("FG_NAMESPACE", "fastgateway-system"),
		KubeContext:  env("KUBE_CONTEXT", ""),
		JWTServerURL: env("JWT_SERVER_URL", ""),
		MockPromURL:  env("MOCK_PROM_URL", ""),

		EnvoyGatewayVersion: env("ENVOY_GATEWAY_VERSION", ""),
	}
	port, err := strconv.Atoi(env("GATEWAY_PORT", "443"))
	if err != nil {
		return nil, fmt.Errorf("GATEWAY_PORT: %w", err)
	}
	c.GatewayPort = port

	var missing []string
	if c.GatewayIP == "" {
		missing = append(missing, "GATEWAY_IP")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envFallback reads primary, then falls back to fallback (itself resolved
// against def), for a pair of env vars that name the same setting under
// two different conventions.
func envFallback(primary, fallback, def string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	return env(fallback, def)
}
