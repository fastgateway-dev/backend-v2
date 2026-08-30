package services

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var mentionRegex = regexp.MustCompile(`@(\w+)`)

// CommentService handles approval comment operations
type CommentService struct {
	commentRepo      repository.CommentRepositoryInterface
	notificationRepo repository.NotificationRepositoryInterface
	approvalRepo     repository.UnifiedApprovalRepositoryInterface
	teamRepo         repository.TeamRepositoryInterface
}

// NewCommentService creates a new comment service
func NewCommentService(
	commentRepo repository.CommentRepositoryInterface,
	notificationRepo repository.NotificationRepositoryInterface,
	approvalRepo repository.UnifiedApprovalRepositoryInterface,
	teamRepo repository.TeamRepositoryInterface,
) *CommentService {
	return &CommentService{
		commentRepo:      commentRepo,
		notificationRepo: notificationRepo,
		approvalRepo:     approvalRepo,
		teamRepo:         teamRepo,
	}
}

// Create creates a comment and sends mention notifications
func (s *CommentService) Create(approvalID uuid.UUID, user *models.User, body string) (*models.ApprovalComment, error) {
	// Verify the approval exists
	approval, err := s.approvalRepo.GetByID(approvalID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("approval not found")
		}
		return nil, err
	}

	comment := &models.ApprovalComment{
		ApprovalID: approvalID,
		UserID:     user.ID,
		Body:       body,
	}

	if err := s.commentRepo.Create(comment); err != nil {
		return nil, err
	}

	// Parse @mentions and create notifications
	s.processMentions(comment, user, approval)

	// Set user on comment for response
	comment.User = *user

	return comment, nil
}

// ListByApprovalID lists comments for an approval
func (s *CommentService) ListByApprovalID(approvalID uuid.UUID) ([]models.ApprovalComment, error) {
	return s.commentRepo.ListByApprovalID(approvalID)
}

// CountByApprovalID counts comments for an approval
func (s *CommentService) CountByApprovalID(approvalID uuid.UUID) (int64, error) {
	return s.commentRepo.CountByApprovalID(approvalID)
}

// processMentions extracts @username mentions, resolves to users, and creates notifications
func (s *CommentService) processMentions(comment *models.ApprovalComment, author *models.User, approval *models.Approval) {
	matches := mentionRegex.FindAllStringSubmatch(comment.Body, -1)
	if len(matches) == 0 {
		return
	}

	// Deduplicate usernames
	usernameSet := make(map[string]bool)
	for _, match := range matches {
		username := match[1]
		if username != author.Username {
			usernameSet[username] = true
		}
	}

	// Get project members
	projectMembers, err := s.getProjectMemberUsernames(approval.ProjectID)
	if err != nil {
		return
	}

	for username := range usernameSet {
		memberUser, ok := projectMembers[username]
		if !ok {
			continue
		}

		notification := &models.Notification{
			UserID: memberUser.ID,
			Type:   "mention",
			Title:  fmt.Sprintf("@%s mentioned you in a comment on approval review", author.Username),
			Link:   fmt.Sprintf("/projects/%s/approvals/%s", approval.ProjectID, approval.ID),
		}
		_ = s.notificationRepo.Create(notification)
	}
}

// getProjectMemberUsernames returns a map of username -> User for all project members
func (s *CommentService) getProjectMemberUsernames(projectID uuid.UUID) (map[string]models.User, error) {
	teamRoles, err := s.teamRepo.ListProjectTeams(projectID)
	if err != nil {
		return nil, err
	}

	result := make(map[string]models.User)
	for _, tr := range teamRoles {
		members, err := s.teamRepo.ListMembers(tr.TeamID)
		if err != nil {
			continue
		}
		for _, member := range members {
			result[member.Username] = member
		}
	}

	return result, nil
}
