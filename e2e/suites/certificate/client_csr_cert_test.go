//go:build e2e

package certificate

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/fastgateway-dev/backend-v2/internal/services/clients"
)

// This file is the second half of Tier 2 of the e2e-coverage plan for the
// managed-certificate feature: "CSR-mode client cert mTLS". Where
// client_managed_cert_test.go's TestClientManagedCertMTLS proves the
// keyMode=managed path (cert-manager generates and holds the private key;
// the platform exports a bundle to the caller), this test proves the
// keyMode=csr path: the CALLER generates its own keypair and a certificate
// signing request, and only asks the platform to get the CSR signed. The
// platform (and cert-manager) never see the private key.
//
// The one API asymmetry this test exists to exercise: there is NO export
// endpoint for csr-mode certificates (ExportBundle is managed-only; a
// csr-mode export request 409s). See
// docs/superpowers/specs/2026-09-21-managed-certificate-e2e-design.md §10,
// "Known product gap (out of scope; flagged)" -- a real CSR-mode caller
// currently has no first-class API to fetch their own signed leaf. For this
// e2e test (as that spec directs), the signed leaf is instead read directly
// from the cluster's cert-manager CertificateRequest resource via
// env.Kube.ReadCertificateRequestSignedCert, exactly as Tier 1's
// TestServerManagedCertServedByGateway already reads a managed cert's leaf
// Secret directly from the cluster for its own data-plane assertions.
//
// All of the domain-mTLS, client, route, and HTTP-assertion helpers this
// test uses (cleanupClientMTLSDomainSettings, addClientMTLSDomainCA,
// createClientMTLSClient, cleanupClientMTLSClient, attachClientAndDeploy,
// clientMTLSUniquePath, clientMTLSRewriteTo, waitForClientMTLSStatus,
// requireClientMTLSStatus, generateUnrelatedClientCert, and the
// clientMTLS* constants) are package-level and already defined in
// client_managed_cert_test.go; pollUntil, createSelfSignedCAIssuer, and the
// timeout constants are already defined in server_managed_cert_test.go.
// Nothing is redefined here.
func TestClientCSRCertMTLS(t *testing.T) {
	// Deliberately no t.Parallel() -- this test mutates env.DomainID's
	// shared domain-level mTLS settings, exactly like
	// TestClientManagedCertMTLS (see that test's doc comment for the full
	// risk analysis of why that requires running strictly non-parallel).

	// Pre-cleanup: remove any domain mTLS config left behind by a previous
	// crashed run.
	cleanupClientMTLSDomainSettings(t, false)

	ctx, cancel := context.WithTimeout(context.Background(), clientMTLSRouteLiveTimeout+3*time.Minute)
	defer cancel()

	// Step 1: issuer. Client-usage certificates require a self-signed CA
	// issuer, same as the managed-key-mode test.
	issuerName := harness.UniqueName(t)
	issuerID := createSelfSignedCAIssuer(t, ctx, issuerName)
	if err := env.Admin.GrantIssuer(ctx, issuerID, env.ProjectID); err != nil {
		t.Fatalf("client csr cert mtls: grant issuer %s to project %s: %v", issuerID, env.ProjectID, err)
	}

	// Step 2: generate the client's own keypair + CSR. keyPEM is kept for
	// the rest of the test -- it is the private key the test will present
	// over mTLS; the platform and cert-manager never see it, only csrPEM.
	// uriSAN must be baked into the CSR itself (GenClientCSR sets it as the
	// CSR's URI SAN) AND passed as the certificate's uriSans below, since
	// AttachCertificate pins the XFCC SAN match from cert.Config.URISANs
	// and the signed leaf carries whatever SAN the CSR requested -- if the
	// two disagree, the mTLS probe below would 403 even with a validly
	// signed cert.
	const commonName = "e2e-client-csr-cert"
	const uriSAN = "spiffe://fastgateway/client/e2e-csr"
	csrPEM, keyPEM, err := harness.GenClientCSR(commonName, uriSAN)
	if err != nil {
		t.Fatalf("client csr cert mtls: generate client CSR: %v", err)
	}

	// Step 3: usage=client, keyMode=csr certificate. "csr" is the exact
	// JSON tag CreateCertificateInput.CSR expects.
	certName := harness.UniqueName(t)
	certBody := map[string]any{
		"name":     certName,
		"issuerId": issuerID,
		"usage":    "client",
		"keyMode":  "csr",
		"csr":      string(csrPEM),
		"subject":  commonName,
		"uriSans":  []string{uriSAN},
	}
	certID, approvalID, err := env.Admin.CreateCertificate(ctx, env.ProjectID, certBody)
	if err != nil {
		t.Fatalf("client csr cert mtls: create certificate %q: %v", certName, err)
	}
	if certID == "" {
		t.Fatalf("client csr cert mtls: create certificate %q: response had no certificate id", certName)
	}
	if approvalID != "" {
		if err := env.Approver.ApproveAllStages(ctx, env.ProjectID, certID); err != nil {
			t.Fatalf("client csr cert mtls: approve certificate create %s: %v", certID, err)
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

	// Step 4: retrieve the signed leaf from the cluster. There is no export
	// API for csr-mode certificates (see this file's package doc comment
	// and spec §10), so the signed leaf is read directly from the
	// cert-manager CertificateRequest CR that ManagedCertificateService
	// created for this certificate. Its name equals cert.Config.CertificateName,
	// which the service derives as "cert-"+certID (managed_certificate_service.go:577) --
	// the same naming convention Tier 1 already relies on for the leaf
	// Secret. .status.certificate can lag the certificate's "ready" status
	// by a moment, so this is polled rather than read once.
	var signedLeaf []byte
	pollUntil(t, ctx, certReadyTimeout, fmt.Sprintf("signed leaf for certificaterequest cert-%s existing", certID), func() (bool, error) {
		leaf, err := env.Kube.ReadCertificateRequestSignedCert(ctx, env.Cfg.Namespace, "cert-"+certID)
		if err != nil {
			return false, err
		}
		signedLeaf = leaf
		return true, nil
	})
	signedLeafPEM := string(signedLeaf)

	// Step 5: domain mTLS trust. Unlike the managed-key-mode test, there is
	// no export bundle to pull a CA chain out of, so the issuer's CA is
	// read directly from the cluster instead -- the same
	// "ca-"+issuerID/ca.crt convention Tier 1's TestServerManagedCertServedByGateway
	// already relies on for CertificateIssuerService.Create's
	// Config.CASecretName.
	caBytes, err := env.Kube.ReadSecretKey(ctx, env.Cfg.Namespace, "ca-"+issuerID, "ca.crt")
	if err != nil {
		t.Fatalf("client csr cert mtls: read CA secret for issuer %s: %v", issuerID, err)
	}

	// Enable optional domain mTLS and register that CA as a domain trust
	// anchor -- mirrors TestClientManagedCertMTLS's step 4 exactly (same
	// enable-then-add-CA sequence, same "optional" mode for the same
	// reasons: this is the SHARED "api.fastgateway.local" domain, and
	// STRICT mTLS would break concurrent sibling suites' certless
	// requests).
	if _, err := updateClientMTLSDomainSettings(ctx, env.ProjectID, env.DomainID, services.UpdateDomainSettingsInput{
		MTLS: &models.DomainMTLSConfig{Enabled: true, Optional: true},
	}); err != nil {
		t.Fatalf("client csr cert mtls: enable optional domain mTLS: %v", err)
	}
	if _, err := addClientMTLSDomainCA(ctx, env.ProjectID, env.DomainID, "csr client cert issuer CA", string(caBytes)); err != nil {
		t.Fatalf("client csr cert mtls: add domain mTLS CA: %v", err)
	}
	t.Cleanup(func() { cleanupClientMTLSDomainSettings(t, true) })

	// Step 6: Client + csr-cert trust derivation. AttachClientCert derives
	// the client's MTLSCAPem/MTLSSANs from the certificate and its issuer
	// exactly as it does for managed-key-mode certs -- the keyMode the
	// certificate was created with doesn't change how attachment works,
	// only how (and by whom) the private key is held. Per
	// ClientCertificateHandler.AttachCertificate this gates on
	// IsTeamMember with no owner bypass, so it must be called as
	// env.Editor.
	teamID, err := uuid.Parse(env.TeamID)
	if err != nil {
		t.Fatalf("client csr cert mtls: parse team ID %q: %v", env.TeamID, err)
	}
	client, err := createClientMTLSClient(ctx, harness.UniqueName(t), teamID)
	if err != nil {
		t.Fatalf("client csr cert mtls: create client: %v", err)
	}
	cleanupClientMTLSClient(t, client.ID.String())

	if err := env.Editor.AttachClientCert(ctx, client.ID.String(), certID); err != nil {
		t.Fatalf("client csr cert mtls: attach csr cert %s to client %s: %v", certID, client.ID, err)
	}

	// Step 7: route + client attachment + deploy -- identical shape to
	// TestClientManagedCertMTLS's step 6.
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
		t.Fatalf("client csr cert mtls: attach client to route: %v", err)
	}

	// Step 8a: NEGATIVE first -- no certificate at all, but a real
	// x-client-id. Gating on 403 (not 200) here is what proves the
	// attachment has actually converged before the positive assertion
	// below is trusted -- see TestClientManagedCertMTLS's doc comment for
	// the full reasoning (an unconverged/unpolicied route also answers 200,
	// so a bare 200 can't distinguish "enforcing" from "not yet
	// programmed").
	noCertProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path, harness.WithHeader("x-client-id", client.ID.String()))
	}
	if _, err := waitForClientMTLSStatus(ctx, noCertProbe, clientMTLSRouteLiveTimeout, 403); err != nil {
		t.Fatalf("client csr cert mtls: with no client cert: %v", err)
	}

	// Step 8b: POSITIVE -- the signed leaf retrieved from the cluster,
	// paired with the test's OWN private key (the platform never held it),
	// must be accepted now that 8a has already proven the attachment is
	// genuinely enforcing.
	allowProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path,
			harness.WithHeader("x-client-id", client.ID.String()),
			harness.WithClientCert(signedLeafPEM, string(keyPEM)),
		)
	}
	if _, err := waitForClientMTLSStatus(ctx, allowProbe, clientMTLSRouteLiveTimeout, 200); err != nil {
		t.Fatalf("client csr cert mtls: with the signed csr leaf + client id: %v", err)
	}

	// Step 8c: NEGATIVE -- an unrelated, self-signed client certificate
	// (never issued via this test's CSR, never registered on this client).
	// Same reasoning as TestClientManagedCertMTLS's step 7c: under optional
	// domain mTLS the handshake itself still succeeds, but the cert's SAN
	// doesn't satisfy this client's XFCC whitelist, so the per-client route
	// fails to match and falls through to deny-by-default -- 403.
	untrustedCertPEM, untrustedKeyPEM := generateUnrelatedClientCert(t, "untrusted-e2e-csr-client")
	untrustedProbe := func(ctx context.Context) (*harness.Response, error) {
		return env.GW.HTTP(ctx, "GET", path,
			harness.WithHeader("x-client-id", client.ID.String()),
			harness.WithClientCert(untrustedCertPEM, untrustedKeyPEM),
		)
	}
	requireClientMTLSStatus(t, ctx, untrustedProbe, 403)
}
