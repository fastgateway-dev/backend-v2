package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services/clients"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ClientCertificateServiceInterface is the subset of
// clients.ClientCertificateService that ClientCertificateHandler uses.
type ClientCertificateServiceInterface interface {
	AttachCertificate(ctx context.Context, clientID, certID, actingUser uuid.UUID) (*models.Client, error)
	DetachCertificate(ctx context.Context, clientID, actingUser uuid.UUID) (*models.Client, error)
	AttachableCertificates(clientID uuid.UUID) ([]models.ManagedCertificate, error)
}

// ClientCertificateHandler exposes attach/detach of a managed client
// certificate onto a Client's mTLS configuration.
//
// It is a separate handler from ClientHandler (rather than two more methods
// bolted onto it) because the service backing it,
// clients.ClientCertificateService, needs a control-plane client and
// therefore only exists when the API server is running in-cluster. main.go
// nil-guards its construction and setupRouter nil-guards its route
// registration the same way it already does for CertificateIssuerHandler
// and ManagedCertificateHandler, instead of ClientHandler ever holding a
// dependency that might be nil.
type ClientCertificateHandler struct {
	certService   ClientCertificateServiceInterface
	clientService ClientServiceInterface
	auditService  AuditServiceInterface
	perms         *middleware.PermissionChecker
}

// NewClientCertificateHandler creates a new client-certificate handler.
func NewClientCertificateHandler(
	certService ClientCertificateServiceInterface,
	clientService ClientServiceInterface,
	auditService AuditServiceInterface,
	perms *middleware.PermissionChecker,
) *ClientCertificateHandler {
	return &ClientCertificateHandler{
		certService:   certService,
		clientService: clientService,
		auditService:  auditService,
		perms:         perms,
	}
}

// AttachClientCertificateRequest is the body for AttachCertificate.
type AttachClientCertificateRequest struct {
	CertificateID uuid.UUID `json:"certificateId" binding:"required"`
}

// AttachCertificate attaches a managed client-usage certificate to a
// client's mTLS configuration. This only writes the client row -- the CA
// Secret and ClientTrafficPolicy it implies materialize at the next domain
// deploy (route_deploy_clients.go), exactly like UpdateClientMTLS.
// PUT /clients/:clientId/certificate
func (h *ClientCertificateHandler) AttachCertificate(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	clientID, err := uuid.Parse(c.Param("clientId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid client ID"})
		return
	}

	// Check team membership
	existingClient, err := h.clientService.GetByID(clientID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}

	isMember, _ := h.perms.IsTeamMember(existingClient.TeamID, user.ID)
	if !isMember {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: you must be a member of the client's team"})
		return
	}

	var input AttachClientCertificateRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	client, err := h.certService.AttachCertificate(c.Request.Context(), clientID, input.CertificateID, user.ID)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "Certificate not found"})
		case errors.Is(err, clients.ErrNotClientCert), errors.Is(err, clients.ErrCertNotReadyForAttach):
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		case errors.Is(err, clients.ErrCertProjectNotAccessible):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		case errors.Is(err, clients.ErrCertAlreadyAttached), errors.Is(err, clients.ErrClientHasManagedCert):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"attach_certificate",
		"client",
		&clientID,
		client.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, client)
}

// AttachableCertificates lists the managed client certificates a given
// client could attach: usage=client, status=ready, in projects the client's
// team has a role in, and not already attached to any client. It lets the
// frontend pre-scope the attach picker to the team's projects instead of
// listing every certificate the caller can see and filtering client-side.
// Responses only ever carry certificate metadata (via
// toManagedCertificateResponse) -- never key or certificate material.
// GET /clients/:clientId/attachable-certificates
func (h *ClientCertificateHandler) AttachableCertificates(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	clientID, err := uuid.Parse(c.Param("clientId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid client ID"})
		return
	}

	// Check team membership
	existingClient, err := h.clientService.GetByID(clientID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}

	isMember, _ := h.perms.IsTeamMember(existingClient.TeamID, user.ID)
	if !isMember {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: you must be a member of the client's team"})
		return
	}

	certs, err := h.certService.AttachableCertificates(clientID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := make([]managedCertificateResponse, 0, len(certs))
	for i := range certs {
		resp = append(resp, toManagedCertificateResponse(&certs[i]))
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// DetachCertificate clears a client's managed certificate and its derived
// mTLS fields, reverting the client to no mTLS. A previously-configured BYO
// mTLS configuration is not restored automatically -- re-add it via
// UpdateClientMTLS if that's wanted.
// DELETE /clients/:clientId/certificate
func (h *ClientCertificateHandler) DetachCertificate(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	clientID, err := uuid.Parse(c.Param("clientId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid client ID"})
		return
	}

	// Check team membership
	existingClient, err := h.clientService.GetByID(clientID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}

	isMember, _ := h.perms.IsTeamMember(existingClient.TeamID, user.ID)
	if !isMember {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: you must be a member of the client's team"})
		return
	}

	client, err := h.certService.DetachCertificate(c.Request.Context(), clientID, user.ID)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	// Audit log
	h.auditService.LogAction(
		nil,
		user,
		"detach_certificate",
		"client",
		&clientID,
		client.Name,
		middleware.AuditDetails(c),
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, client)
}
