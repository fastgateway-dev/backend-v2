package services

import (
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
)

// hasAPIKeyClientAttachments checks if there are any API key client attachments for a route
func (s *RouteService) hasAPIKeyClientAttachments(routeID uuid.UUID) bool {
	if s.clientAttachmentRepo == nil {
		return false
	}

	// Get active attachments
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return false
	}

	// Get approved attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		approvedAttachments = nil
	}

	// Check if any attachment has API key enabled
	for _, att := range append(activeAttachments, approvedAttachments...) {
		if att.EnableAPIKey {
			return true
		}
	}

	return false
}

// hasJWTClientAttachments checks if there are any JWT client attachments for a route
func (s *RouteService) hasJWTClientAttachments(routeID uuid.UUID) bool {
	if s.clientAttachmentRepo == nil {
		return false
	}

	// Get active attachments
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return false
	}

	// Get approved attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		approvedAttachments = nil
	}

	// Check if any attachment has JWT enabled
	for _, att := range append(activeAttachments, approvedAttachments...) {
		if att.EnableJWT {
			return true
		}
	}

	return false
}

func (s *RouteService) hasMTLSClientAttachments(routeID uuid.UUID) bool {
	if s.clientAttachmentRepo == nil {
		return false
	}

	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return false
	}

	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		approvedAttachments = nil
	}

	for _, att := range append(activeAttachments, approvedAttachments...) {
		if att.EnableMTLS {
			return true
		}
	}

	return false
}

// countClientAttachments counts active and approved client attachments for a route
func (s *RouteService) countClientAttachments(routeID uuid.UUID) int {
	if s.clientAttachmentRepo == nil {
		return 0
	}

	count := 0

	// Get active attachments
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err == nil {
		count += len(activeAttachments)
	}

	// Get approved attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err == nil {
		count += len(approvedAttachments)
	}

	return count
}

// buildClientIPAuthorizationConfig builds authorization config from base-route-only clients.
// This collects IPs, headers, and methods from active/approved client attachments
// that do NOT have API key/JWT/mTLS enabled (those go to per-client routes).
func (s *RouteService) buildClientIPAuthorizationConfig(routeID uuid.UUID) *kubernetes.AuthorizationPolicyConfig {
	// Collect client IPs from attachments (normalize to ensure CIDR format)
	clientCIDRs := s.collectClientIPCIDRs(routeID)
	clientHeaders := s.collectClientHeaders(routeID)
	clientMethods := s.collectClientMethods(routeID)

	if len(clientCIDRs) == 0 && len(clientHeaders) == 0 && len(clientMethods) == 0 {
		return nil
	}

	rule := kubernetes.AuthorizationRulePolicyConfig{
		Action: "Allow",
	}

	// Normalize and deduplicate CIDRs
	if len(clientCIDRs) > 0 {
		seen := make(map[string]bool)
		uniqueCIDRs := make([]string, 0, len(clientCIDRs))
		for _, cidr := range clientCIDRs {
			normalized := routeplan.NormalizeCIDR(cidr)
			if !seen[normalized] {
				seen[normalized] = true
				uniqueCIDRs = append(uniqueCIDRs, normalized)
			}
		}
		rule.ClientCIDRs = uniqueCIDRs
	}

	// Add headers
	if len(clientHeaders) > 0 {
		for _, h := range clientHeaders {
			rule.Headers = append(rule.Headers, kubernetes.HeaderMatchPolicyConfig{Name: h.Name, Values: h.Values})
		}
	}

	// Add methods
	if len(clientMethods) > 0 {
		rule.Methods = clientMethods
	}

	return &kubernetes.AuthorizationPolicyConfig{
		DefaultAction: "Deny",
		Rules:         []kubernetes.AuthorizationRulePolicyConfig{rule},
	}
}

