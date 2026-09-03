package services

import (
	"errors"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// The five attachment queries the cascades used, one adapter each.
//
// They exist because cascadeToAttachedRoutes takes the query as a parameter,
// so each cascade caller can bind its own repository method without
// cascadeToAttachedRoutes needing to know which credential kind it is
// serving.
func (s *ClientService) attachmentsWithIPAllowlist(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	return s.clientAttachmentRepo.ListActiveByClientIDWithIPAllowlist(clientID)
}

func (s *ClientService) attachmentsWithHeaderAuth(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	return s.clientAttachmentRepo.ListActiveByClientIDWithHeaderAuth(clientID)
}

func (s *ClientService) attachmentsWithAPIKey(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	return s.clientAttachmentRepo.ListActiveByClientIDWithAPIKey(clientID)
}

func (s *ClientService) attachmentsWithJWT(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	return s.clientAttachmentRepo.ListActiveByClientIDWithJWT(clientID)
}

// allAttachments backs the allowed-methods cascade. Methods apply to every
// attachment, not just one credential kind, so the query is the unfiltered
// ListByClientID; cascadeToAttachedRoutes applies the active filter that
// cascadeMethodChangeToRoutes used to apply inline.
func (s *ClientService) allAttachments(clientID uuid.UUID) ([]models.ClientRouteAttachment, error) {
	return s.clientAttachmentRepo.ListByClientID(clientID)
}

// cascadeToAttachedRoutes marks every active route attached to this client as
// pending_deploy, so it picks up the client's changed configuration on the
// next deploy.
//
// Before Phase 2D this existed five times -- once per credential kind
// (cascadeIPChangeToRoutes, cascadeMethodChangeToRoutes,
// cascadeHeaderChangeToRoutes, cascadeAPIKeyChangeToRoutes,
// cascadeJWTChangeToRoutes) -- differing only in which repository query
// selected the attachments. Two behaviours changed in the collapse:
//
//  1. ERRORS PROPAGATE. Every copy discarded every failure, including the
//     final routeRepo.Update. After an API-key revocation that meant the
//     route kept serving the revoked credential with nothing logged.
//     Failures are now collected and returned as one aggregate error, and
//     one bad row does NOT stop the fan-out.
//  2. UNIFORM ACTIVE FILTERING. Four copies relied on a pre-filtered
//     ListActiveByClientIDWith* query; cascadeMethodChangeToRoutes used the
//     unfiltered ListByClientID plus a Go-side status check. The check now
//     runs here for every query -- redundant for the filtered ones, and free.
//
// clientAttachmentRepo and routeRepo are required constructor dependencies
// (Phase 2E Task 3), so this always runs. Before Phase 2E, controller ruling
// R13 documented a deliberate, time-boxed deviation from master design
// section 6.6 here: cascadeToAttachedRoutes logged and returned nil when
// either repository -- or the state machine derived from routeRepo -- was
// nil, because the test tree could construct ClientService without them.
// Now that NewClientService panics on a missing RouteRepo or
// ClientAttachmentRepo, that path is unreachable and the guard is gone.
func (s *ClientService) cascadeToAttachedRoutes(
	clientID uuid.UUID,
	list func(uuid.UUID) ([]models.ClientRouteAttachment, error),
	reason string,
) error {
	attachments, err := list(clientID)
	if err != nil {
		return fmt.Errorf("cascade %q for client %s: list attachments: %w", reason, clientID, err)
	}

	var failures []error
	for _, attachment := range attachments {
		if attachment.Status != models.AttachmentStatusActive {
			continue
		}
		route, err := s.routeRepo.GetByID(attachment.RouteID)
		if err != nil {
			failures = append(failures, fmt.Errorf("route %s: %w", attachment.RouteID, err))
			continue
		}
		// Only routes that are live in Kubernetes need redeploying; anything
		// else picks the change up when it is deployed for the first time.
		if route.Status != models.RouteStatusActive {
			continue
		}
		if err := s.state.To(SiteClientCascade, route, models.RouteStatusPendingDeploy, reason); err != nil {
			failures = append(failures, fmt.Errorf("route %s: %w", route.ID, err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("cascade %q for client %s: %w", reason, clientID, errors.Join(failures...))
	}
	return nil
}
