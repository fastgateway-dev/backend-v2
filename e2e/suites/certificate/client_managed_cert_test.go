//go:build e2e

package certificate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/fastgateway-dev/backend-v2/internal/services/clients"
)

// This file is Tier 2 of the e2e-coverage plan for the managed-certificate
// feature: "managed client cert mTLS". It proves the managed CLIENT
// certificate path end-to-end -- issue a usage=client, keyMode=managed
// certificate, EXPORT its bundle, attach the managed cert to a Client (which
// derives the client's mTLS trust from the issuer CA and pins the cert's
// SAN via clients.ClientCertificateService.AttachCertificate), attach that
// Client to a route with mTLS enforced, then prove over a real mTLS
// handshake that the exported cert is accepted and an unrelated cert is
// denied.
//
// Everything below this comment (constants, domain-mTLS helpers, client
// helpers, route helpers, the assertion helpers) is a deliberate,
// file-local mirror of e2e/suites/security/{main_test.go,
// client_mode_helpers_test.go, domain_mtls_helpers_test.go} and its
// TestClientModeMTLS. Those are suite-local (package security, `_test.go`)
// and therefore not importable from package certificate, and task-5's brief
// forbids touching e2e/harness or the security suite, so the pieces this
// test needs are replicated inline here rather than shared. Where this test
// diverges from that reference on purpose, see the doc comment on
// TestClientManagedCertMTLS itself.
//
// pollUntil, createSelfSignedCAIssuer, and the cert-readiness timeout
// constants (issuerReadyTimeout, certReadyTimeout, pollInterval) are NOT
// redefined here -- they already live in server_managed_cert_test.go in
// this same package and are reused as-is.

const (
	// clientMTLSBackendNamespace/clientMTLSNginxService/clientMTLSNginxPort
	// point this suite's route at the exact same shared test backend
	// e2e/suites/security uses (see that package's main_test.go doc
	// comment): a stock nginx that always serves "Welcome to nginx!" at
	// "/" and 404s anything else, which is why every route below also
	// carries a urlRewrite back to "/".
	clientMTLSBackendNamespace = "default"
	clientMTLSNginxService     = "nginx-service"
	clientMTLSNginxPort        = 80

	// clientMTLSRouteLiveTimeout mirrors e2e/suites/security/main_test.go's
	// routeLiveTimeout: how long a freshly deployed route and client
	// attachment take to actually converge (HTTPRoute + SecurityPolicy +
	// ClientTrafficPolicy all reconciled) before the gateway serves their
	// true, final behavior.
	clientMTLSRouteLiveTimeout = 180 * time.Second
)

// --- domain mTLS helpers (mirror e2e/suites/security/domain_mtls_helpers_test.go) ---

// clientMTLSDomainSettingsEnvelope mirrors domain_mtls_helpers_test.go's
// domainSettingsEnvelope: DomainHandler.GetDomainSettings/
// UpdateDomainSettings/AddDomainMTLSCA all nest the actual config under a
// top-level "settings" key.
type clientMTLSDomainSettingsEnvelope struct {
	Settings models.DomainSettingsConfig `json:"settings"`
}

// getClientMTLSDomainSettings mirrors api.py:get_domain_settings (GET
// /projects/:projectId/domains/:domainId/settings).
func getClientMTLSDomainSettings(ctx context.Context, projectID, domainID string) (models.DomainSettingsConfig, error) {
	var out clientMTLSDomainSettingsEnvelope
	path := "/projects/" + projectID + "/domains/" + domainID + "/settings"
	if _, err := env.Admin.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return models.DomainSettingsConfig{}, err
	}
	return out.Settings, nil
}

// updateClientMTLSDomainSettings mirrors api.py:update_domain_settings (PUT
// /projects/:projectId/domains/:domainId/settings). A zero-value
// services.UpdateDomainSettingsInput{} resets/deletes the domain's
// ClientTrafficPolicy and settings row entirely.
func updateClientMTLSDomainSettings(ctx context.Context, projectID, domainID string, input services.UpdateDomainSettingsInput) (models.DomainSettingsConfig, error) {
	var out clientMTLSDomainSettingsEnvelope
	path := "/projects/" + projectID + "/domains/" + domainID + "/settings"
	if _, err := env.Admin.Do(ctx, http.MethodPut, path, input, &out); err != nil {
		return models.DomainSettingsConfig{}, err
	}
	return out.Settings, nil
}

