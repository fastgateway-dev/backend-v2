//go:build e2e && certe2e

package certificate

import (
	"context"
	"crypto/x509"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/e2e/harness"
)

const (
	// issuerReadyTimeout bounds how long a freshly created self-signed CA
	// issuer takes to reconcile: CertificateIssuerService.Create applies a
	// cert-manager Issuer and a Certificate CR, and cert-manager
	// itself has to notice, sign, and write the resulting CA Secret before
	// IssuerStatus reports "ready".
	issuerReadyTimeout = 60 * time.Second

	// certReadyTimeout bounds how long a freshly created managed
	// certificate takes to actually issue: cert-manager has to create a
	// CertificateRequest against the Issuer, get it signed, and
	// write the leaf Secret before CertificateStatus reports "ready".
	// Longer than issuerReadyTimeout since it depends on the issuer already
	// being ready plus its own signing round-trip.
	certReadyTimeout = 90 * time.Second

	// leafSecretTimeout bounds how long, after AttachServerCertToDomain,
	// the leaf Secret cert-manager wrote for the certificate takes to
	// actually exist (it should already exist by the time the cert is
	// "ready", but this is a belt-and-suspenders confirmation of
	// distribution before asserting anything about the data plane).
	leafSecretTimeout = 60 * time.Second

	// gatewayServingTimeout bounds how long Envoy takes to reprogram its
	// listener with the newly attached certificate after
	// AttachServerCertToDomain returns. This is a genuinely asynchronous
	// step (SDS/xDS push to the Envoy proxy pods), so the first few
	// handshake attempts are expected to still see the domain's previous
	// (or absent) certificate.
	gatewayServingTimeout = 60 * time.Second

	pollInterval = 2 * time.Second
)

