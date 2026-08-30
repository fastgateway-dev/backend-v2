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
}

func FromEnv() (*Config, error) {
	c := &Config{
		APIURL:        env("FASTGATEWAY_API_URL", "http://localhost:8081/api/v1"),
		GatewayIP:     env("GATEWAY_IP", ""),
		GatewayDomain: env("GATEWAY_DOMAIN", "api.fastgateway.local"),
		AdminUser:     env("ADMIN_USER", "admin"),
		AdminPass:     env("ADMIN_PASS", "admin123"),
		EditorUser:    env("EDITOR_USER", "dev1"),
		EditorPass:    env("EDITOR_PASS", "password123"),
		ApproverUser:  env("APPROVER_USER", "sec1"),
		ApproverPass:  env("APPROVER_PASS", "password123"),
		ProjectName:   env("PROJECT_NAME", "e2e"),
		DomainName:    env("DOMAIN_NAME", "api.fastgateway.local"),
		Namespace:     env("FG_NAMESPACE", "fastgateway-system"),
		KubeContext:   env("KUBE_CONTEXT", ""),
		JWTServerURL:  env("JWT_SERVER_URL", ""),
		MockPromURL:   env("MOCK_PROM_URL", ""),
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
