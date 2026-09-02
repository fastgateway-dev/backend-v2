package services

import (
	"errors"
	"fmt"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/routeplan"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// hasAPIKeyClientAttachments checks if there are any API key client attachments for a route
func (s *RouteService) hasAPIKeyClientAttachments(routeID uuid.UUID) bool {
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

// countClientAttachments counts active and approved client attachments for a route.
//
// SINCE Phase 2G (S3): ListActiveByRouteID/ListApprovedByRouteID both end in
// gorm's Find, which returns an empty slice with a nil error when there are
// no rows -- so any non-nil error here is a genuine repository failure, never
// absence, and is now propagated. BEFORE Phase 2G: either error was silently
// treated as "0 attachments", indistinguishable from a route with no clients
// at all. Its caller, deploySecurityPolicy, gates the entire
// DefaultTrafficPolicy block (including "deny") on clientCount > 0, so a
// swallowed error here used to deploy a defaultTrafficPolicy=deny route with
// NO deny rule at all.
func (s *RouteService) countClientAttachments(routeID uuid.UUID) (int, error) {
	// Get active attachments
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return 0, fmt.Errorf("list active client attachments for route %s: %w", routeID, err)
	}

	// Get approved attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		return 0, fmt.Errorf("list approved client attachments for route %s: %w", routeID, err)
	}

	return len(activeAttachments) + len(approvedAttachments), nil
}

// buildClientIPAuthorizationConfig builds authorization config from base-route-only clients.
// This collects IPs, headers, and methods from active/approved client attachments
// that do NOT have API key/JWT/mTLS enabled (those go to per-client routes).
//
// SINCE Phase 2G (S4): propagates any error from its three collectors instead
// of treating a collector failure as "this client configured nothing" -- see
// each collector's own comment for why a swallowed error there is a fail-open.
func (s *RouteService) buildClientIPAuthorizationConfig(routeID uuid.UUID) (*kubernetes.AuthorizationPolicyConfig, error) {
	// Collect client IPs from attachments (normalize to ensure CIDR format)
	clientCIDRs, err := s.collectClientIPCIDRs(routeID)
	if err != nil {
		return nil, err
	}
	clientHeaders, err := s.collectClientHeaders(routeID)
	if err != nil {
		return nil, err
	}
	clientMethods, err := s.collectClientMethods(routeID)
	if err != nil {
		return nil, err
	}

	if len(clientCIDRs) == 0 && len(clientHeaders) == 0 && len(clientMethods) == 0 {
		return nil, nil
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
	}, nil
}

// collectClientHeaders collects header matches from base-route-only clients
// (header auth enabled, but NOT API key/JWT/mTLS enabled)
//
// SINCE Phase 2G (S4): ListActiveByRouteID/ListApprovedByRouteID/
// clientHeaderRepo.ListByClientID all end in gorm's Find, so any non-nil
// error from them is a genuine repository failure, never absence -- it is now
// propagated instead of yielding nil/skipping the client. BEFORE Phase 2G: a
// swallowed error here silently dropped that client's Authorization header
// requirement, indistinguishable from a client with no header auth
// configured.
func (s *RouteService) collectClientHeaders(routeID uuid.UUID) ([]models.AuthorizationHeaderMatch, error) {
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("list active client attachments for route %s: %w", routeID, err)
	}
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("list approved client attachments for route %s: %w", routeID, err)
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
			return nil, fmt.Errorf("list headers for client %s: %w", attachment.ClientID, err)
		}
		for _, h := range clientHeaders {
			headers = append(headers, models.AuthorizationHeaderMatch{Name: h.Name, Values: []string(h.Values)})
		}
	}
	return headers, nil
}

// collectClientMethods collects allowed methods from base-route-only clients
// Methods are now stored on the client entity, not the attachment.
// Only includes clients that are NOT per-client route clients (API key/JWT/mTLS).
//
// SINCE Phase 2G (S4): ListActiveByRouteID/ListApprovedByRouteID end in gorm's
// Find (any error is a genuine failure, propagated unconditionally).
// clientRepo.GetByID ends in First, so absence surfaces as
// gorm.ErrRecordNotFound and is distinguished from a real lookup failure: a
// client that has genuinely been deleted (or has no AllowedMethods
// configured) is legitimately skipped, but any OTHER error is propagated.
// BEFORE Phase 2G: `err != nil || len(...) == 0` conflated the two, so a
// GetByID failure silently dropped that client's verb restriction, leaving
// the rule with NO method restriction at all.
func (s *RouteService) collectClientMethods(routeID uuid.UUID) ([]string, error) {
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("list active client attachments for route %s: %w", routeID, err)
	}
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("list approved client attachments for route %s: %w", routeID, err)
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
		switch {
		case err == nil:
			// fall through to the AllowedMethods check below
		case errors.Is(err, gorm.ErrRecordNotFound):
			// Genuine absence: the client no longer exists.
			continue
		default:
			return nil, fmt.Errorf("get client %s: %w", attachment.ClientID, err)
		}
		if len(client.AllowedMethods) == 0 {
			continue
		}
		for _, m := range client.AllowedMethods {
			if !seen[m] {
				seen[m] = true
				methods = append(methods, m)
			}
		}
	}
	return methods, nil
}

// collectClientIPCIDRs collects IP CIDRs from all active/approved client attachments
// with IP allowlisting enabled BUT NOT API key/JWT enabled.
// Clients with both IP and API key/JWT go to per-client routes only (AND logic).
// Used by buildClientIPAuthorizationConfig.
//
// SINCE Phase 2G (S4): ListActiveByRouteID/ListApprovedByRouteID/
// clientIPRepo.ListByClientID all end in gorm's Find, so any non-nil error
// from them is a genuine repository failure, never absence -- it is now
// propagated. BEFORE Phase 2G: all three were logged and swallowed, and a
// swallowed error here fed the all-empty check in
// buildClientIPAuthorizationConfig, which could drop the WHOLE Authorization
// block for the base route.
func (s *RouteService) collectClientIPCIDRs(routeID uuid.UUID) ([]string, error) {
	// Get active attachments with IP allowlist enabled
	activeAttachments, err := s.clientAttachmentRepo.ListActiveByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("list active client attachments for route %s: %w", routeID, err)
	}

	// Also get approved (pending deploy) attachments
	approvedAttachments, err := s.clientAttachmentRepo.ListApprovedByRouteID(routeID)
	if err != nil {
		return nil, fmt.Errorf("list approved client attachments for route %s: %w", routeID, err)
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
			return nil, fmt.Errorf("list IPs for client %s: %w", attachment.ClientID, err)
		}

		for _, ip := range ips {
			cidrs = append(cidrs, ip.CIDR)
		}
	}

	return cidrs, nil
}

// buildAuthorizationFromClientAttachments builds authorization config by collecting
// IP CIDRs from all active/approved client attachments with IP allowlisting enabled
// DEPRECATED: Use buildMergedAuthorizationConfig instead which includes direct IPs
func (s *RouteService) buildAuthorizationFromClientAttachments(routeID uuid.UUID) *kubernetes.AuthorizationPolicyConfig {
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