// collectClientHeaders collects header matches from base-route-only clients
// (header auth enabled, but NOT API key/JWT/mTLS enabled)
func (s *RouteService) collectClientHeaders(routeID uuid.UUID) []models.AuthorizationHeaderMatch {
	if s.clientAttachmentRepo == nil || s.clientHeaderRepo == nil {
		return nil
	}

	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil
	}
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		approvedAttachments = nil
	}
	allAttachments := append(activeAttachments, approvedAttachments...)

	var headers []models.AuthorizationHeaderMatch
	for _, attachment := range allAttachments {
		if !attachment.EnableHeaderAuth {
			continue
		}
		// Skip per-client route clients
		if attachment.EnableAPIKey || attachment.EnableJWT || attachment.EnableMTLS {
			continue
		}
		clientHeaders, err := s.clientHeaderRepo.ListByClientID(attachment.ClientID)
		if err != nil {
			continue
		}
		for _, h := range clientHeaders {
			headers = append(headers, models.AuthorizationHeaderMatch{Name: h.Name, Values: []string(h.Values)})
		}
	}
	return headers
}

// collectClientMethods collects allowed methods from base-route-only clients
// Methods are now stored on the client entity, not the attachment.
// Only includes clients that are NOT per-client route clients (API key/JWT/mTLS).
func (s *RouteService) collectClientMethods(routeID uuid.UUID) []string {
	if s.clientAttachmentRepo == nil || s.clientRepo == nil {
		return nil
	}

	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil
	}
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		approvedAttachments = nil
	}
	allAttachments := append(activeAttachments, approvedAttachments...)

	seen := make(map[string]bool)
	var methods []string
	for _, attachment := range allAttachments {
		// Skip per-client route clients
		if attachment.EnableAPIKey || attachment.EnableJWT || attachment.EnableMTLS {
			continue
		}
		// Get client to read allowed methods
		client, err := s.clientRepo.GetByID(attachment.ClientID)
		if err != nil || len(client.AllowedMethods) == 0 {
			continue
		}
		for _, m := range client.AllowedMethods {
			if !seen[m] {
				seen[m] = true
				methods = append(methods, m)
			}
		}
	}
	return methods
}

// collectClientIPCIDRs collects IP CIDRs from all active/approved client attachments
// with IP allowlisting enabled BUT NOT API key/JWT enabled.
// Clients with both IP and API key/JWT go to per-client routes only (AND logic).
// Used by buildClientIPAuthorizationConfig.
func (s *RouteService) collectClientIPCIDRs(routeID uuid.UUID) []string {
	if s.clientAttachmentRepo == nil || s.clientIPRepo == nil {
		return nil
	}

	// Get active attachments with IP allowlist enabled
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		log.Printf("Failed to list active attachments for route %s: %v", routeID, err)
		return nil
	}

	// Also get approved (pending deploy) attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		log.Printf("Failed to list approved attachments for route %s: %v", routeID, err)
	}

	// Merge active + approved attachments
	allAttachments := append(activeAttachments, approvedAttachments...)

	// Collect CIDRs from clients with IP allowlisting enabled (but NOT API key)
	// Clients with both IP and API key enabled require BOTH checks (AND logic),
	// so their IPs should NOT be in the base route - they go to per-client routes only
	var cidrs []string
	for _, attachment := range allAttachments {
		if !attachment.EnableIPAllowlist {
			continue
		}

		// Skip if API key or JWT is also enabled - those clients go to per-client routes only
		// Adding their IPs here would allow bypassing the API key/JWT requirement
		if attachment.EnableAPIKey || attachment.EnableJWT {
			continue
		}

		ips, err := s.clientIPRepo.ListByClientID(attachment.ClientID)
		if err != nil {
			log.Printf("Failed to list IPs for client %s: %v", attachment.ClientID, err)
			continue
		}

		for _, ip := range ips {
			cidrs = append(cidrs, ip.CIDR)
		}
	}

	return cidrs
}

