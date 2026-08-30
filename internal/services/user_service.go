package services

import (
	"errors"
	"log"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UserService handles user business logic
type UserService struct {
	userRepo repository.UserRepositoryInterface
}

// NewUserService creates a new user service
func NewUserService(userRepo repository.UserRepositoryInterface) *UserService {
	return &UserService{
		userRepo: userRepo,
	}
}

// CreateUserInput represents input for creating a user
type CreateUserInput struct {
	Username string          `json:"username" binding:"required"`
	Email    string          `json:"email" binding:"required,email"`
	Password string          `json:"password" binding:"required,min=8"`
	Role     models.UserRole `json:"role" binding:"required"`
}

// UpdateUserInput represents input for updating a user
type UpdateUserInput struct {
	Email    string `json:"email" binding:"omitempty,email"`
	Password string `json:"password" binding:"omitempty,min=8"`
	IsActive *bool  `json:"isActive"`
}

// Create creates a new user
func (s *UserService) Create(input *CreateUserInput) (*models.User, error) {
	// Check if username already exists
	_, err := s.userRepo.GetByUsername(input.Username)
	if err == nil {
		return nil, errors.New("username already exists")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Check if email already exists
	_, err = s.userRepo.GetByEmail(input.Email)
	if err == nil {
		return nil, errors.New("email already exists")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Hash password
	passwordHash, err := HashPassword(input.Password)
	if err != nil {
		return nil, err
	}

	user := &models.User{
		Username:     input.Username,
		Email:        input.Email,
		PasswordHash: &passwordHash,
		Role:         input.Role,
		IsActive:     true,
	}

	if err := s.userRepo.Create(user); err != nil {
		return nil, err
	}

	return user, nil
}

// GetByID gets a user by ID
func (s *UserService) GetByID(id uuid.UUID) (*models.User, error) {
	return s.userRepo.GetByID(id)
}

// List lists users with pagination
func (s *UserService) List(page, limit int, role string) ([]models.User, int64, error) {
	return s.userRepo.List(page, limit, role)
}

// Update updates a user
func (s *UserService) Update(id uuid.UUID, input *UpdateUserInput) (*models.User, error) {
	user, err := s.userRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if input.Email != "" {
		// Check if email already exists for another user
		existing, err := s.userRepo.GetByEmail(input.Email)
		if err == nil && existing.ID != id {
			return nil, errors.New("email already exists")
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		user.Email = input.Email
	}

	if input.Password != "" {
		passwordHash, err := HashPassword(input.Password)
		if err != nil {
			return nil, err
		}
		user.PasswordHash = &passwordHash
	}

	if input.IsActive != nil {
		user.IsActive = *input.IsActive
	}

	if err := s.userRepo.Update(user); err != nil {
		return nil, err
	}

	return user, nil
}

// Delete deletes a user
func (s *UserService) Delete(id uuid.UUID) error {
	return s.userRepo.Delete(id)
}

// SeedDefaultAdmin creates the default admin user if no users exist in the database.
// The password is hashed at runtime using bcrypt, avoiding pre-computed hash issues.
func (s *UserService) SeedDefaultAdmin(username, password, email string) error {
	// Check if the admin user already exists
	_, err := s.userRepo.GetByUsername(username)
	if err == nil {
		// User already exists, skip seeding
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	// Hash password at runtime
	passwordHash, err := HashPassword(password)
	if err != nil {
		return err
	}

	user := &models.User{
		Username:     username,
		Email:        email,
		PasswordHash: &passwordHash,
		Role:         models.UserRoleOwner,
		IsActive:     true,
	}

	if err := s.userRepo.Create(user); err != nil {
		return err
	}

	log.Printf("Default admin user '%s' created successfully", username)
	return nil
}