// addClientMTLSDomainCA mirrors api.py:add_domain_mtls_ca (POST
// /projects/:projectId/domains/:domainId/settings/mtls/ca). Callers must
// have already enabled MTLS via updateClientMTLSDomainSettings first.
func addClientMTLSDomainCA(ctx context.Context, projectID, domainID, name, caPEM string) (models.DomainSettingsConfig, error) {
	body := services.AddDomainMTLSCAInput{Name: name, CAPem: caPEM}
	var out clientMTLSDomainSettingsEnvelope
	path := "/projects/" + projectID + "/domains/" + domainID + "/settings/mtls/ca"
	if _, err := env.Admin.Do(ctx, http.MethodPost, path, body, &out); err != nil {
		return models.DomainSettingsConfig{}, err
	}
	return out.Settings, nil
}

// removeClientMTLSDomainCA mirrors api.py:remove_domain_mtls_ca (DELETE
// /projects/:projectId/domains/:domainId/settings/mtls/ca/:caId).
func removeClientMTLSDomainCA(ctx context.Context, projectID, domainID, caID string) error {
	path := "/projects/" + projectID + "/domains/" + domainID + "/settings/mtls/ca/" + caID
	_, err := env.Admin.Do(ctx, http.MethodDelete, path, nil, nil)
	return err
}

// cleanupClientMTLSDomainSettings mirrors domain_mtls_helpers_test.go's
// cleanupDomainMTLS: best-effort remove any leftover CAs, then reset domain
// settings to empty. Used both as a pre-cleanup (a previous crashed run may
// have left the shared domain's mTLS config dirty) and, via t.Cleanup, as
// this test's own teardown -- report=true there so a real leak still fails
// the test.
func cleanupClientMTLSDomainSettings(t *testing.T, report bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	settings, err := getClientMTLSDomainSettings(ctx, env.ProjectID, env.DomainID)
	if err == nil && settings.MTLS != nil {
		for _, ca := range settings.MTLS.CACerts {
			if err := removeClientMTLSDomainCA(ctx, env.ProjectID, env.DomainID, ca.ID); err != nil && report {
				t.Errorf("cleanup: remove domain mTLS CA %s: %v", ca.ID, err)
			}
		}
	}
	if _, err := updateClientMTLSDomainSettings(ctx, env.ProjectID, env.DomainID, services.UpdateDomainSettingsInput{}); err != nil && report {
		t.Errorf("cleanup: reset domain settings: %v", err)
	}
}

// --- client helpers (mirror e2e/suites/security/client_mode_helpers_test.go) ---

// createClientMTLSClient mirrors api.py:create_client (POST /clients), as
// env.Admin (client creation has no team-membership gate).
func createClientMTLSClient(ctx context.Context, name string, teamID uuid.UUID) (harness.Client, error) {
	body := clients.CreateClientInput{
		Name:         name,
		Description:  "E2E test",
		TeamID:       teamID,
		ContactName:  "test",
		ContactEmail: "test@test.com",
	}
	return env.Admin.CreateClient(ctx, body)
}

// deleteClientMTLSClient mirrors api.py:delete_client (DELETE /clients/:clientId).
func deleteClientMTLSClient(ctx context.Context, clientID string) error {
	_, err := env.Admin.Do(ctx, http.MethodDelete, "/clients/"+clientID, nil, nil)
	return err
}

// cleanupClientMTLSClient registers a t.Cleanup that deletes clientID,
// reporting (not aborting) on failure -- mirrors client_mode_helpers_test.go's
// cleanupClient.
func cleanupClientMTLSClient(t *testing.T, clientID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := deleteClientMTLSClient(ctx, clientID); err != nil {
			t.Errorf("cleanup: delete client %s: %v", clientID, err)
		}
	})
}

