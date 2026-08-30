// Command e2e-seed replaces e2e/bootstrap.py: it seeds a fresh FastGateway
// deployment with the project, users, teams, domain template, domain, and
// client fixtures that the Go e2e suite (e2e/harness, e2e/...) expects to
// already exist.
//
// It is a standalone binary, not a test: it does not import
// e2e/harness (which is gated behind the "e2e" build tag) so that it
// builds and runs without any build tag. It reuses the backend's own
// request/response types from internal/services and internal/models so a
// contract change becomes a compile error here too.
//
// Run with `go run ./e2e/cmd/e2e-seed` (or `go build ./e2e/cmd/e2e-seed`)
// against a
// running FastGateway API server; see the env vars below for
// configuration.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	ctx := context.Background()

	cfg := loadSeedConfig()

	// --- Fail loudly, before creating anything, if the TLS secret the
	// domain will reference doesn't exist. bootstrap.py:187 hardcoded a
	// secret name derived from the test domain that create-secrets.sh
	// never creates (it creates "domain-tls" in "fastgateway-system"); a
	// domain pointed at a nonexistent secret can never terminate TLS.
	// Here the secret name is configurable (E2E_TLS_SECRET_NAME, default
	// "domain-tls") and its presence is verified up front instead of
	// discovered later as an opaque Gateway/Domain failure. ---
	if err := verifyTLSSecretExists(ctx, cfg); err != nil {
		log.Fatalf("FATAL: %v", err)
	}

	api := &seedAPI{baseURL: strings.TrimRight(cfg.apiBase, "/"), http: &http.Client{Timeout: 30 * time.Second}}

	log.Println("=== Login as admin ===")
	var login struct {
		AccessToken string      `json:"accessToken"`
		User        models.User `json:"user"`
	}
	if err := api.post(ctx, "/auth/login", map[string]string{
		"username": cfg.adminUser,
		"password": cfg.adminPass,
	}, &login); err != nil {
		log.Fatalf("FATAL: login as %q: %v", cfg.adminUser, err)
	}
	api.token = login.AccessToken
	log.Printf("  Logged in as %s (role: %s)", login.User.Username, login.User.Role)

	log.Printf("\n=== Create project: %s ===", cfg.projectName)
	var project models.Project
	if err := api.post(ctx, "/projects", services.CreateProjectInput{
		Name:        cfg.projectName,
		Description: "FastGateway e2e test project",
		K8sAPIURL:   cfg.k8sAPIURL,
		K8sToken:    cfg.k8sToken,
	}, &project); err != nil {
		log.Fatalf("FATAL: create project: %v", err)
	}
	projectID := project.ID.String()
	log.Printf("  Project created: %s", projectID)

	log.Println("  Testing connection...")
	var conn struct {
		Success           bool   `json:"success"`
		Message           string `json:"message"`
		KubernetesVersion string `json:"kubernetesVersion"`
	}
	if err := api.post(ctx, fmt.Sprintf("/projects/%s/test-connection", projectID), nil, &conn); err != nil || !conn.Success {
		log.Fatalf("FATAL: Kubernetes connection test failed (there is no CI scenario where seeding should continue against an unreachable cluster): %v %s", err, conn.Message)
	}
	log.Printf("  Connection OK - Kubernetes %s", conn.KubernetesVersion)

	log.Println("\n=== Create users ===")
	users := map[string]uuid.UUID{}
	for _, u := range []struct{ username, email string }{
		{"dev1", "dev1@fastgateway.local"},
		{"dev2", "dev2@fastgateway.local"},
		{"dev3", "dev3@fastgateway.local"},
		{"dev4", "dev4@fastgateway.local"},
		{"sec1", "sec1@fastgateway.local"},
	} {
		var user models.User
		if err := api.post(ctx, "/users", services.CreateUserInput{
			Username: u.username,
			Email:    u.email,
			Password: cfg.seedUserPass,
			Role:     models.UserRole("user"),
		}, &user); err != nil {
			log.Fatalf("FATAL: create user %s: %v", u.username, err)
		}
		users[u.username] = user.ID
		log.Printf("  Created user: %s (%s)", u.username, user.ID)
	}

	log.Println("\n=== Create teams ===")
	teams := map[string]uuid.UUID{}
	for _, tm := range []struct{ name, desc string }{
		{"dev", "Development team"},
		{"money-dev", "Money development team"},
		{"security", "Security team"},
		{"view", "View-only team"},
	} {
		var team models.Team
		if err := api.post(ctx, "/teams", services.CreateTeamInput{
			Name:        tm.name,
			Description: tm.desc,
		}, &team); err != nil {
			log.Fatalf("FATAL: create team %s: %v", tm.name, err)
		}
		teams[tm.name] = team.ID
		log.Printf("  Created team: %s (%s)", tm.name, team.ID)
	}

	log.Println("\n=== Assign users to teams ===")
	for _, a := range []struct{ username, team string }{
		{"dev1", "dev"},
		{"dev2", "dev"},
		{"dev3", "money-dev"},
		{"dev4", "money-dev"},
		{"sec1", "security"},
	} {
		path := fmt.Sprintf("/teams/%s/members", teams[a.team])
		if err := api.post(ctx, path, map[string]uuid.UUID{"userId": users[a.username]}, nil); err != nil {
			log.Fatalf("FATAL: add %s to team %s: %v", a.username, a.team, err)
		}
		log.Printf("  Added %s -> %s", a.username, a.team)
	}

	log.Println("\n=== Fetch built-in presets ===")
	var presetList []models.PermissionPreset
	if err := api.get(ctx, fmt.Sprintf("/projects/%s/presets", projectID), &presetList); err != nil {
		log.Fatalf("FATAL: list presets: %v", err)
	}
	presets := map[string]uuid.UUID{}
	names := make([]string, 0, len(presetList))
	for _, p := range presetList {
		presets[p.Name] = p.ID
		names = append(names, p.Name)
	}
	log.Printf("  Found presets: %s", strings.Join(names, ", "))

	log.Println("\n=== Assign teams to project ===")
	for _, ta := range []struct {
		team    string
		presets []string
	}{
		{"dev", []string{"Editor"}},
		{"money-dev", []string{"Editor"}},
		{"security", []string{"Approver"}},
		{"view", []string{"Viewer"}},
	} {
		presetIDs := make([]uuid.UUID, 0, len(ta.presets))
		for _, name := range ta.presets {
			id, ok := presets[name]
			if !ok {
				log.Fatalf("FATAL: preset %q not found (have: %s)", name, strings.Join(names, ", "))
			}
			presetIDs = append(presetIDs, id)
		}
		if err := api.post(ctx, fmt.Sprintf("/projects/%s/teams", projectID), services.AssignTeamInput{
			TeamID:    teams[ta.team],
			PresetIDs: presetIDs,
		}, nil); err != nil {
			log.Fatalf("FATAL: assign team %s to project: %v", ta.team, err)
		}
		log.Printf("  Assigned %s -> %s", ta.team, strings.Join(ta.presets, ", "))
	}

	// --- Register namespace. FIX vs bootstrap.py: bootstrap.py posts only
	// {"namespace": "default"}, but CreateProjectNamespaceInput.Capabilities
	// is `binding:"required"` on this backend -- that payload would 400.
	// Request every capability so the namespace can host Gateways, backend
	// Services, and TLS secrets alike. ---
	log.Printf("\n=== Register namespace: %s ===", cfg.namespace)
	var ns models.ProjectNamespace
	if err := api.post(ctx, fmt.Sprintf("/projects/%s/namespaces", projectID), services.CreateProjectNamespaceInput{
		Namespace:    cfg.namespace,
		Capabilities: models.AllowedNamespaceCapabilities,
	}, &ns); err != nil {
		log.Fatalf("FATAL: register namespace %s: %v", cfg.namespace, err)
	}
	// ProjectNamespaceService.Create returns a nil error even when the
	// ReferenceGrant it needs failed to apply -- it only logs and leaves
	// ReferenceGrantCreated false. Without a ReferenceGrant, every
	// cross-namespace backendRef the suite creates is denied and every
	// traffic test 500s, so treat this the same as a hard failure here.
	if !ns.ReferenceGrantCreated {
		log.Fatalf("FATAL: namespace %s registered but ReferenceGrant was not created (cross-namespace backendRefs will be denied; check the FastGateway server logs for the underlying Kubernetes error)", cfg.namespace)
	}
	log.Printf("  Namespace registered: %s (ReferenceGrant created: %v)", ns.ID, ns.ReferenceGrantCreated)

	log.Println("\n=== Create domain template: default-public ===")
	var template models.DomainTemplate
	if err := api.post(ctx, fmt.Sprintf("/projects/%s/domain-templates", projectID), services.CreateDomainTemplateInput{
		Name:                  "default-public",
		Description:           "Default public domain template with TLS and LoadBalancer",
		ExposureType:          "LoadBalancer",
		TLSMode:               "tls_only",
		HTTPSPort:             443,
		TLSPolicy:             "terminate",
		ExternalTrafficPolicy: "Local",
	}, &template); err != nil {
		log.Fatalf("FATAL: create domain template: %v", err)
	}
	// DomainTemplateService.Create/reconcile returns a 201 with the record
	// itself carrying Status=error (StatusMessage explains why) rather than
	// a non-nil error when GatewayClass/EnvoyProxy creation fails. Left
	// unchecked, the seeder would print "Bootstrap complete" with no
	// GatewayClass ever created, and every domain built on top of this
	// template would be broken from the start.
	if template.Status != models.DomainTemplateStatusActive {
		log.Fatalf("FATAL: domain template %s did not become active (status: %s, message: %s)", template.ID, template.Status, template.StatusMessage)
	}
	log.Printf("  Template created: %s (status: %s)", template.ID, template.Status)

	log.Printf("\n=== Create domain: %s ===", cfg.domainName)
	var domain models.Domain
	if err := api.post(ctx, fmt.Sprintf("/projects/%s/domains", projectID), services.CreateDomainInput{
		Name:             cfg.domainName,
		Hostname:         cfg.domainName,
		DomainTemplateID: template.ID.String(),
		TLSSecretName:    cfg.tlsSecretName,
	}, &domain); err != nil {
		log.Fatalf("FATAL: create domain: %v", err)
	}
	// DomainService.Create likewise returns a 201 with Status=error (see
	// internal/services/domain_service.go) rather than a non-nil error
	// when Gateway creation fails -- unchecked, the seeder would report
	// success with no Gateway ever created, and every route built against
	// this domain would fail to deploy.
	if domain.Status != models.DomainStatusActive {
		log.Fatalf("FATAL: domain %s did not become active (status: %s, message: %s)", domain.ID, domain.Status, domain.StatusMessage)
	}
	log.Printf("  Domain created: %s (status: %s)", domain.ID, domain.Status)

	log.Println("\n=== Create clients ===")
	clients := map[string]uuid.UUID{}

	// Client 1: IP allowlist only
	c1 := createClient(ctx, api, services.CreateClientInput{
		Name:         "ip-only-client",
		Description:  "Client with only IP allowlist authentication",
		TeamID:       teams["dev"],
		ContactName:  "Dev Team",
		ContactEmail: "dev@fastgateway.local",
	})
	clients["ip-only-client"] = c1.ID
	log.Printf("  Created client: ip-only-client (%s)", c1.ID)
	for _, ip := range []struct{ cidr, desc string }{
		{"192.168.1.0/24", "Internal network"},
		{"10.0.0.0/8", "Private network"},
	} {
		addClientIP(ctx, api, c1.ID, ip.cidr, ip.desc)
	}

	// Client 2: IP allowlist + API key
	c2 := createClient(ctx, api, services.CreateClientInput{
		Name:               "ip-apikey-client",
		Description:        "Client with IP allowlist and API key authentication",
		TeamID:             teams["dev"],
		ContactName:        "Dev Team",
		ContactEmail:       "dev@fastgateway.local",
		ClientIDHeaderName: "x-client-id",
	})
	clients["ip-apikey-client"] = c2.ID
	log.Printf("  Created client: ip-apikey-client (%s)", c2.ID)
	for _, ip := range []struct{ cidr, desc string }{
		{"172.16.0.0/12", "Docker network"},
		{"100.64.0.0/10", "Carrier-grade NAT"},
	} {
		addClientIP(ctx, api, c2.ID, ip.cidr, ip.desc)
	}
	if key, err := generateAPIKey(ctx, api, c2.ID); err != nil {
		log.Printf("    WARNING: API key generation failed: %v", err)
	} else {
		log.Printf("    API key generated: %s", key.Prefix)
	}

	// Client 3: API key only
	c3 := createClient(ctx, api, services.CreateClientInput{
		Name:               "apikey-only-client",
		Description:        "Client with only API key authentication",
		TeamID:             teams["money-dev"],
		ContactName:        "Money Dev Team",
		ContactEmail:       "money-dev@fastgateway.local",
		ClientIDHeaderName: "x-client-id",
	})
	clients["apikey-only-client"] = c3.ID
	log.Printf("  Created client: apikey-only-client (%s)", c3.ID)
	if key, err := generateAPIKey(ctx, api, c3.ID); err != nil {
		log.Printf("    WARNING: API key generation failed: %v", err)
	} else {
		log.Printf("    API key generated: %s", key.Prefix)
	}

	// Client 4: JWT authentication (uses jwt-server)
	c4 := createClient(ctx, api, services.CreateClientInput{
		Name:               "jwt-client",
		Description:        "Client with JWT authentication via jwt-server",
		TeamID:             teams["dev"],
		ContactName:        "Dev Team",
		ContactEmail:       "dev@fastgateway.local",
		ClientIDHeaderName: "x-client-id",
	})
	clients["jwt-client"] = c4.ID
	log.Printf("  Created client: jwt-client (%s)", c4.ID)
	if err := api.post(ctx, fmt.Sprintf("/clients/%s/jwt", c4.ID), services.ConfigureJWTInput{
		// The Service is in "default", but the JWKS fetch happens from the
		// Envoy proxy pod in "envoy-gateway-system", where the bare name
		// "jwt-server" does not resolve -- use the in-cluster FQDN, same
		// as e2e/suites/security and e2e/suites/grpcroute's
		// defaultJWTIssuerURL.
		Issuer:    "http://jwt-server.default.svc.cluster.local:9000",
		JWKSURL:   "http://jwt-server.default.svc.cluster.local:9000/jwks",
		Audiences: []string{"my-api"},
		RequiredClaims: []models.JWTRequiredClaim{
			{Name: "scope", Values: []string{"api:read"}, ValueType: "StringContains"},
		},
	}, nil); err != nil {
		log.Printf("    WARNING: JWT configuration failed (is jwt-server running?): %v", err)
	} else {
		log.Println("    JWT configured (issuer: http://jwt-server.default.svc.cluster.local:9000)")
	}

	// Client 5: JWT + IP (combined auth)
	c5 := createClient(ctx, api, services.CreateClientInput{
		Name:               "jwt-ip-client",
		Description:        "Client with JWT + IP allowlist authentication",
		TeamID:             teams["dev"],
		ContactName:        "Dev Team",
		ContactEmail:       "dev@fastgateway.local",
		ClientIDHeaderName: "x-client-id",
	})
	clients["jwt-ip-client"] = c5.ID
	log.Printf("  Created client: jwt-ip-client (%s)", c5.ID)
	addClientIP(ctx, api, c5.ID, "192.168.1.0/24", "Internal network")
	if err := api.post(ctx, fmt.Sprintf("/clients/%s/jwt", c5.ID), services.ConfigureJWTInput{
		// The Service is in "default", but the JWKS fetch happens from the
		// Envoy proxy pod in "envoy-gateway-system", where the bare name
		// "jwt-server" does not resolve -- use the in-cluster FQDN, same
		// as e2e/suites/security and e2e/suites/grpcroute's
		// defaultJWTIssuerURL.
		Issuer:    "http://jwt-server.default.svc.cluster.local:9000",
		JWKSURL:   "http://jwt-server.default.svc.cluster.local:9000/jwks",
		Audiences: []string{"my-api"},
	}, nil); err != nil {
		log.Printf("    WARNING: JWT configuration failed (is jwt-server running?): %v", err)
	} else {
		log.Println("    JWT configured (issuer: http://jwt-server.default.svc.cluster.local:9000)")
	}

	clientNames := make([]string, 0, len(clients))
	for name := range clients {
		clientNames = append(clientNames, name)
	}

	log.Println("\n=== Bootstrap complete ===")
	log.Printf("  Project:    %s", projectID)
	log.Printf("  Namespace:  %s (%s)", cfg.namespace, ns.ID)
	log.Printf("  Template:   %s", template.ID)
	log.Printf("  Domain:     %s", domain.ID)
	log.Printf("  Users:      dev1, dev2, dev3, dev4, sec1")
	log.Printf("  Teams:      dev, money-dev, security, view")
	log.Printf("  Clients:    %s", strings.Join(clientNames, ", "))
}