// buildAuthorizationFromClientAttachments builds authorization config by collecting
// IP CIDRs from all active/approved client attachments with IP allowlisting enabled
// DEPRECATED: Use buildMergedAuthorizationConfig instead which includes direct IPs
func (s *RouteService) buildAuthorizationFromClientAttachments(routeID uuid.UUID) *kubernetes.AuthorizationPolicyConfig {
	if s.clientAttachmentRepo == nil || s.clientIPRepo == nil {
		return nil
	}

	// Get active attachments with IP allowlist enabled
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		log.Printf("Failed to list active attachments for route %s: %v", routeID, err)
		return nil
	}

	// Also get approved (pending deploy) attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		log.Printf("Failed to list approved attachments for route %s: %v", routeID, err)
	}

	// Merge active + approved attachments
	allAttachments := append(activeAttachments, approvedAttachments...)

	// Collect CIDRs from clients with IP allowlisting enabled
	var allCIDRs []string
	for _, attachment := range allAttachments {
		if !attachment.EnableIPAllowlist {
			continue
		}

		ips, err := s.clientIPRepo.ListByClientID(attachment.ClientID)
		if err != nil {
			log.Printf("Failed to list IPs for client %s: %v", attachment.ClientID, err)
			continue
		}

		for _, ip := range ips {
			allCIDRs = append(allCIDRs, ip.CIDR)
		}
	}

	if len(allCIDRs) == 0 {
		return nil
	}

	return &kubernetes.AuthorizationPolicyConfig{
		DefaultAction: "Deny",
		Rules: []kubernetes.AuthorizationRulePolicyConfig{
			{
				Action:      "Allow",
				ClientCIDRs: allCIDRs,
			},
		},
	}
}

// updateClientAttachmentStatuses updates client attachment statuses after a successful deploy
// approved → active (for new/updated attachments)
// pending_detach approved attachments → removed (handled separately via approved status first)
func (s *RouteService) updateClientAttachmentStatuses(routeID uuid.UUID) {
	if s.clientAttachmentRepo == nil {
		return
	}

	// Move approved attachments to active
	if err := s.clientAttachmentRepo.UpdateStatusByRouteID(routeID, models.AttachmentStatusApproved, models.AttachmentStatusActive); err != nil {
		log.Printf("Failed to update client attachment statuses (approved→active) for route %s: %v", routeID, err)
	}

	// Detach cleanup: OnApprovalComplete sets detached attachments directly to "removed",
	// so cleanupStaleAPIKeyRoutes correctly identifies their K8s resources as stale.
}

// EffectiveIPEntry represents a single IP CIDR in the effective IP allowlist
type EffectiveIPEntry struct {
	CIDR        string `json:"cidr"`
	ClientID    string `json:"clientId"`
	ClientName  string `json:"clientName"`
	Description string `json:"description,omitempty"`
}

// GetEffectiveIPAllowlist returns the merged IP allowlist for a route from active client attachments
func (s *RouteService) GetEffectiveIPAllowlist(routeID uuid.UUID) ([]EffectiveIPEntry, error) {
	if s.clientAttachmentRepo == nil || s.clientIPRepo == nil {
		return []EffectiveIPEntry{}, nil
	}

	// Get active attachments with IP allowlist enabled
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("failed to list active attachments: %w", err)
	}

	// Also include approved (pending deploy) attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("failed to list approved attachments: %w", err)
	}

	allAttachments := append(activeAttachments, approvedAttachments...)

	var entries []EffectiveIPEntry
	for _, attachment := range allAttachments {
		if !attachment.EnableIPAllowlist {
			continue
		}

		clientName := "Unknown"
		if attachment.Client != nil {
			clientName = attachment.Client.Name
		}

		ips, err := s.clientIPRepo.ListByClientID(attachment.ClientID)
		if err != nil {
			continue
		}

		for _, ip := range ips {
			entries = append(entries, EffectiveIPEntry{
				CIDR:        ip.CIDR,
				ClientID:    attachment.ClientID.String(),
				ClientName:  clientName,
				Description: ip.Description,
			})
		}
	}

	if entries == nil {
		entries = []EffectiveIPEntry{}
	}

	return entries, nil
}
