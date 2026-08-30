package mocks

import (
	"context"

	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

// Compile-time interface satisfaction check
var _ services.TopologyServiceInterface = (*MockTopologyService)(nil)

// MockTopologyService is a testify mock implementation of TopologyServiceInterface.
type MockTopologyService struct {
	mock.Mock
}

func (m *MockTopologyService) GetProjectTopology(ctx context.Context, projectID uuid.UUID) (*services.ProjectTopologyResponse, error) {
	args := m.Called(ctx, projectID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.ProjectTopologyResponse), args.Error(1)
}

func (m *MockTopologyService) GetDomainTopology(ctx context.Context, projectID, domainID uuid.UUID) (*services.DomainTopologyResponse, error) {
	args := m.Called(ctx, projectID, domainID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.DomainTopologyResponse), args.Error(1)
}