// attachClientAndDeploy mirrors conftest.py:attach_and_deploy /
// client_mode_helpers_test.go's attachAndDeploy: attach a client to routeID
// as editor, approve the resulting pending client approval as admin (owner
// bypasses team-membership checks), then redeploy the route as editor so
// the SecurityPolicy/ClientTrafficPolicy implied by the attachment (and, in
// this test, by the managed-cert-derived MTLS* fields already written onto
// the client row -- see AttachCertificate's own doc comment in
// internal/services/clients/client_certificate.go, "the CA Secret and
// ClientTrafficPolicy it implies materialize at the next domain deploy")
// are actually applied.
func attachClientAndDeploy(ctx context.Context, routeID string, input clients.AttachFromRouteInput) (models.ClientRouteAttachment, error) {
	attachment, err := env.Editor.AttachClient(ctx, env.ProjectID, env.DomainID, routeID, input)
	if err != nil {
		return models.ClientRouteAttachment{}, fmt.Errorf("attach client: %w", err)
	}
	if err := env.Admin.ApproveClientAttachment(ctx, env.ProjectID, attachment.ID.String()); err != nil {
		return attachment, fmt.Errorf("approve client attachment: %w", err)
	}
	if err := env.Editor.DeployRoute(ctx, env.ProjectID, env.DomainID, routeID); err != nil {
		return attachment, fmt.Errorf("deploy route after attach: %w", err)
	}
	return attachment, nil
}

// --- route helpers (mirror e2e/suites/security/main_test.go) ---

// clientMTLSUniquePath mirrors main_test.go's uniquePath: a route name (via
// harness.UniqueName) and the "/"-prefixed gateway path derived from it, so
// this test's traffic is unambiguous against any concurrently running
// suite sharing the same domain.
func clientMTLSUniquePath(t *testing.T) (name, path string) {
	t.Helper()
	name = harness.UniqueName(t)
	return name, "/" + name
}

// clientMTLSRewriteTo mirrors main_test.go's rewriteTo: rewrites the
// route's matched prefix to backendPath before forwarding to nginx-service,
// which only ever serves "/". Without this, a request to this test's
// randomly-named path would get nginx's own legitimate 404, indistinguishable
// from "route not programmed yet".
func clientMTLSRewriteTo(backendPath string) *models.URLRewrite {
	return &models.URLRewrite{
		Path: &models.PathRewrite{
			Type:               "ReplacePrefixMatch",
			ReplacePrefixMatch: backendPath,
		},
	}
}

// --- HTTP assertion helpers (mirror e2e/suites/security/main_test.go) ---

