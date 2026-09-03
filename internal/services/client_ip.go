package services

import (
	"errors"
	"net"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// CreateClientIPInput represents the input for adding an IP address
type CreateClientIPInput struct {
	CIDR        string `json:"cidr" binding:"required"`
	Description string `json:"description"`
}

// AddIP adds an IP address to a client
func (s *ClientService) AddIP(clientID uuid.UUID, input *CreateClientIPInput, createdBy uuid.UUID) (*models.ClientIPAddress, error) {
	// Verify client exists
	_, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	// Validate CIDR format
	cidr := strings.TrimSpace(input.CIDR)
	if err := validateCIDR(cidr); err != nil {
		return nil, err
	}

	ip := &models.ClientIPAddress{
		ClientID:    clientID,
		CIDR:        cidr,
		Description: input.Description,
		CreatedBy:   createdBy,
	}

	if err := s.clientIPRepo.Create(ip); err != nil {
		return nil, err
	}

	// Cascade: mark affected routes as pending_deploy.
	// RETURN: AddIP's success value is the created IP row, which the caller
	// can re-read with ListIPs, so surfacing the cascade failure destroys
	// nothing. A silently stale allowlist would.
	if err := s.cascadeToAttachedRoutes(clientID, s.attachmentsWithIPAllowlist,
		"client ip allowlist changed"); err != nil {
		return nil, err
	}

	return s.clientIPRepo.GetByID(ip.ID)
}

// RemoveIP removes an IP address from a client
func (s *ClientService) RemoveIP(clientID uuid.UUID, ipID uuid.UUID) error {
	ip, err := s.clientIPRepo.GetByID(ipID)
	if err != nil {
		return errors.New("IP address not found")
	}

	if ip.ClientID != clientID {
		return errors.New("IP address does not belong to this client")
	}

	if err := s.clientIPRepo.Delete(ipID); err != nil {
		return err
	}

	// Cascade: mark affected routes as pending_deploy.
	// RETURN: error-only signature, nothing to lose, and a route still
	// serving a removed IP is exactly what the caller needs to hear about.
	return s.cascadeToAttachedRoutes(clientID, s.attachmentsWithIPAllowlist,
		"client ip allowlist changed")
}

// ListIPs returns all IP addresses for a client
func (s *ClientService) ListIPs(clientID uuid.UUID) ([]models.ClientIPAddress, error) {
	// Verify client exists
	_, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	return s.clientIPRepo.ListByClientID(clientID)
}

// validateCIDR validates that the input is a valid CIDR notation or IP address
func validateCIDR(cidr string) error {
	// Try parsing as CIDR first
	_, _, err := net.ParseCIDR(cidr)
	if err == nil {
		return nil
	}

	// Try parsing as plain IP address (will be treated as /32 or /128)
	ip := net.ParseIP(cidr)
	if ip != nil {
		return nil
	}

	return errors.New("invalid CIDR notation or IP address: " + cidr)
}
