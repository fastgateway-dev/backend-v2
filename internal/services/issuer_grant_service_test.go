package services_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

func TestIssuerGrantService_Grant_ValidatesIssuerAndProject(t *testing.T) {
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	issRepo := new(mocks.MockCertificateIssuerRepository)
	projRepo := new(mocks.MockProjectRepository)
	svc := services.NewIssuerGrantService(services.IssuerGrantServiceDeps{GrantRepo: grantRepo, IssuerRepo: issRepo, ProjectRepo: projRepo, ManagedCertRepo: new(mocks.MockManagedCertificateRepository)})

	issID, projID, by := uuid.New(), uuid.New(), uuid.New()
	issRepo.On("GetByID", issID).Return(&models.CertificateIssuer{ID: issID}, nil)
	projRepo.On("GetByID", projID).Return(&models.Project{ID: projID}, nil)
	grantRepo.On("Exists", issID, projID).Return(false, nil)
	grantRepo.On("Create", mock.AnythingOfType("*models.IssuerProjectGrant")).Return(nil)

	require.NoError(t, svc.Grant(issID, projID, by))
	grantRepo.AssertExpectations(t)
}

func TestIssuerGrantService_Revoke_BlockedWhenCertUsesIssuer(t *testing.T) {
	grantRepo := new(mocks.MockIssuerProjectGrantRepository)
	issRepo := new(mocks.MockCertificateIssuerRepository)
	projRepo := new(mocks.MockProjectRepository)
	managedCertRepo := new(mocks.MockManagedCertificateRepository)
	svc := services.NewIssuerGrantService(services.IssuerGrantServiceDeps{
		GrantRepo: grantRepo, IssuerRepo: issRepo, ProjectRepo: projRepo, ManagedCertRepo: managedCertRepo,
	})

	issID, projID := uuid.New(), uuid.New()
	managedCertRepo.On("CountByIssuerAndProject", issID, projID).Return(int64(1), nil)

	err := svc.Revoke(issID, projID)
	require.Error(t, err)
	grantRepo.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything)
}
