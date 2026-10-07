package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// DNSRecordHandler exposes project-scoped management of a domain's single
// FastGateway-managed DNS record (models.DomainDNSRecord). Every endpoint
// here is gated by canManageDomains, the same permission level domain
// settings/certificate/mTLS management already uses -- DNS record
// configuration is domain management, not a read-only view any project
// member should get.
type DNSRecordHandler struct {
	service      DNSRecordServiceInterface
	permChecker  *middleware.PermissionChecker
	auditService AuditServiceInterface
}

// NewDNSRecordHandler creates a new DNS record handler.
func NewDNSRecordHandler(service DNSRecordServiceInterface, permChecker *middleware.PermissionChecker, auditService AuditServiceInterface) *DNSRecordHandler {
	return &DNSRecordHandler{service: service, permChecker: permChecker, auditService: auditService}
}

// dnsRecordRequest is the create/update payload for a domain's DNS record.
type dnsRecordRequest struct {
	HostedZoneID *string `json:"hostedZoneId"`
	RecordType   string  `json:"recordType"`
	TTL          *int    `json:"ttl"`
	Proxied      bool    `json:"proxied"`
}

// toInput parses the request body into services.DNSRecordInput. The only
// failure mode is a malformed (non-UUID) hostedZoneId.
func (r dnsRecordRequest) toInput() (services.DNSRecordInput, error) {
	in := services.DNSRecordInput{
		RecordType: models.DNSRecordType(r.RecordType),
		TTL:        r.TTL,
		Proxied:    r.Proxied,
	}
	if r.HostedZoneID != nil {
		id, err := uuid.Parse(*r.HostedZoneID)
		if err != nil {
			return in, errors.New("invalid hostedZoneId")
		}
		in.HostedZoneID = &id
	}
	return in, nil
}

// dnsRecordResponse exposes every DomainDNSRecord field. Nothing here is
// sensitive -- the DNS provider credential's own material lives on the
// DNSProviderCredential row, never on this one, so unlike
// managedCertificateResponse there is nothing to omit.
type dnsRecordResponse struct {
	ID             uuid.UUID              `json:"id"`
	DomainID       uuid.UUID              `json:"domainId"`
	HostedZoneID   uuid.UUID              `json:"hostedZoneId"`
	RecordType     models.DNSRecordType   `json:"recordType"`
	TTL            *int                   `json:"ttl,omitempty"`
	Proxied        bool                   `json:"proxied"`
	ResolvedTarget string                 `json:"resolvedTarget,omitempty"`
	Status         models.DNSRecordStatus `json:"status"`
	StatusMessage  string                 `json:"statusMessage,omitempty"`
	CreatedBy      uuid.UUID              `json:"createdBy"`
	CreatedAt      time.Time              `json:"createdAt"`
	UpdatedAt      time.Time              `json:"updatedAt"`
}

func toDNSRecordResponse(r *models.DomainDNSRecord) dnsRecordResponse {
	return dnsRecordResponse{
		ID:             r.ID,
		DomainID:       r.DomainID,
		HostedZoneID:   r.HostedZoneID,
		RecordType:     r.RecordType,
		TTL:            r.TTL,
		Proxied:        r.Proxied,
		ResolvedTarget: r.ResolvedTarget,
		Status:         r.Status,
		StatusMessage:  r.StatusMessage,
		CreatedBy:      r.CreatedBy,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

// mapDNSRecordServiceError maps the service-layer errors Enable/Get/Update
// can return into an HTTP response: ErrNoHostedZone and ErrInvalidRecordType
// are caller-fixable input problems (400), ErrDNSRecordExists means Enable
// was called twice for the same domain (409), gorm.ErrRecordNotFound means
// there is no record for this domain yet (404), services.ErrDomainNotFound
// means the domain is unknown or belongs to a different project (404 -- a
// cross-project probe is indistinguishable from a missing domain), and
// anything else is an unexpected failure (500).
func mapDNSRecordServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrNoHostedZone), errors.Is(err, services.ErrInvalidRecordType):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrDNSRecordExists),
		errors.Is(err, services.ErrHostnameClaimed),
		errors.Is(err, services.ErrForeignRecordExists):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrDNSProviderUnavailable):
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, services.ErrDomainNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "DNS record not found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// parseProjectAndDomainID parses the projectId/domainId path params shared
// by every method below. ok is false if either is malformed, in which case
// the caller has already written the 400 response.
func parseProjectAndDomainID(c *gin.Context) (projectID, domainID uuid.UUID, ok bool) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return uuid.Nil, uuid.Nil, false
	}
	domainID, err = uuid.Parse(c.Param("domainId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid domain ID"})
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, domainID, true
}