// waitForClientMTLSStatus mirrors main_test.go's waitForHTTPStatus: polls
// probe every 2s until it returns a response whose status is exactly one
// of want, or fails once timeout elapses. This is what makes the negative
// probe below trustworthy -- see TestClientManagedCertMTLS's own doc
// comment for why the negative case is checked FIRST via this poll, not a
// single call.
func waitForClientMTLSStatus(
	ctx context.Context,
	probe func(context.Context) (*harness.Response, error),
	timeout time.Duration,
	want ...int,
) (*harness.Response, error) {
	isWant := func(code int) bool {
		for _, w := range want {
			if code == w {
				return true
			}
		}
		return false
	}

	deadline := time.Now().Add(timeout)
	var last *harness.Response
	var lastErr error

	for time.Now().Before(deadline) {
		resp, err := probe(ctx)
		if err != nil {
			lastErr = err
		} else {
			last = resp
			if isWant(resp.StatusCode) {
				return resp, nil
			}
			lastErr = fmt.Errorf("got status %d, want one of %v (body: %s)", resp.StatusCode, want, clientMTLSTruncate(resp.Body, 300))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if last != nil {
		return last, fmt.Errorf("status did not settle to any of %v within %s: %w", want, timeout, lastErr)
	}
	return nil, fmt.Errorf("route did not become live within %s: %w", timeout, lastErr)
}

// requireClientMTLSStatus mirrors main_test.go's requireStatus: issues
// probe once (retrying only on transport-level errors, up to 3 attempts)
// and fails t immediately if the response status is not one of want. Call
// this only after the route's positive path has already been proven live.
func requireClientMTLSStatus(t *testing.T, ctx context.Context, probe func(context.Context) (*harness.Response, error), want ...int) *harness.Response {
	t.Helper()

	var resp *harness.Response
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = probe(ctx)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("request: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if err != nil {
		t.Fatalf("request failed after retries: %v", err)
	}
	for _, w := range want {
		if resp.StatusCode == w {
			return resp
		}
	}
	t.Fatalf("got status %d, want one of %v (body: %s)", resp.StatusCode, want, clientMTLSTruncate(resp.Body, 500))
	return nil
}

// clientMTLSTruncate trims b to at most n bytes for embedding in failure
// messages without flooding test output.
func clientMTLSTruncate(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// --- export-bundle and untrusted-cert helpers (new to this file; no security-suite equivalent) ---

// splitClientExportBundle splits the raw PEM bundle DownloadExport returns
// into its three parts. Per managed_certificate_handler.go's
// ExportDownload (body := key ++ leaf ++ caChain, all PEM-concatenated),
// decoding blocks in order gives: first block = the private key (Type ends
// in "PRIVATE KEY" -- CreateCertificateInput's default KeyAlgorithm is RSA,
// so in practice "RSA PRIVATE KEY"), second block = the leaf certificate
// (Type == "CERTIFICATE"), and every remaining CERTIFICATE block is the CA
// chain cert-manager appended. The leaf and key blocks are re-encoded on
// their own so they can be handed to harness.WithClientCert (which wants
// PEM text for exactly one cert + one key, not a bundle); the CA block(s)
// are also re-encoded so they can be registered as the domain's mTLS trust
// CA.
func splitClientExportBundle(t *testing.T, bundle []byte) (leafPEM, keyPEM, caPEM string) {
	t.Helper()

	var keyBlock, leafBlock *pem.Block
	var caBlocks []*pem.Block

	rest := bundle
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch {
		case strings.HasSuffix(block.Type, "PRIVATE KEY"):
			if keyBlock == nil {
				keyBlock = block
			}
		case block.Type == "CERTIFICATE":
			if leafBlock == nil {
				leafBlock = block
			} else {
				caBlocks = append(caBlocks, block)
			}
		}
	}

	if keyBlock == nil {
		t.Fatalf("export bundle: no private-key PEM block found")
	}
	if leafBlock == nil {
		t.Fatalf("export bundle: no leaf certificate PEM block found")
	}
	if len(caBlocks) == 0 {
		t.Fatalf("export bundle: no CA chain PEM block found")
	}

	leafPEM = string(pem.EncodeToMemory(leafBlock))
	keyPEM = string(pem.EncodeToMemory(keyBlock))

	var caBuf strings.Builder
	for _, b := range caBlocks {
		caBuf.Write(pem.EncodeToMemory(b))
	}
	caPEM = caBuf.String()

	return leafPEM, keyPEM, caPEM
}

// generateUnrelatedClientCert generates a fresh, self-signed ECDSA
// certificate/key pair that has nothing to do with the issuer used in this
// test -- not signed by it, not derived from any CSR the platform ever
// saw. Used for the "untrusted cert" negative case: presenting it proves
// the route denies a credential the attached client never registered, as
// opposed to merely denying "no cert at all".
func generateUnrelatedClientCert(t *testing.T, commonName string) (certPEM, keyPEM string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate unrelated client key: %v", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate unrelated client cert serial: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create unrelated client cert: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal unrelated client key: %v", err)
	}

	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}

