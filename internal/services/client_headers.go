package services

import (
	"errors"
	"strings"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// CreateClientHeaderInput represents the input for adding a header
type CreateClientHeaderInput struct {
	Name        string   `json:"name" binding:"required"`
	Values      []string `json:"values" binding:"required"`
	Description string   `json:"description"`
}

// AddHeader adds a header to a client
func (s *ClientService) AddHeader(clientID uuid.UUID, input *CreateClientHeaderInput, createdBy uuid.UUID) (*models.ClientHeader, error) {
	_, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, errors.New("header name is required")
	}
	if len(input.Values) == 0 {
		return nil, errors.New("at least one header value is required")
	}
	for _, v := range input.Values {
		if strings.TrimSpace(v) == "" {
			return nil, errors.New("header values must not be empty")
		}
	}

	header := &models.ClientHeader{
		ClientID:    clientID,
		Name:        name,
		Values:      models.StringList(input.Values),
		Description: input.Description,
		CreatedBy:   createdBy,
	}

	if err := s.clientHeaderRepo.Create(header); err != nil {
		return nil, err
	}

	// RETURN: the created header is re-readable via ListHeaders.
	if err := s.cascadeToAttachedRoutes(clientID, s.attachmentsWithHeaderAuth,
		"client header auth changed"); err != nil {
		return nil, err
	}

	return s.clientHeaderRepo.GetByID(header.ID)
}

// RemoveHeader removes a header from a client
func (s *ClientService) RemoveHeader(clientID uuid.UUID, headerID uuid.UUID) error {
	header, err := s.clientHeaderRepo.GetByID(headerID)
	if err != nil {
		return errors.New("header not found")
	}
	if header.ClientID != clientID {
		return errors.New("header does not belong to this client")
	}
	if err := s.clientHeaderRepo.Delete(headerID); err != nil {
		return err
	}
	// RETURN: error-only signature; a route still matching a removed header
	// value is a live authorization gap.
	return s.cascadeToAttachedRoutes(clientID, s.attachmentsWithHeaderAuth,
		"client header auth changed")
}

// ListHeaders returns all headers for a client
func (s *ClientService) ListHeaders(clientID uuid.UUID) ([]models.ClientHeader, error) {
	_, err := s.clientRepo.GetByID(clientID)
	if err != nil {
		return nil, errors.New("client not found")
	}
	return s.clientHeaderRepo.ListByClientID(clientID)
}
