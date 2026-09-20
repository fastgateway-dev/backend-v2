package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// ManagedCertificateHandler exposes project-scoped management of managed
// certificates. Responses only ever carry certificate metadata --
// managedCertificateResponse deliberately omits models.ManagedCertificate's
// Config (DNS names are surfaced separately; the cert-manager Secret/
// Certificate resource names it also holds are internal plumbing) and
// CreatedBy, and no endpoint here ever returns key or certificate material:
// that lives in a cert-manager-managed Kubernetes Secret, never in this row.
type ManagedCertificateHandler struct {
	service      ManagedCertificateServiceInterface
	permChecker  *middleware.PermissionChecker
	auditService AuditServiceInterface
}

// NewManagedCertificateHandler creates a new managed certificate handler.
func NewManagedCertificateHandler(service ManagedCertificateServiceInterface, permChecker *middleware.PermissionChecker, auditService AuditServiceInterface) *ManagedCertificateHandler {
	return &ManagedCertificateHandler{
		service:      service,
		permChecker:  permChecker,
		auditService: auditService,
	}
}

// managedCertificateResponse carries only certificate metadata -- never key
// or certificate material.
type managedCertificateResponse struct {
	ID            uuid.UUID                 `json:"id"`
	ProjectID     uuid.UUID                 `json:"projectId"`
	Name          string                    `json:"name"`
	IssuerID      uuid.UUID                 `json:"issuerId"`
	Usage         models.ManagedCertUsage   `json:"usage"`
	DNSNames      []string                  `json:"dnsNames,omitempty"`
	Status        models.ManagedCertStatus  `json:"status"`
	StatusMessage string                    `json:"statusMessage,omitempty"`
	Fingerprint   string                    `json:"fingerprint,omitempty"`
	NotAfter      *time.Time                `json:"notAfter,omitempty"`
	CreatedAt     time.Time                 `json:"createdAt"`
	KeyMode       models.ManagedCertKeyMode `json:"keyMode,omitempty"`
	Subject       string                    `json:"subject,omitempty"`
	URISANs       []string                  `json:"uriSans,omitempty"`

	// ExportAvailable reports whether the CURRENT caller has an approved,
	// unconsumed, unexpired export grant for this certificate -- per-cert
	// AND per-user, so the frontend can show Download instead of
	// Request-export. Create leaves this false (a freshly created
	// certificate has no grant yet); List/ListFleet/Get populate it from the
	// service.
	ExportAvailable bool `json:"exportAvailable"`
}

func toManagedCertificateResponse(c *models.ManagedCertificate) managedCertificateResponse {
	return managedCertificateResponse{
		ID:            c.ID,
		ProjectID:     c.ProjectID,
		Name:          c.Name,
		IssuerID:      c.IssuerID,
		Usage:         c.Usage,
		DNSNames:      c.Config.DNSNames,
		Status:        c.Status,
		StatusMessage: c.StatusMessage,
		Fingerprint:   c.Fingerprint,
		NotAfter:      c.NotAfter,
		CreatedAt:     c.CreatedAt,
		KeyMode:       c.Config.KeyMode,
		Subject:       c.Config.Subject,
		URISANs:       c.Config.URISANs,
	}
}

// domainSummaryResponse carries only a referencing domain's id and hostname
// -- never its TLS config, gateway wiring, or any other field.
type domainSummaryResponse struct {
	ID       uuid.UUID `json:"id"`
	Hostname string    `json:"hostname"`
}

// enrichedCertificateResponse extends managedCertificateResponse with the
// visibility fields Task 3/4 add: the resolved issuer name/type, the Phase
// 3a distribution sync state (nil if never distributed), and the domains
// referencing this certificate (id+hostname only -- no secret material). It
// is a separate type from managedCertificateResponse (rather than adding
// fields there) so Get/Create/Delete/Status's plain responses stay
// unchanged.
type enrichedCertificateResponse struct {
	managedCertificateResponse
	IssuerName   string                           `json:"issuerName"`
	IssuerType   string                           `json:"issuerType"`
	Distribution *certificateDistributionResponse `json:"distribution"`
	Domains      []domainSummaryResponse          `json:"domains"`
}

