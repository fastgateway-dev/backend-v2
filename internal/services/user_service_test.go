package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserService_Create_Success(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	input := &services.CreateUserInput{
		Username: "testuser",
		Email:    "test@example.com",
		Password: "password123",
		Role:     models.UserRoleUser,
	}

	mockRepo.On("GetByUsername", "testuser").Return(nil, gorm.ErrRecordNotFound)
	mockRepo.On("GetByEmail", "test@example.com").Return(nil, gorm.ErrRecordNotFound)
	mockRepo.On("Create", mock.AnythingOfType("*models.User")).Return(nil)

	user, err := svc.Create(input)

	require.NoError(t, err)
	assert.Equal(t, "testuser", user.Username)
	assert.Equal(t, "test@example.com", user.Email)
	assert.Equal(t, models.UserRoleUser, user.Role)
	assert.True(t, user.IsActive)
	assert.NotNil(t, user.PasswordHash)
	mockRepo.AssertExpectations(t)
}

func TestUserService_Create_DuplicateUsername(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	existingUser := &models.User{Username: "testuser"}
	mockRepo.On("GetByUsername", "testuser").Return(existingUser, nil)

	input := &services.CreateUserInput{
		Username: "testuser",
		Email:    "test@example.com",
		Password: "password123",
		Role:     models.UserRoleUser,
	}

	user, err := svc.Create(input)

	assert.Nil(t, user)
	assert.EqualError(t, err, "username already exists")
	mockRepo.AssertExpectations(t)
}

func TestUserService_Create_DuplicateEmail(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	mockRepo.On("GetByUsername", "testuser").Return(nil, gorm.ErrRecordNotFound)
	existingUser := &models.User{Email: "test@example.com"}
	mockRepo.On("GetByEmail", "test@example.com").Return(existingUser, nil)

	input := &services.CreateUserInput{
		Username: "testuser",
		Email:    "test@example.com",
		Password: "password123",
		Role:     models.UserRoleUser,
	}

	user, err := svc.Create(input)

	assert.Nil(t, user)
	assert.EqualError(t, err, "email already exists")
	mockRepo.AssertExpectations(t)
}

func TestUserService_GetByID_Success(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	id := uuid.New()
	expected := &models.User{ID: id, Username: "testuser"}
	mockRepo.On("GetByID", id).Return(expected, nil)

	user, err := svc.GetByID(id)

	require.NoError(t, err)
	assert.Equal(t, expected, user)
	mockRepo.AssertExpectations(t)
}

func TestUserService_GetByID_NotFound(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	id := uuid.New()
	mockRepo.On("GetByID", id).Return(nil, gorm.ErrRecordNotFound)

	user, err := svc.GetByID(id)

	assert.Nil(t, user)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	mockRepo.AssertExpectations(t)
}

func TestUserService_List(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	users := []models.User{
		{ID: uuid.New(), Username: "user1"},
		{ID: uuid.New(), Username: "user2"},
	}
	mockRepo.On("List", 1, 10, "").Return(users, int64(2), nil)

	result, total, err := svc.List(1, 10, "")

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, int64(2), total)
	mockRepo.AssertExpectations(t)
}

func TestUserService_Update_Success(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	id := uuid.New()
	existing := &models.User{ID: id, Username: "testuser", Email: "old@example.com", IsActive: true}
	mockRepo.On("GetByID", id).Return(existing, nil)
	mockRepo.On("GetByEmail", "new@example.com").Return(nil, gorm.ErrRecordNotFound)
	mockRepo.On("Update", mock.AnythingOfType("*models.User")).Return(nil)

	input := &services.UpdateUserInput{Email: "new@example.com"}
	user, err := svc.Update(id, input)

	require.NoError(t, err)
	assert.Equal(t, "new@example.com", user.Email)
	mockRepo.AssertExpectations(t)
}

func TestUserService_Delete(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	id := uuid.New()
	mockRepo.On("Delete", id).Return(nil)

	err := svc.Delete(id)

	require.NoError(t, err)
	mockRepo.AssertExpectations(t)
}

func TestUserService_Delete_Error(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	id := uuid.New()
	mockRepo.On("Delete", id).Return(errors.New("db error"))

	err := svc.Delete(id)

	assert.EqualError(t, err, "db error")
	mockRepo.AssertExpectations(t)
}

func TestUserService_SeedDefaultAdmin_UserExists(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	existingUser := &models.User{
		ID:       uuid.New(),
		Username: "admin",
		Email:    "admin@example.com",
		Role:     models.UserRoleOwner,
	}
	mockRepo.On("GetByUsername", "admin").Return(existingUser, nil)

	err := svc.SeedDefaultAdmin("admin", "password123", "admin@example.com")

	require.NoError(t, err) // Should succeed silently when user exists
	mockRepo.AssertExpectations(t)
	// Verify Create was NOT called
	mockRepo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestUserService_SeedDefaultAdmin_UserDoesNotExist(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	mockRepo.On("GetByUsername", "admin").Return(nil, gorm.ErrRecordNotFound)
	mockRepo.On("Create", mock.AnythingOfType("*models.User")).Return(nil)

	err := svc.SeedDefaultAdmin("admin", "password123", "admin@example.com")

	require.NoError(t, err)
	mockRepo.AssertExpectations(t)
	// Verify Create was called
	mockRepo.AssertCalled(t, "Create", mock.AnythingOfType("*models.User"))
}

func TestUserService_SeedDefaultAdmin_CreateError(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	mockRepo.On("GetByUsername", "admin").Return(nil, gorm.ErrRecordNotFound)
	mockRepo.On("Create", mock.AnythingOfType("*models.User")).Return(errors.New("db error"))

	err := svc.SeedDefaultAdmin("admin", "password123", "admin@example.com")

	assert.EqualError(t, err, "db error")
	mockRepo.AssertExpectations(t)
}

func TestUserService_SeedDefaultAdmin_LookupError(t *testing.T) {
	mockRepo := new(mocks.MockUserRepository)
	svc := services.NewUserService(mockRepo)

	mockRepo.On("GetByUsername", "admin").Return(nil, errors.New("db connection error"))

	err := svc.SeedDefaultAdmin("admin", "password123", "admin@example.com")

	assert.EqualError(t, err, "db connection error")
	mockRepo.AssertExpectations(t)
}