// --- config ---

type seedConfig struct {
	apiBase      string
	adminUser    string
	adminPass    string
	seedUserPass string

	k8sAPIURL string
	k8sToken  string

	projectName   string
	domainName    string
	namespace     string
	fgNamespace   string
	tlsSecretName string
	kubeContext   string
}

func loadSeedConfig() seedConfig {
	c := seedConfig{
		apiBase: env("FASTGATEWAY_API_URL", "http://localhost:8081/api/v1"),
		// The server (internal/config/config.go) reads ADMIN_USERNAME /
		// ADMIN_PASSWORD -- and ADMIN_PASSWORD has no default there, it is
		// a hard error if unset. This binary historically read ADMIN_USER
		// / ADMIN_PASS instead, which only worked because CI happens to
		// set both pairs to the same value. Prefer the server's own var
		// names first so anyone who sets ADMIN_PASSWORD (as the server's
		// own error message instructs) doesn't get a 401 from the seeder.
		adminUser:     envFallback("ADMIN_USERNAME", "ADMIN_USER", "admin"),
		adminPass:     envFallback("ADMIN_PASSWORD", "ADMIN_PASS", "admin123"),
		seedUserPass:  env("SEED_USER_PASS", "password123"),
		k8sAPIURL:     os.Getenv("K8S_API_URL"),
		k8sToken:      os.Getenv("K8S_TOKEN"),
		projectName:   env("PROJECT_NAME", "e2e"),
		domainName:    env("DOMAIN_NAME", "api.fastgateway.local"),
		namespace:     env("NAMESPACE", "default"),
		fgNamespace:   env("FG_NAMESPACE", "fastgateway-system"),
		tlsSecretName: env("E2E_TLS_SECRET_NAME", "domain-tls"),
		kubeContext:   os.Getenv("KUBE_CONTEXT"),
	}
	if c.k8sAPIURL == "" || c.k8sToken == "" {
		log.Fatal("FATAL: K8S_API_URL and K8S_TOKEN must both be set (the project's own K8s credentials -- never host.docker.internal)")
	}
	return c
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

// verifyTLSSecretExists checks -- via client-go against the ambient
// kubeconfig, honouring KUBE_CONTEXT exactly like harness.NewKube -- that
// the TLS secret the domain will reference already exists in the
// FastGateway namespace, so a misconfigured or missing secret fails the
// seed run immediately instead of producing a domain that can never
// terminate TLS.
func verifyTLSSecretExists(ctx context.Context, cfg seedConfig) error {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if cfg.kubeContext != "" {
		overrides.CurrentContext = cfg.kubeContext
	}
	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("build kubernetes clientset: %w", err)
	}

	_, err = clientset.CoreV1().Secrets(cfg.fgNamespace).Get(ctx, cfg.tlsSecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf(
			"TLS secret %q not found in namespace %q -- create it first (see e2e/deps/create-secrets.sh, "+
				"which creates it as \"domain-tls\") or set E2E_TLS_SECRET_NAME to match an existing secret",
			cfg.tlsSecretName, cfg.fgNamespace)
	}
	if err != nil {
		return fmt.Errorf("check TLS secret %s/%s: %w", cfg.fgNamespace, cfg.tlsSecretName, err)
	}
	return nil
}

