package services

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// ValidateStreamPortsForTest and CheckUsedClaimsKeptForTest expose the claim
// validators to the external test package; Create/Update call them.
func ValidateStreamPortsForTest(s *StreamService, tmpl *models.DomainTemplate, selfID *uuid.UUID, ports models.StreamPorts) (models.StreamPorts, error) {
	return s.validateStreamPorts(tmpl, selfID, ports)
}

func CheckUsedClaimsKeptForTest(s *StreamService, stream *models.Stream, next models.StreamPorts) error {
	return s.checkUsedClaimsKept(stream, next)
}
