package services

import (
	"github.com/google/uuid"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// EnrichedCertificate is a ManagedCertificate joined with its distribution
// sync state, referencing domains, and issuer name/type — for the project and
// fleet visibility views. No secret material.
type EnrichedCertificate struct {
	Certificate  models.ManagedCertificate
	IssuerName   string
	IssuerType   string
	Distribution *models.CertificateDistribution // nil if never distributed
	Domains      []models.Domain                 // referencing domains (empty if none)
}

// buildEnrichedCertificates joins a page of certs with their distribution rows,
// referencing domains, and issuers using in-memory maps — the caller fetches
// each relation in ONE batch query, so this does no per-cert lookups.
func buildEnrichedCertificates(
	certs []models.ManagedCertificate,
	dists []models.CertificateDistribution,
	domains []models.Domain,
	issuers []models.CertificateIssuer,
) []EnrichedCertificate {
	// Build distByCert := map[uuid.UUID]*models.CertificateDistribution
	// Keyed by dist.ManagedCertificateID (1:1 per cert)
	// Use loop-local copy to avoid the classic Go loop-variable aliasing bug
	distByCert := make(map[uuid.UUID]*models.CertificateDistribution)
	for i := range dists {
		d := dists[i] // Loop-local copy
		distByCert[d.ManagedCertificateID] = &d
	}

	// Build domainsByCert := map[uuid.UUID][]models.Domain
	// Append each domain under *domain.ManagedCertificateID (the FK is *uuid.UUID)
	// Skip any domain whose ManagedCertificateID is nil (defensively)
	domainsByCert := make(map[uuid.UUID][]models.Domain)
	for _, domain := range domains {
		if domain.ManagedCertificateID != nil {
			certID := *domain.ManagedCertificateID
			domainsByCert[certID] = append(domainsByCert[certID], domain)
		}
	}

	// Build issuerByID := map[uuid.UUID]models.CertificateIssuer
	// Keyed by issuer.ID
	issuerByID := make(map[uuid.UUID]models.CertificateIssuer)
	for _, issuer := range issuers {
		issuerByID[issuer.ID] = issuer
	}

	// For each cert in certs (preserve order), assemble an EnrichedCertificate
	result := make([]EnrichedCertificate, 0, len(certs))
	for _, cert := range certs {
		enriched := EnrichedCertificate{
			Certificate:  cert,
			Distribution: distByCert[cert.ID],    // nil if absent
			Domains:      domainsByCert[cert.ID], // nil/empty if absent
		}

		// Resolve issuer name/type from issuerByID[cert.IssuerID]
		// Empty strings if the issuer isn't in the list (defensive, don't panic)
		if issuer, ok := issuerByID[cert.IssuerID]; ok {
			enriched.IssuerName = issuer.Name
			enriched.IssuerType = string(issuer.Type)
		}

		result = append(result, enriched)
	}

	return result
}