// TestClientManagedCertMTLS is the Tier 2 "managed client cert mTLS" e2e
// test for the managed-certificate feature. It exercises the managed
// CLIENT-certificate path end to end:
//
//  1. Create + grant a self-signed CA issuer (reusing this package's Tier 1
//     helper, createSelfSignedCAIssuer).
//  2. Create a usage=client, keyMode=managed certificate, approve it if
//     gated, and wait for cert-manager to issue it.
//  3. Request an export, approve that export approval (ApproveAllStages
//     against the certificate ID also covers the export approval -- see
//     harness.API.RequestExport's own doc comment), download the bundle,
//     and split it into leaf/key/CA PEM.
//  4. Create a Client and call env.Editor.AttachClientCert(clientID,
//     certID) -- clients.ClientCertificateService.AttachCertificate derives
//     the client's MTLSCAPem from the issuer's own CA (read from the
//     control-plane cluster) and MTLSSANs from the certificate's
//     DNSNames/URISANs, replacing what
//     e2e/suites/security/client_mode_mtls_test.go's reference test does by
//     hand via its raw configureClientMTLS(...) call.
//  5. Create a route, attach the client to it with mTLS enforced, and
//     deploy -- exactly the reference test's attachAndDeploy step.
//  6. Prove over a REAL mTLS handshake that:
//     - no client cert at all -> 403 (checked FIRST; see below)
//     - the exported leaf+key -> 200
//     - an unrelated, self-signed client cert -> 403 (exact match)
//
// # Why domain mTLS must ALSO be configured here (the brief's "ruling")
//
// AttachCertificate only writes the client row's MTLS* fields; it never
// touches the domain. But domain mTLS is what makes Envoy's listener
// request a client certificate DURING THE TLS HANDSHAKE at all -- without
// it, no cert is ever solicited, so a WithClientCert probe would never even
// exercise the code path this test is trying to prove. So, mirroring
// TestClientModeMTLS exactly, this test separately enables OPTIONAL domain
// mTLS on env.DomainID and registers a domain trust CA. The CA used is the
// one extracted from THIS test's own export bundle (caPEM) -- which is the
// exact same CA AttachCertificate itself reads from the cluster and writes
// onto the client row (see clients.ClientCertificateService.
// readIssuerCAPEM / AttachCertificate), so the two configuration paths
// agree on the trust root by construction, not by coincidence.
//
// # Why domain mTLS stays "optional", and why the negative case still lands on 403
//
// Same reasoning as TestClientModeMTLS's package-level and test-level doc
// comments in e2e/suites/security: STRICT domain mTLS would fail the TLS
// handshake itself for every certless request on the SHARED
// "api.fastgateway.local" domain, breaking concurrent sibling suites.
// "optional" maps to ClientTrafficPolicy's clientValidation.optional, which
// Envoy implements as trust_chain_verification: ACCEPT_UNTRUSTED -- the
// handshake always succeeds regardless of what is (or isn't) presented.
// Enforcement of "this client's cert is required" therefore happens one
// layer up, at routing: the per-client route's XFCC header regex match
// (buildMTLSXFCCHeaderMatches in route_service.go) against this client's
// pinned SANs. No cert, or a cert whose SAN isn't on this client's
// whitelist, fails that match and falls through to deny-by-default -- HTTP
// 403 in both cases, never a transport-layer failure. This test therefore
// asserts app-layer 403 (via requireClientMTLSStatus, an exact-match
// assert) for the untrusted-cert case, not requireTLSFailure.
//
// # Why the negative (no-cert) case is checked BEFORE the positive case
//
// A 200 cannot prove the client attachment's SecurityPolicy has actually
// converged: an unpolicied route (attachment not yet programmed) also
// forwards straight to the backend and answers 200. HTTP 403 is the one
// status the unconverged state cannot produce, so gating on it first (via
// waitForClientMTLSStatus, a bounded poll) is what makes the subsequent
// positive assertion trustworthy, exactly per
// e2e/suites/security/main_test.go's package doc comment.
//
// # Why this test omits t.Parallel()
//
// It mutates env.DomainID's shared domain-level mTLS settings, exactly
// like TestClientModeMTLS, which every other suite/test targeting the same
// domain is exposed to. See that test's own doc comment (in
// e2e/suites/security/client_mode_mtls_test.go) for the full risk analysis
// of why that requires running strictly non-parallel; the same reasoning
// applies verbatim here.
func TestClientManagedCertMTLS(t *testing.T) {
	// Deliberately no t.Parallel() -- see the doc comment above.

	// Pre-cleanup: remove any domain mTLS config left behind by a previous
	// crashed run, mirroring TestClientModeMTLS's unconditional
	// cleanupDomainMTLS(t, false) call at the top of the test.
	cleanupClientMTLSDomainSettings(t, false)

	ctx, cancel := context.WithTimeout(context.Background(), clientMTLSRouteLiveTimeout+3*time.Minute)
	defer cancel()

	// Step 1: issuer.
	issuerName := harness.UniqueName(t)
	issuerID := createSelfSignedCAIssuer(t, ctx, issuerName)
	if err := env.Admin.GrantIssuer(ctx, issuerID, env.ProjectID); err != nil {
		t.Fatalf("client managed cert mtls: grant issuer %s to project %s: %v", issuerID, env.ProjectID, err)
	}

	// Step 2: usage=client, keyMode=managed certificate.
	certName := harness.UniqueName(t)
	certBody := map[string]any{
		"name":     certName,
		"issuerId": issuerID,
		"usage":    "client",
		"keyMode":  "managed",
		"subject":  "e2e-client-managed-cert",
		"uriSans":  []string{"spiffe://fastgateway/client/e2e"},
	}
	certID, approvalID, err := env.Admin.CreateCertificate(ctx, env.ProjectID, certBody)
	if err != nil {
		t.Fatalf("client managed cert mtls: create certificate %q: %v", certName, err)
	}
	if certID == "" {
		t.Fatalf("client managed cert mtls: create certificate %q: response had no certificate id", certName)
	}
	if approvalID != "" {
		if err := env.Approver.ApproveAllStages(ctx, env.ProjectID, certID); err != nil {
			t.Fatalf("client managed cert mtls: approve certificate create %s: %v", certID, err)
		}
	}

	var lastCertStatus string
	pollUntil(t, ctx, certReadyTimeout, fmt.Sprintf("certificate %s becoming ready", certID), func() (bool, error) {
		status, err := env.Admin.CertificateStatus(ctx, env.ProjectID, certID)
		if err != nil {
			return false, err
		}
		lastCertStatus = status
		switch status {
		case "ready":
			return true, nil
		case "error":
			t.Fatalf("certificate %s entered error status", certID)
			return false, nil
		default:
			return false, fmt.Errorf("status %q", lastCertStatus)
		}
	})

	// Step 3: export -> approve -> download -> split.
	exportApprovalID, err := env.Admin.RequestExport(ctx, env.ProjectID, certID)
	if err != nil {
		t.Fatalf("client managed cert mtls: request export for certificate %s: %v", certID, err)
	}
	if exportApprovalID != "" {
		if err := env.Approver.ApproveAllStages(ctx, env.ProjectID, certID); err != nil {
			t.Fatalf("client managed cert mtls: approve export for certificate %s: %v", certID, err)
		}
	}

	bundle, err := env.Admin.DownloadExport(ctx, env.ProjectID, certID)
	if err != nil {
		t.Fatalf("client managed cert mtls: download export for certificate %s: %v", certID, err)
	}
	leafPEM, keyPEM, caPEM := splitClientExportBundle(t, bundle)

	// Step 4: domain mTLS trust = the issuer CA extracted from the export
	// bundle above. See the test's own doc comment for why this must be
	// configured separately from AttachClientCert below.
	if _, err := updateClientMTLSDomainSettings(ctx, env.ProjectID, env.DomainID, services.UpdateDomainSettingsInput{
		MTLS: &models.DomainMTLSConfig{Enabled: true, Optional: true},
	}); err != nil {
		t.Fatalf("client managed cert mtls: enable optional domain mTLS: %v", err)
	}
	if _, err := addClientMTLSDomainCA(ctx, env.ProjectID, env.DomainID, "managed client cert issuer CA", caPEM); err != nil {
		t.Fatalf("client managed cert mtls: add domain mTLS CA: %v", err)
	}
	t.Cleanup(func() { cleanupClientMTLSDomainSettings(t, true) })

	// Step 5: Client + managed-cert trust derivation.
	teamID, err := uuid.Parse(env.TeamID)
	if err != nil {
		t.Fatalf("client managed cert mtls: parse team ID %q: %v", env.TeamID, err)
	}
	client, err := createClientMTLSClient(ctx, harness.UniqueName(t), teamID)
	if err != nil {
		t.Fatalf("client managed cert mtls: create client: %v", err)
	}
	cleanupClientMTLSClient(t, client.ID.String())

	// AttachClientCert REPLACES the reference test's raw
	// configureClientMTLS(...) call: it derives the same MTLSEnabled/
	// MTLSCAPem/MTLSSANs fields, but from the managed certificate and its
	// issuer instead of hand-supplied values. Per PUT
	// /clients/:id/certificate's handler (ClientCertificateHandler.
	// AttachCertificate), this gates on IsTeamMember with NO owner bypass,
	// so it must be called as env.Editor (a real "dev"-team member), not
	// env.Admin -- exactly like configureClientMTLS in the reference test.
	if err := env.Editor.AttachClientCert(ctx, client.ID.String(), certID); err != nil {
		t.Fatalf("client managed cert mtls: attach managed cert %s to client %s: %v", certID, client.ID, err)
	}

	// Step 6: route + client attachment + deploy.
	name, path := clientMTLSUniquePath(t)
	cfg := services.CreateRouteInput{
		Name:         name,
		SecurityMode: models.SecurityModeClient,
		TeamID:       teamID,
		Config: models.RouteConfig{
			RouteType:            models.RouteTypeBackend,
			DefaultTrafficPolicy: models.DefaultTrafficPolicyDeny,
			Matches: []models.RouteMatch{
				{Path: &models.PathMatch{Type: "Prefix", Value: path}},
			},
			Backends: []models.RouteBackend{
				{Type: models.BackendTypeKubernetes, Namespace: clientMTLSBackendNamespace, Service: clientMTLSNginxService, Port: clientMTLSNginxPort, Weight: 100},
			},
			URLRewrite: clientMTLSRewriteTo("/"),
		},
	}
	fx := harness.NewFixture(t, env)
	route := fx.Route(cfg)

	if _, err := attachClientAndDeploy(ctx, route.ID.String(), clients.AttachFromRouteInput{
		ClientID:   client.ID,
		EnableMTLS: true,
	}); err != nil {
		t.Fatalf("client managed cert mtls: attach client to route: %v", err)
	}

	// Step 7a: NEGATIVE first -- no certificate at all, but a real
	// x-client-id. See the test's own doc comment for why 403 (not 200) is
	// the only sound convergence gate here.
	noCertProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-client-id", client.ID.String()))
	}
	if _, err := waitForClientMTLSStatus(ctx, noCertProbe, clientMTLSRouteLiveTimeout, 403); err != nil {
		t.Fatalf("client managed cert mtls: with no client cert: %v", err)
	}

	// Step 7b: POSITIVE -- the exact leaf+key this test exported must be
	// accepted, now that 403 above has already proven the attachment is
	// genuinely enforcing.
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path,
			harness.WithHeader("x-client-id", client.ID.String()),
			harness.WithClientCert(leafPEM, keyPEM),
		)
	}
	if _, err := waitForClientMTLSStatus(ctx, allowProbe, clientMTLSRouteLiveTimeout, 200); err != nil {
		t.Fatalf("client managed cert mtls: with the exported client cert + client id: %v", err)
	}

	// Step 7c: NEGATIVE -- an unrelated, self-signed client certificate
	// (never issued by this test's issuer, never registered on this
	// client). Under optional domain mTLS the handshake itself still
	// succeeds (ACCEPT_UNTRUSTED accepts any cert), but its SAN does not
	// satisfy this client's XFCC header match, so the per-client route
	// fails to match and the request falls through to deny-by-default --
	// 403, exact match, checked via requireClientMTLSStatus since the
	// route is already proven live by 7a/7b above.
	untrustedCertPEM, untrustedKeyPEM := generateUnrelatedClientCert(t, "untrusted-e2e-client")
	untrustedProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path,
			harness.WithHeader("x-client-id", client.ID.String()),
			harness.WithClientCert(untrustedCertPEM, untrustedKeyPEM),
		)
	}
	requireClientMTLSStatus(t, ctx, untrustedProbe, 403)
}
