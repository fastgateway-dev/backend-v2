//go:build e2e

package harness

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// --- Certificate issuers ---

// CreateIssuer creates a platform-global certificate issuer
// (CertificateIssuerHandler.Create), which returns the full
// models.CertificateIssuer as a bare JSON object. Only the id is decoded
// here since that's all callers need to chain into GrantIssuer/CreateCertificate.
func (a *API) CreateIssuer(ctx context.Context, body any) (created struct {
	ID string `json:"id"`
}, err error) {
	_, err = a.Do(ctx, http.MethodPost, "/certificates/issuers", body, &created)
	return created, err
}

// IssuerStatus polls a certificate issuer's live status
// (CertificateIssuerHandler.Status), which responds with a bare
// {"status", "statusMessage"} object -- not models.CertificateIssuer.
func (a *API) IssuerStatus(ctx context.Context, issuerID string) (status string, message string, err error) {
	var out struct {
		Status  string `json:"status"`
		Message string `json:"statusMessage"`
	}
	if _, err := a.Do(ctx, http.MethodGet, "/certificates/issuers/"+issuerID+"/status", nil, &out); err != nil {
		return "", "", err
	}
	return out.Status, out.Message, nil
}

// GrantIssuer grants issuerID to projectID (IssuerGrantHandler.Grant),
// which responds 201 with no body.
func (a *API) GrantIssuer(ctx context.Context, issuerID, projectID string) error {
	body := map[string]string{"projectId": projectID}
	_, err := a.Do(ctx, http.MethodPost, "/certificates/issuers/"+issuerID+"/grants", body, nil)
	return err
}

// --- Managed certificates ---

// CreateCertificate creates a project-scoped managed certificate
// (ManagedCertificateHandler.Create), which responds with
// {"certificate": managedCertificateResponse, "approvalId": *uuid|null}.
// approvalID comes back "" when the project has approvals disabled (the
// fast path issues immediately, mirroring RequestExport's fast path).
func (a *API) CreateCertificate(ctx context.Context, projectID string, body any) (certID string, approvalID string, err error) {
	var out struct {
		Certificate struct {
			ID string `json:"id"`
		} `json:"certificate"`
		ApprovalID *string `json:"approvalId"`
	}
	path := fmt.Sprintf("/projects/%s/certificates", projectID)
	if _, err := a.Do(ctx, http.MethodPost, path, body, &out); err != nil {
		return "", "", err
	}
	if out.ApprovalID != nil {
		approvalID = *out.ApprovalID
	}
	return out.Certificate.ID, approvalID, nil
}

// CertificateStatus reports a managed certificate's live cert-manager
// issuance status (ManagedCertificateHandler.Status -> services.CertStatus,
// serialized as {"status", "message", "notAfter"}).
func (a *API) CertificateStatus(ctx context.Context, projectID, certID string) (status string, err error) {
	var out struct {
		Status string `json:"status"`
	}
	path := fmt.Sprintf("/projects/%s/certificates/%s/status", projectID, certID)
	if _, err := a.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return "", err
	}
	return out.Status, nil
}

// AttachServerCertToDomain points a domain at a managed certificate
// (DomainHandler.AttachCertificate).
func (a *API) AttachServerCertToDomain(ctx context.Context, projectID, domainID, certID string) error {
	body := map[string]string{"certificateId": certID}
	path := fmt.Sprintf("/projects/%s/domains/%s/certificate", projectID, domainID)
	_, err := a.Do(ctx, http.MethodPut, path, body, nil)
	return err
}

// RequestExport opens an export approval for certID
// (ManagedCertificateHandler.RequestExport), which responds 202 with
// {"approvalId": *uuid|null}. approvalID comes back "" on the fast path
// (project has approvals disabled).
//
// NOTE for callers: the export approval's EntityID is the certificate ID
// (see ManagedCertificateService.RequestExport, which submits
// approvalpkg.Spec{EntityID: cert.ID, ...} exactly like Create's approval
// does), so the existing ApproveAllStages(ctx, projectID, certID) approves
// both the create approval and this export approval -- no separate
// "ApproveExport" helper is needed.
func (a *API) RequestExport(ctx context.Context, projectID, certID string) (approvalID string, err error) {
	var out struct {
		ApprovalID *string `json:"approvalId"`
	}
	path := fmt.Sprintf("/projects/%s/certificates/%s/export", projectID, certID)
	if _, err := a.Do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return "", err
	}
	if out.ApprovalID != nil {
		approvalID = *out.ApprovalID
	}
	return approvalID, nil
}

// DownloadExport downloads the consumed export grant's PEM bundle
// (ManagedCertificateHandler.ExportDownload -- key, leaf cert, then CA
// chain concatenated, Content-Type application/x-pem-file). Do's body
// capture/replace means the raw bytes are still readable off the response
// even though out is nil.
func (a *API) DownloadExport(ctx context.Context, projectID, certID string) (bundlePEM []byte, err error) {
	path := fmt.Sprintf("/projects/%s/certificates/%s/export/download", projectID, certID)
	resp, err := a.Do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(resp.Body)
}

// --- Client certificates ---

// AttachClientCert attaches a managed client-usage certificate to a
// client's mTLS configuration (ClientCertificateHandler.AttachCertificate).
func (a *API) AttachClientCert(ctx context.Context, clientID, certID string) error {
	body := map[string]string{"certificateId": certID}
	_, err := a.Do(ctx, http.MethodPut, "/clients/"+clientID+"/certificate", body, nil)
	return err
}

// --- CSR generation ---

// GenClientCSR generates a fresh ECDSA P-256 key and a PEM-encoded
// certificate signing request for it, with Subject.CommonName set to
// commonName and a single URI SAN parsed from uriSAN. Used for csr-mode
// client certificates, where the caller (not cert-manager) holds the
// private key and only asks the server to sign the CSR.
func GenClientCSR(commonName, uriSAN string) (csrPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ECDSA key: %w", err)
	}

	uri, err := url.Parse(uriSAN)
	if err != nil {
		return nil, nil, fmt.Errorf("parse URI SAN %q: %w", uriSAN, err)
	}

	template := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: commonName},
		URIs:    []*url.URL{uri},
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate request: %w", err)
	}
	csrPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal EC private key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return csrPEM, keyPEM, nil
}