// dnsRecordListItemResponse is a project-wide list row: every DomainDNSRecord
// field (promoted) plus the record's name (its domain hostname) and the name of
// the hosted zone it lives in, so the list can be shown without a lookup per row.
type dnsRecordListItemResponse struct {
	dnsRecordResponse
	DomainHostname string `json:"domainHostname"`
	ZoneName       string `json:"zoneName"`
}

func toDNSRecordListItemResponse(it models.DNSRecordListItem) dnsRecordListItemResponse {
	return dnsRecordListItemResponse{
		dnsRecordResponse: toDNSRecordResponse(&it.DomainDNSRecord),
		DomainHostname:    it.DomainHostname,
		ZoneName:          it.ZoneName,
	}
}

// List returns every managed DNS record in the project, each enriched with its
// domain hostname and hosted-zone name. Gated by the same canManageDomains
// permission as the per-domain record endpoints.
func (h *DNSRecordHandler) List(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage DNS records"})
		return
	}

	items, err := h.service.List(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := make([]dnsRecordListItemResponse, 0, len(items))
	for _, it := range items {
		resp = append(resp, toDNSRecordListItemResponse(it))
	}
	c.JSON(http.StatusOK, resp)
}

// Get returns the domain's DNS record.
func (h *DNSRecordHandler) Get(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, domainID, ok := parseProjectAndDomainID(c)
	if !ok {
		return
	}

	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage DNS records"})
		return
	}

	rec, err := h.service.Get(domainID, projectID)
	if err != nil {
		mapDNSRecordServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, toDNSRecordResponse(rec))
}

// Enable creates the domain's DNS record.
func (h *DNSRecordHandler) Enable(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, domainID, ok := parseProjectAndDomainID(c)
	if !ok {
		return
	}

	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage DNS records"})
		return
	}

	var req dnsRecordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	input, err := req.toInput()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	rec, err := h.service.Enable(domainID, projectID, user.ID, input)
	if err != nil {
		mapDNSRecordServiceError(c, err)
		return
	}

	h.auditService.LogAction(
		&projectID,
		user,
		"create",
		"dns_record",
		&rec.ID,
		domainID.String(),
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusCreated, toDNSRecordResponse(rec))
}

// Update changes the domain's DNS record settings.
func (h *DNSRecordHandler) Update(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, domainID, ok := parseProjectAndDomainID(c)
	if !ok {
		return
	}

	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage DNS records"})
		return
	}

	var req dnsRecordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	input, err := req.toInput()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	rec, err := h.service.Update(domainID, projectID, input)
	if err != nil {
		mapDNSRecordServiceError(c, err)
		return
	}

	h.auditService.LogAction(
		&projectID,
		user,
		"update",
		"dns_record",
		&rec.ID,
		domainID.String(),
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, toDNSRecordResponse(rec))
}

// Delete removes the domain's DNS record. Idempotent --
// DNSRecordService.Delete treats "no record for this domain" as success
// rather than an error, so this always 204s unless something actually
// fails.
func (h *DNSRecordHandler) Delete(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, domainID, ok := parseProjectAndDomainID(c)
	if !ok {
		return
	}

	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage DNS records"})
		return
	}

	if err := h.service.Delete(domainID, projectID); err != nil {
		mapDNSRecordServiceError(c, err)
		return
	}

	h.auditService.LogAction(
		&projectID,
		user,
		"delete",
		"dns_record",
		nil,
		domainID.String(),
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.Status(http.StatusNoContent)
}

// Refresh re-runs reconciliation and returns the fresh record.
func (h *DNSRecordHandler) Refresh(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	projectID, domainID, ok := parseProjectAndDomainID(c)
	if !ok {
		return
	}

	if !h.permChecker.CanManageDomains(projectID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: only project admins can manage DNS records"})
		return
	}

	rec, err := h.service.Refresh(domainID, projectID)
	if err != nil {
		mapDNSRecordServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, toDNSRecordResponse(rec))
}
