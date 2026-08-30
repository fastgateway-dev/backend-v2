package services

import (
	"errors"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
)

// AddMemberResult represents the result of adding a member by email
type AddMemberResult struct {
	Type   string                  `json:"type"` // "added" or "invited"
	User   *models.User            `json:"user,omitempty"`
	Invite *models.TeamEmailInvite `json:"invite,omitempty"`
}

// TeamEmailInviteService handles team email invite business logic
type TeamEmailInviteService struct {
	emailInviteRepo repository.TeamEmailInviteRepositoryInterface
	userRepo        repository.UserRepositoryInterface
	teamRepo        repository.TeamRepositoryInterface
}

// NewTeamEmailInviteService creates a new team email invite service
func NewTeamEmailInviteService(
	emailInviteRepo repository.TeamEmailInviteRepositoryInterface,
	userRepo repository.UserRepositoryInterface,
	teamRepo repository.TeamRepositoryInterface,
) *TeamEmailInviteService {
	return &TeamEmailInviteService{
		emailInviteRepo: emailInviteRepo,
		userRepo:        userRepo,
		teamRepo:        teamRepo,
	}
}

// AddMemberByEmail adds a member by email. If the user exists, they are added directly.
// If not, a pending email invite is created.
func (s *TeamEmailInviteService) AddMemberByEmail(teamID uuid.UUID, email string, invitedBy uuid.UUID) (*AddMemberResult, error) {
	// Try to find an existing user with this email
	user, err := s.userRepo.GetByEmail(email)
	if err == nil && user != nil {
		// User exists - check if already a member
		isMember, err := s.teamRepo.IsMember(teamID, user.ID)
		if err != nil {
			return nil, err
		}
		if isMember {
			return nil, errors.New("user is already a member of this team")
		}

		// Add them directly
		if err := s.teamRepo.AddMember(teamID, user.ID); err != nil {
			return nil, err
		}

		return &AddMemberResult{
			Type: "added",
			User: user,
		}, nil
	}

	// User doesn't exist - check for existing invite to avoid duplicates
	existingInvites, err := s.emailInviteRepo.GetByEmail(email)
	if err != nil {
		return nil, err
	}
	for _, inv := range existingInvites {
		if inv.TeamID == teamID {
			return nil, errors.New("an invite for this email already exists for this team")
		}
	}

	// Create a pending invite
	invite := &models.TeamEmailInvite{
		TeamID:    teamID,
		Email:     email,
		InvitedBy: invitedBy,
	}

	if err := s.emailInviteRepo.Create(invite); err != nil {
		return nil, err
	}

	return &AddMemberResult{
		Type:   "invited",
		Invite: invite,
	}, nil
}

// ListInvites lists pending email invites for a team
func (s *TeamEmailInviteService) ListInvites(teamID uuid.UUID) ([]models.TeamEmailInvite, error) {
	return s.emailInviteRepo.ListByTeam(teamID)
}

// DeleteInvite deletes a pending email invite
func (s *TeamEmailInviteService) DeleteInvite(inviteID uuid.UUID) error {
	return s.emailInviteRepo.Delete(inviteID)
}

// ListAllInvites lists all pending email invites (admin use)
func (s *TeamEmailInviteService) ListAllInvites() ([]models.TeamEmailInvite, error) {
	return s.emailInviteRepo.ListAll()
}