func toEnrichedCertificateResponse(e *services.EnrichedCertificate) enrichedCertificateResponse {
	domains := make([]domainSummaryResponse, 0, len(e.Domains))
	for _, d := range e.Domains {
		domains = append(domains, domainSummaryResponse{ID: d.ID, Hostname: d.Hostname})
	}

	var dist *certificateDistributionResponse
	if e.Distribution != nil {
		converted := toCertificateDistributionResponse(e.Distribution)
		dist = &converted
	}

	resp := enrichedCertificateResponse{
		managedCertificateResponse: toManagedCertificateResponse(&e.Certificate),
		IssuerName:                 e.IssuerName,
		IssuerType:                 e.IssuerType,
		Distribution:               dist,
		Domains:                    domains,
	}
	resp.ExportAvailable = e.ExportAvailable
	return resp
}

// List lists managed certificates for a project, enriched with each
// certificate's resolved issuer name/type, distribution sync state, and
// referencing domains. Gated by certificate.view (Ruling: previously this
// endpoint was reachable by anyone with any project access via the
// group-level RequireProjectAccess() middleware; a project member in a
// preset lacking certificate.view now loses list access -- PresetViewer
// already includes it, so ordinary viewers are unaffected).
func (h *ManagedCertificateHandler) List(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	if !h.permChecker.CanViewCertificates(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: insufficient permissions to view certificates"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}

	filter := repository.CertificateListFilter{
		Status: c.Query("status"),
		Usage:  c.Query("usage"),
	}

	if issuerIDStr := c.Query("issuerId"); issuerIDStr != "" {
		issuerID, err := uuid.Parse(issuerIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issuerId"})
			return
		}
		filter.IssuerID = &issuerID
	}

	if expiresBeforeStr := c.Query("expiresBefore"); expiresBeforeStr != "" {
		expiresBefore, err := time.Parse(time.RFC3339, expiresBeforeStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid expiresBefore: must be RFC3339"})
			return
		}
		filter.ExpiresBefore = &expiresBefore
	}

	enriched, total, err := h.service.ListProjectCertificatesEnriched(projectID, page, limit, filter, user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := make([]enrichedCertificateResponse, 0, len(enriched))
	for i := range enriched {
		resp = append(resp, toEnrichedCertificateResponse(&enriched[i]))
	}

	c.JSON(http.StatusOK, gin.H{
		"data": resp,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// ListFleet lists managed certificates across ALL projects, enriched the
// same way as List (resolved issuer name/type, distribution sync state, and
// referencing domains -- the enriched DTO already carries projectId, which
// is what makes cross-project rows identifiable here). Owner-only: gated by
// the RequireRole("owner") middleware on the route group in main.go, not by
// a per-handler permission check -- there is no project in scope to check
// permissions against. Unlike List, the projectId filter is read from the
// query string (rather than fixed by a path param) so an owner can narrow
// the fleet view to one project.
func (h *ManagedCertificateHandler) ListFleet(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}

	filter := repository.CertificateListFilter{
		Status: c.Query("status"),
		Usage:  c.Query("usage"),
	}

	if issuerIDStr := c.Query("issuerId"); issuerIDStr != "" {
		issuerID, err := uuid.Parse(issuerIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issuerId"})
			return
		}
		filter.IssuerID = &issuerID
	}

	if projectIDStr := c.Query("projectId"); projectIDStr != "" {
		projectID, err := uuid.Parse(projectIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid projectId"})
			return
		}
		filter.ProjectID = &projectID
	}

	if expiresBeforeStr := c.Query("expiresBefore"); expiresBeforeStr != "" {
		expiresBefore, err := time.Parse(time.RFC3339, expiresBeforeStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid expiresBefore: must be RFC3339"})
			return
		}
		filter.ExpiresBefore = &expiresBefore
	}

	enriched, total, err := h.service.ListFleetCertificates(page, limit, filter, user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := make([]enrichedCertificateResponse, 0, len(enriched))
	for i := range enriched {
		resp = append(resp, toEnrichedCertificateResponse(&enriched[i]))
	}

	c.JSON(http.StatusOK, gin.H{
		"data": resp,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// Create creates a new managed certificate. The response's approvalId is
// nil when the project has approvals disabled and the certificate was
// issued via the fast path.
func (h *ManagedCertificateHandler) Create(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	if !h.permChecker.CanCreateCertificates(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only editors can create certificates"})
		return
	}

	var input services.CreateCertificateInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cert, approval, err := h.service.Create(projectID, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	details := middleware.AuditDetails(c)
	details["approvalEntityType"] = "certificate"
	var approvalID *uuid.UUID
	statusCode := http.StatusCreated
	if approval != nil {
		id := approval.ID
		approvalID = &id
		details["approvalId"] = approval.ID
		statusCode = http.StatusAccepted
	}

	h.auditService.LogAction(
		&projectID,
		user,
		"create",
		"certificate",
		&cert.ID,
		cert.Name,
		details,
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(statusCode, gin.H{
		"certificate": toManagedCertificateResponse(cert),
		"approvalId":  approvalID,
	})
}

// Get returns a single managed certificate.
func (h *ManagedCertificateHandler) Get(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	id, err := uuid.Parse(c.Param("certificateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid certificate ID"})
		return
	}

	cert, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// A certificate belonging to a different project must 404, not just be
	// denied -- 404 (rather than 403) avoids confirming that a certificate
	// with this ID exists at all in another project the caller has no
	// business knowing about.
	if cert.ProjectID != projectID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
		return
	}

	resp := toManagedCertificateResponse(cert)

	user := middleware.GetCurrentUser(c)
	if user != nil {
		exportAvailable, err := h.service.HasUsableExportGrant(cert.ID, user.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		resp.ExportAvailable = exportAvailable
	}

	c.JSON(http.StatusOK, resp)
}

// Delete deletes a managed certificate.
func (h *ManagedCertificateHandler) Delete(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	if !h.permChecker.CanManageCertificates(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can delete certificates"})
		return
	}

	id, err := uuid.Parse(c.Param("certificateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid certificate ID"})
		return
	}

	cert, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// See Get's comment: a cross-project delete must 404, not proceed.
	if cert.ProjectID != projectID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
		return
	}

	if err := h.service.Delete(id); err != nil {
		if errors.Is(err, services.ErrCertificateInUse) {
			c.JSON(http.StatusConflict, gin.H{"error": "certificate is attached to one or more domains or clients; detach it first"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.auditService.LogAction(
		&projectID,
		user,
		"delete",
		"certificate",
		&id,
		cert.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// Status reports the certificate's live cert-manager issuance status.
func (h *ManagedCertificateHandler) Status(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	id, err := uuid.Parse(c.Param("certificateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid certificate ID"})
		return
	}

	// Status only takes an id, so the ownership check needs its own lookup
	// before calling it -- see Get's comment on why this 404s rather than
	// proceeding or 403ing.
	cert, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cert.ProjectID != projectID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
		return
	}

	status, err := h.service.Status(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, status)
}

// certificateDistributionResponse carries only the Phase 3a distribution
// state -- status, the (already-public) fingerprint hash, a human-readable
// message, and when it last synced. Never key or certificate material.
type certificateDistributionResponse struct {
	Status                models.CertDistStatus `json:"status"`
	LastPushedFingerprint string                `json:"lastPushedFingerprint,omitempty"`
	Message               string                `json:"message,omitempty"`
	LastSyncedAt          *time.Time            `json:"lastSyncedAt,omitempty"`
}

func toCertificateDistributionResponse(d *models.CertificateDistribution) certificateDistributionResponse {
	return certificateDistributionResponse{
		Status:                d.Status,
		LastPushedFingerprint: d.LastPushedFingerprint,
		Message:               d.Message,
		LastSyncedAt:          d.LastSyncedAt,
	}
}

// Distribution reports the certificate's Phase 3a distribution state --
// how far the certdist controller has gotten pushing this certificate's
// Secret into the project's tenant cluster.
func (h *ManagedCertificateHandler) Distribution(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	id, err := uuid.Parse(c.Param("certificateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid certificate ID"})
		return
	}

	// DistributionStatus only takes an id, so the ownership check needs its
	// own lookup first -- see Get's comment on why this 404s rather than
	// proceeding or 403ing.
	cert, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cert.ProjectID != projectID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
		return
	}

	dist, err := h.service.DistributionStatus(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, toCertificateDistributionResponse(dist))
}

// Resync forces the certdist controller to re-push the certificate's Secret
// on its next tick.
func (h *ManagedCertificateHandler) Resync(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	if !h.permChecker.CanEditCertificates(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only editors can resync certificates"})
		return
	}

	id, err := uuid.Parse(c.Param("certificateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid certificate ID"})
		return
	}

	cert, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// See Get's comment: a cross-project resync must 404, not proceed.
	if cert.ProjectID != projectID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
		return
	}

	if err := h.service.Resync(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.auditService.LogAction(
		&projectID,
		user,
		"resync",
		"certificate",
		&id,
		cert.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusAccepted)
}

// RequestExport opens an export approval for a managed certificate: once
// approved, the requesting user gets a short-lived, single-use grant to
// download the certificate's private key material via ExportDownload. Only
// the export request is gated by permission here -- the real gate is the
// approval itself (and, once approved, the grant being bound to this one
// user). Gated by certificate.edit (CanEditCertificates): exporting key
// material is at least as sensitive as the resync/reissue actions that gate
// already covers, and reusing it avoids introducing a new permission for a
// single endpoint.
func (h *ManagedCertificateHandler) RequestExport(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	if !h.permChecker.CanEditCertificates(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only editors can request a certificate export"})
		return
	}

	id, err := uuid.Parse(c.Param("certificateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid certificate ID"})
		return
	}

	cert, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// See Get's comment: a cross-project export request must 404, not
	// proceed.
	if cert.ProjectID != projectID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
		return
	}

	approval, err := h.service.RequestExport(id, user.ID)
	if err != nil {
		if errors.Is(err, services.ErrExportNotApplicable) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, services.ErrCertificateNotReadyForExport) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	details := middleware.AuditDetails(c)
	details["approvalEntityType"] = "certificate"
	var approvalID *uuid.UUID
	if approval != nil {
		aid := approval.ID
		approvalID = &aid
		details["approvalId"] = approval.ID
	}

	h.auditService.LogAction(
		&projectID,
		user,
		"export_request",
		"certificate",
		&id,
		cert.Name,
		details,
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusAccepted, gin.H{"approvalId": approvalID})
}

// ExportDownload streams a certificate's private key material once, if (and
// only if) the calling user holds an approved, unconsumed, unexpired export
// grant for this certificate -- ExportBundle consumes it atomically before
// reading anything. Deliberately user-bound rather than a `?token=` query
// parameter: every call here is already authenticated, so putting a bearer
// secret in the URL (where it lands in server logs, browser history, and
// Referer headers) would only add risk with no offsetting benefit. A second
// download attempt (grant already consumed) 410s rather than 403/404 --
// distinct from "you never had access" -- so a legitimate caller knows to
// request a fresh export rather than re-authenticating.
func (h *ManagedCertificateHandler) ExportDownload(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	id, err := uuid.Parse(c.Param("certificateId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid certificate ID"})
		return
	}

	cert, err := h.service.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// See Get's comment: a cross-project download must 404, not proceed --
	// and critically must not even reach ExportBundle, which would consume
	// the caller's grant (if they somehow held one) against the wrong
	// certificate's response.
	if cert.ProjectID != projectID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
		return
	}

	leafPEM, keyPEM, caChainPEM, err := h.service.ExportBundle(id, user.ID)
	if err != nil {
		if errors.Is(err, repository.ErrExportGrantUnavailable) {
			c.JSON(http.StatusGone, gin.H{"error": "no approved export available; request export and get it approved"})
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.auditService.LogAction(
		&projectID,
		user,
		"export_download",
		"certificate",
		&id,
		cert.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	body := make([]byte, 0, len(keyPEM)+len(leafPEM)+len(caChainPEM))
	body = append(body, keyPEM...)
	body = append(body, leafPEM...)
	body = append(body, caChainPEM...)

	c.Header("Content-Disposition", `attachment; filename="`+cert.Name+`.pem"`)
	c.Data(http.StatusOK, "application/x-pem-file", body)
}

// IssuersForProject lists the certificate issuers granted to a project, for
// populating a certificate-create form's issuer picker.
func (h *ManagedCertificateHandler) IssuersForProject(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	issuers, err := h.service.IssuersForProject(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": issuers})
}
