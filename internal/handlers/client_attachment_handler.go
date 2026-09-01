package handlers

import (
	"net/http"
	"strconv"

	"github.com/fastgateway-dev/backend-v2/internal/middleware"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ClientAttachmentHandler handles client-route attachment endpoints
type ClientAttachmentHandler struct {
	attachmentService services.ClientAttachmentServiceInterface
	clientService     services.ClientReader
	auditService      services.AuditServiceInterface
	routeService      services.RouteApprovalReader
	perms             *middleware.PermissionChecker
}

// NewClientAttachmentHandler creates a new client attachment handler
func NewClientAttachmentHandler(
	attachmentService services.ClientAttachmentServiceInterface,
	clientService services.ClientReader,
	auditService services.AuditServiceInterface,
	routeService services.RouteApprovalReader,
	perms *middleware.PermissionChecker,
) *ClientAttachmentHandler {
	return &ClientAttachmentHandler{
		attachmentService: attachmentService,
		clientService:     clientService,
		auditService:      auditService,
		routeService:      routeService,
		perms:             perms,
	}
}

// ===== Route-side endpoints =====

// ListRouteClients lists clients attached to a route
// GET /projects/:projectId/domains/:domainId/routes/:routeId/clients
func (h *ClientAttachmentHandler) ListRouteClients(c *gin.Context) {
	routeID, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	attachments, err := h.attachmentService.ListByRouteID(routeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, attachments)
}

// AttachFromRoute attaches a client to a route (route team initiates)
// POST /projects/:projectId/domains/:domainId/routes/:routeId/clients/attach
func (h *ClientAttachmentHandler) AttachFromRoute(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	routeID, err := uuid.Parse(c.Param("routeId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid route ID"})
		return
	}

	// Permission: owner or user with client.attach permission
	if !h.perms.HasTeamPermission(projectID, user, models.PermClientAttach) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: requires client.attach permission"})
		return
	}

	var input services.AttachFromRouteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	attachment, err := h.attachmentService.AttachFromRoute(routeID, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	attachDetails := map[string]interface{}{
		"clientId":           input.ClientID,
		"attachmentId":       attachment.ID,
		"approvalEntityType": "client_attachment",
		"authMethod":         middleware.GetAuthMethod(c),
	}
	if did, err := uuid.Parse(c.Param("domainId")); err == nil {
		if dn, err := h.routeService.GetDomainName(did); err == nil {
			attachDetails["domainName"] = dn
		}
	}
	if aid, err := h.routeService.GetApprovalIDForEntity(models.ApprovalEntityClientAttachment, attachment.ID); err == nil {
		attachDetails["approvalId"] = aid
	}
	h.auditService.LogAction(
		&projectID,
		user,
		"attach_client",
		"route",
		&routeID,
		"",
		attachDetails,
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusCreated, attachment)
}

// RequestDetachFromRoute requests detachment of a client from a route
// POST /projects/:projectId/domains/:domainId/routes/:routeId/clients/:attachmentId/detach
func (h *ClientAttachmentHandler) RequestDetachFromRoute(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	attachmentID, err := uuid.Parse(c.Param("attachmentId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid attachment ID"})
		return
	}

	// Permission: owner or user with client.detach permission
	if !h.perms.HasTeamPermission(projectID, user, models.PermClientDetach) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied: requires client.detach permission"})
		return
	}

	attachment, err := h.attachmentService.RequestDetach(attachmentID, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	routeID := attachment.RouteID
	detachDetails := map[string]interface{}{
		"clientId":           attachment.ClientID,
		"attachmentId":       attachment.ID,
		"approvalEntityType": "client_attachment",
		"authMethod":         middleware.GetAuthMethod(c),
	}
	if did, err := uuid.Parse(c.Param("domainId")); err == nil {
		if dn, err := h.routeService.GetDomainName(did); err == nil {
			detachDetails["domainName"] = dn
		}
	}
	if aid, err := h.routeService.GetApprovalIDForEntity(models.ApprovalEntityClientAttachment, attachment.ID); err == nil {
		detachDetails["approvalId"] = aid
	}
	h.auditService.LogAction(
		&projectID,
		user,
		"detach_client",
		"route",
		&routeID,
		"",
		detachDetails,
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, attachment)
}

// ===== Client-side endpoints =====