// pollUntil calls fn every pollInterval until it returns true, or fails t
// once deadline elapses. fn's error (if any) is folded into the timeout
// failure message so the last observed failure reason is visible; it is
// never fatal on its own, since an error mid-poll (e.g. "certificate not
// found yet") is normally just the resource not existing yet.
func pollUntil(t *testing.T, ctx context.Context, timeout time.Duration, what string, fn func() (bool, error)) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ok, err := fn()
		if err != nil {
			lastErr = err
		} else if ok {
			return
		} else {
			lastErr = nil
		}

		select {
		case <-ctx.Done():
			t.Fatalf("%s: context done while polling: %v", what, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
	t.Fatalf("%s: did not succeed within %s (last error: %v)", what, timeout, lastErr)
}

// createSelfSignedCAIssuer creates a platform-global self-signed CA issuer
// as env.Admin (the seeded owner user -- CertificateIssuerHandler.Create is
// owner-only) and polls IssuerStatus until cert-manager reports it "ready".
// Returns the new issuer's ID.
//
// This is a suite-local helper, not a harness.API method: unlike
// CreateIssuer/IssuerStatus (Task 3's thin, reusable HTTP wrappers), this
// bakes in the self-signed-CA request shape and a specific polling policy,
// which only this Tier 1 happy-path flow needs so far.
func createSelfSignedCAIssuer(t *testing.T, ctx context.Context, name string) string {
	t.Helper()

	body := map[string]any{
		"type":       "self_signed_ca",
		"name":       name,
		"commonName": "e2e Test CA",
	}
	created, err := env.Admin.CreateIssuer(ctx, body)
	if err != nil {
		t.Fatalf("create self-signed CA issuer %q: %v", name, err)
	}
	if created.ID == "" {
		t.Fatalf("create self-signed CA issuer %q: response had no id", name)
	}

	var lastStatus, lastMessage string
	pollUntil(t, ctx, issuerReadyTimeout, fmt.Sprintf("issuer %s (%s) becoming ready", name, created.ID), func() (bool, error) {
		status, message, err := env.Admin.IssuerStatus(ctx, created.ID)
		if err != nil {
			return false, err
		}
		lastStatus, lastMessage = status, message
		switch status {
		case "ready":
			return true, nil
		case "error":
			t.Fatalf("issuer %s (%s) entered error status: %s", name, created.ID, message)
			return false, nil
		default:
			return false, fmt.Errorf("status %q: %s", lastStatus, lastMessage)
		}
	})

	return created.ID
}

// TestServerManagedCertServedByGateway is the Tier 1 "server happy path"
// e2e test for the managed-certificate feature: it drives a server-usage,
// managed-key-mode certificate through its full real-world lifecycle --
// issuer creation, issuer grant, certificate creation and approval,
// issuance, and domain attachment -- and then proves, from OUTSIDE the
// cluster, that Envoy Gateway actually serves that exact leaf certificate
// (one that chains to the platform CA it was just issued from) for the
// domain's hostname. None of the API-level or Kubernetes-level assertions
// alone would catch a leaf that issues and attaches successfully in the
// control plane but never actually reaches the data plane -- only a real
// TLS handshake against the gateway does.
func TestServerManagedCertServedByGateway(t *testing.T) {
	// Bound the whole test so a black-holed TLS dial (VerifyServedBy /
	// TLSServedChain use this ctx) fails fast instead of blocking a poll
	// until the 30m suite timeout. The budget must exceed the sum of the
	// sequential phase timeouts below; the extra slack covers approval and
	// the API round-trips between them.
	ctx, cancel := context.WithTimeout(context.Background(),
		issuerReadyTimeout+certReadyTimeout+leafSecretTimeout+gatewayServingTimeout+2*time.Minute)
	defer cancel()
	hostname := env.Cfg.GatewayDomain

	// Step 1: create a self-signed CA issuer and wait for cert-manager to
	// report it ready.
	issuerName := harness.UniqueName(t)
	issuerID := createSelfSignedCAIssuer(t, ctx, issuerName)

	// Step 2: grant the issuer to the project so certificates in this
	// project may reference it.
	if err := env.Admin.GrantIssuer(ctx, issuerID, env.ProjectID); err != nil {
		t.Fatalf("grant issuer %s to project %s: %v", issuerID, env.ProjectID, err)
	}

	// Step 3: create a server-usage, managed-key-mode certificate for the
	// gateway's own hostname, approve it if the project gates certificate
	// creation behind an approval, then wait for cert-manager to issue it.
	certName := harness.UniqueName(t)
	certBody := map[string]any{
		"name":     certName,
		"issuerId": issuerID,
		"usage":    "server",
		"keyMode":  "managed",
		"dnsNames": []string{hostname},
	}
	certID, approvalID, err := env.Admin.CreateCertificate(ctx, env.ProjectID, certBody)
	if err != nil {
		t.Fatalf("create certificate %q: %v", certName, err)
	}
	if certID == "" {
		t.Fatalf("create certificate %q: response had no certificate id", certName)
	}

	if approvalID != "" {
		// Approvals are enabled for this project: ApproveAllStages'
		// EntityID match is the certificate ID, and the create approval's
		// EntityID is exactly that (ManagedCertificateService.Create), so
		// this approves the pending create stage(s).
		if err := env.Approver.ApproveAllStages(ctx, env.ProjectID, certID); err != nil {
			t.Fatalf("approve certificate %s: %v", certID, err)
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

	// Step 4: attach the now-ready certificate to the seeded domain.
	if err := env.Admin.AttachServerCertToDomain(ctx, env.ProjectID, env.DomainID, certID); err != nil {
		t.Fatalf("attach certificate %s to domain %s: %v", certID, env.DomainID, err)
	}

	// Step 5: confirm cert-manager actually distributed the leaf Secret
	// into the cluster (SecretName == "cert-"+certID, per
	// ManagedCertificateService.Create's Config.SecretName derivation)
	// before asserting anything about the data plane.
	pollUntil(t, ctx, leafSecretTimeout, fmt.Sprintf("leaf secret for certificate %s existing", certID), func() (bool, error) {
		_, err := env.Kube.ReadSecretKey(ctx, env.Cfg.Namespace, "cert-"+certID, "tls.crt")
		if err != nil {
			return false, err
		}
		return true, nil
	})

	// Read the issuer's own CA certificate (CASecretName == "ca-"+issuerID,
	// per CertificateIssuerService.Create's Config.CASecretName derivation)
	// to build the trust root the served leaf must chain to.
	caBytes, err := env.Kube.ReadSecretKey(ctx, env.Cfg.Namespace, "ca-"+issuerID, "ca.crt")
	if err != nil {
		t.Fatalf("read CA secret for issuer %s: %v", issuerID, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		t.Fatalf("issuer %s CA secret did not contain a parseable PEM certificate", issuerID)
	}

	// Step 6: retry the real TLS handshake against the gateway until it
	// succeeds against the platform CA -- Envoy needs time to reprogram
	// its listener after the attach above, so the first several attempts
	// are expected to fail.
	var lastVerifyErr error
	pollUntil(t, ctx, gatewayServingTimeout, fmt.Sprintf("gateway serving a certificate trusted by issuer %s's CA for %s", issuerID, hostname), func() (bool, error) {
		if err := env.GW.VerifyServedBy(ctx, hostname, pool); err != nil {
			lastVerifyErr = err
			return false, err
		}
		return true, nil
	})
	if lastVerifyErr != nil {
		t.Logf("gateway eventually served a chain trusted by the platform CA (last transient error before success: %v)", lastVerifyErr)
	}

	// Finally, confirm the served leaf is actually the certificate for
	// this hostname (and not, say, some other cert that happens to also
	// chain to the same CA): its DNS SANs (or CommonName, as a fallback)
	// must include hostname.
	chain, err := env.GW.TLSServedChain(ctx, hostname)
	if err != nil {
		t.Fatalf("fetch gateway's served TLS chain for %s: %v", hostname, err)
	}
	if len(chain) == 0 {
		t.Fatalf("gateway served an empty certificate chain for %s", hostname)
	}
	leaf := chain[0]

	matchesHostname := false
	for _, dnsName := range leaf.DNSNames {
		if strings.EqualFold(dnsName, hostname) {
			matchesHostname = true
			break
		}
	}
	if !matchesHostname && strings.EqualFold(leaf.Subject.CommonName, hostname) {
		matchesHostname = true
	}
	if !matchesHostname {
		t.Fatalf("served leaf for %s does not identify that hostname (DNSNames=%v, CommonName=%q)",
			hostname, leaf.DNSNames, leaf.Subject.CommonName)
	}
}