var _ = corev1.Secret{} // keep the k8s.io/api/core/v1 import even if only used via the typed client above

// --- tiny HTTP client (deliberately not e2e/harness.API: this binary must
// build without the "e2e" tag) ---

type seedAPI struct {
	baseURL string
	token   string
	http    *http.Client
}

func (a *seedAPI) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}

	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: read response body: %w", method, path, err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, string(respBody))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("%s %s: decode response: %w (body: %s)", method, path, err, string(respBody))
		}
	}
	return nil
}

func (a *seedAPI) get(ctx context.Context, path string, out any) error {
	return a.do(ctx, http.MethodGet, path, nil, out)
}

func (a *seedAPI) post(ctx context.Context, path string, body, out any) error {
	return a.do(ctx, http.MethodPost, path, body, out)
}

// --- client helpers ---

func createClient(ctx context.Context, api *seedAPI, input services.CreateClientInput) models.Client {
	var out models.Client
	if err := api.post(ctx, "/clients", input, &out); err != nil {
		log.Fatalf("FATAL: create client %s: %v", input.Name, err)
	}
	return out
}

func addClientIP(ctx context.Context, api *seedAPI, clientID uuid.UUID, cidr, desc string) {
	if err := api.post(ctx, fmt.Sprintf("/clients/%s/ips", clientID), services.CreateClientIPInput{
		CIDR:        cidr,
		Description: desc,
	}, nil); err != nil {
		log.Printf("    WARNING: add IP %s to client %s failed: %v", cidr, clientID, err)
		return
	}
	log.Printf("    Added IP: %s", cidr)
}

func generateAPIKey(ctx context.Context, api *seedAPI, clientID uuid.UUID) (services.GenerateAPIKeyResponse, error) {
	var out services.GenerateAPIKeyResponse
	err := api.post(ctx, fmt.Sprintf("/clients/%s/api-key", clientID), services.GenerateAPIKeyInput{
		HeaderName: "x-api-key",
	}, &out)
	return out, err
}