// ListClientRoutes lists routes attached to a client
// GET /clients/:clientId/routes
func (h *ClientAttachmentHandler) ListClientRoutes(c *gin.Context) {
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

	// Check permission: owner or client team member
	client, err := h.clientService.GetByID(clientID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}

	if !h.perms.CanAccessTeamResource(client.TeamID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	attachments, err := h.attachmentService.ListByClientID(clientID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, attachments)
}

// AttachFromClient attaches a client to a route (client team initiates)
// POST /clients/:clientId/routes/attach
func (h *ClientAttachmentHandler) AttachFromClient(c *gin.Context) {
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

	// Check permission: owner or client team member
	client, err := h.clientService.GetByID(clientID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}

	if !h.perms.CanAccessTeamResource(client.TeamID, user) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	var input services.AttachFromClientInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	attachment, err := h.attachmentService.AttachFromClient(clientID, &input, user.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	clientAttachDetails := map[string]interface{}{
		"routeId":            input.RouteID,
		"attachmentId":       attachment.ID,
		"approvalEntityType": "client_attachment",
		"authMethod":         middleware.GetAuthMethod(c),
	}
	if aid, err := h.routeService.GetApprovalIDForEntity(models.ApprovalEntityClientAttachment, attachment.ID); err == nil {
		clientAttachDetails["approvalId"] = aid
	}
	h.auditService.LogAction(
		nil,
		user,
		"attach_to_route",
		"client",
		&clientID,
		client.Name,
		clientAttachDetails,
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusCreated, attachment)
}

// ===== Approval endpoints =====

// ListClientApprovals lists client attachment approvals for a project
// GET /projects/:projectId/client-approvals
func (h *ClientAttachmentHandler) ListClientApprovals(c *gin.Context) {
	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	status := c.DefaultQuery("status", "pending")

	approvals, total, err := h.attachmentService.ListApprovalsByProjectID(projectID, page, limit, status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": approvals,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": (total + int64(limit) - 1) / int64(limit),
		},
	})
}

// GetClientApproval gets a client attachment approval by ID
// GET /projects/:projectId/client-approvals/:approvalId
func (h *ClientAttachmentHandler) GetClientApproval(c *gin.Context) {
	approvalID, err := uuid.Parse(c.Param("approvalId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid approval ID"})
		return
	}

	approval, err := h.attachmentService.GetApproval(approvalID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Approval not found"})
		return
	}

	c.JSON(http.StatusOK, approval)
}

// ApproveStage approves a specific stage of a client attachment approval
// POST /projects/:projectId/client-approvals/:approvalId/stages/:stageId/approve
func (h *ClientAttachmentHandler) ApproveStage(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	approvalID, err := uuid.Parse(c.Param("approvalId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid approval ID"})
		return
	}

	stageID, err := uuid.Parse(c.Param("stageId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid stage ID"})
		return
	}

	approval, err := h.attachmentService.ApproveStage(approvalID, stageID, user)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"approve_stage",
		"client_attachment",
		&approvalID,
		"",
		map[string]interface{}{
			"entityId":           approval.EntityID,
			"action":             approval.Action,
			"stageId":            stageID,
			"approvalId":         approvalID,
			"approvalEntityType": "client_attachment",
			"authMethod":         middleware.GetAuthMethod(c),
		},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, approval)
}

// RejectClientApproval rejects a specific stage of a client attachment approval
// POST /projects/:projectId/client-approvals/:approvalId/stages/:stageId/reject
func (h *ClientAttachmentHandler) RejectClientApproval(c *gin.Context) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found"})
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	approvalID, err := uuid.Parse(c.Param("approvalId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid approval ID"})
		return
	}

	stageID, err := uuid.Parse(c.Param("stageId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid stage ID"})
		return
	}

	var req struct {
		Comment string `json:"comment" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	approval, err := h.attachmentService.RejectStage(approvalID, stageID, user, req.Comment)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Audit log
	h.auditService.LogAction(
		&projectID,
		user,
		"reject_stage",
		"client_attachment",
		&approvalID,
		"",
		map[string]interface{}{
			"entityId":           approval.EntityID,
			"action":             approval.Action,
			"stageId":            stageID,
			"comment":            req.Comment,
			"approvalId":         approvalID,
			"approvalEntityType": "client_attachment",
			"authMethod":         middleware.GetAuthMethod(c),
		},
		c.ClientIP(),
		c.Request.UserAgent(),
	)

	c.JSON(http.StatusOK, approval)
}
