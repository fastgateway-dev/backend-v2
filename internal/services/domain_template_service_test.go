package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// GetByID
// ---------------------------------------------------------------------------

func TestDomainTemplateService_GetByID_Success(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	id := uuid.New()
	expected := &models.DomainTemplate{ID: id, Name: "my-template"}
	dtRepo.On("GetByID", id).Return(expected, nil)

	result, err := svc.GetByID(id)

	require.NoError(t, err)
	assert.Equal(t, "my-template", result.Name)
	dtRepo.AssertExpectations(t)
}

func TestDomainTemplateService_GetByID_NotFound(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	id := uuid.New()
	dtRepo.On("GetByID", id).Return(nil, errors.New("not found"))

	_, err := svc.GetByID(id)

	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// ListByProjectID
// ---------------------------------------------------------------------------

func TestDomainTemplateService_ListByProjectID_Success(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	templates := []models.DomainTemplate{
		{ID: uuid.New(), Name: "tpl-a"},
		{ID: uuid.New(), Name: "tpl-b"},
	}
	dtRepo.On("ListByProjectID", projectID, 1, 20).Return(templates, int64(2), nil)

	result, total, err := svc.ListByProjectID(projectID, 1, 20)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, int64(2), total)
	dtRepo.AssertExpectations(t)
}

func TestDomainTemplateService_ListByProjectID_Empty(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	dtRepo.On("ListByProjectID", projectID, 1, 10).Return([]models.DomainTemplate{}, int64(0), nil)

	result, total, err := svc.ListByProjectID(projectID, 1, 10)

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Equal(t, int64(0), total)
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestDomainTemplateService_Delete_RepoGetByIDSuccess(t *testing.T) {
	// Delete requires non-nil k8sService (calls k8sService.DeleteGatewayClass etc.),
	// so we only test the "not found" path here. A full Delete test would need a
	// Kubernetes client mock beyond simple repository mocks.
	t.Skip("Delete with k8sService requires Kubernetes client mocking")
}

func TestDomainTemplateService_Delete_NotFound(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	id := uuid.New()
	dtRepo.On("GetByID", id).Return(nil, errors.New("not found"))

	err := svc.Delete(id)

	require.Error(t, err)
	dtRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// GetByName
// ---------------------------------------------------------------------------

func TestDomainTemplateService_GetByName_Success(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	expected := &models.DomainTemplate{ID: uuid.New(), Name: "my-template", ProjectID: projectID}
	dtRepo.On("GetByName", projectID, "my-template").Return(expected, nil)

	result, err := svc.GetByName(projectID, "my-template")

	require.NoError(t, err)
	assert.Equal(t, "my-template", result.Name)
	dtRepo.AssertExpectations(t)
}

func TestDomainTemplateService_GetByName_NotFound(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	dtRepo.On("GetByName", projectID, "nonexistent").Return(nil, errors.New("not found"))

	_, err := svc.GetByName(projectID, "nonexistent")

	require.Error(t, err)
	dtRepo.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// PreviewCreate - validation paths
// ---------------------------------------------------------------------------

func TestDomainTemplateService_PreviewCreate_InvalidExposureType(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "InvalidType",
		TLSMode:      "tls_only",
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	assert.Nil(t, result)
	assert.EqualError(t, err, "exposure type must be 'LoadBalancer' or 'ClusterIP'")
}

func TestDomainTemplateService_PreviewCreate_InvalidTLSMode(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		TLSMode:      "invalid",
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	assert.Nil(t, result)
	assert.EqualError(t, err, "TLS mode must be 'tls_only', 'no_tls', or 'both'")
}

func TestDomainTemplateService_PreviewCreate_InvalidControllerName(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:           "my-template",
		ExposureType:   "LoadBalancer",
		TLSMode:        "tls_only",
		ControllerName: "some-other-controller",
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	assert.Nil(t, result)
	assert.EqualError(t, err, "only Envoy Gateway controller is currently supported")
}

func TestDomainTemplateService_PreviewCreate_InvalidName(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:         "Invalid Name!",
		ExposureType: "LoadBalancer",
		TLSMode:      "tls_only",
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "name must be lowercase")
}

func TestDomainTemplateService_PreviewCreate_InvalidScalingConfig(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		TLSMode:      "tls_only",
		ScalingConfig: &models.ScalingConfig{
			Type: "invalid",
		},
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	assert.Nil(t, result)
	assert.EqualError(t, err, "scaling type must be 'fixed' or 'hpa'")
}

func TestDomainTemplateService_PreviewCreate_InvalidPort(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		TLSMode:      "tls_only",
		HTTPPort:     99999,
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	assert.Nil(t, result)
	assert.EqualError(t, err, "HTTP port must be between 1 and 65535")
}

func TestDomainTemplateService_PreviewCreate_Success(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		TLSMode:      "tls_only",
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	require.NoError(t, err)
	assert.NotEmpty(t, result.GatewayClassYaml)
	assert.NotEmpty(t, result.EnvoyProxyYaml)
	assert.NotEmpty(t, result.GatewayYaml)
	assert.Nil(t, result.AIReview)
}

func TestDomainTemplateService_PreviewCreate_InvalidTLSPolicy(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		TLSMode:      "tls_only",
		TLSPolicy:    "invalid-policy",
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	assert.Nil(t, result)
	assert.EqualError(t, err, "TLS policy must be 'terminate' or 'passthrough'")
}

func TestDomainTemplateService_PreviewCreate_InvalidExternalTrafficPolicy(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:                  "my-template",
		ExposureType:          "LoadBalancer",
		TLSMode:               "tls_only",
		ExternalTrafficPolicy: "Invalid",
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	assert.Nil(t, result)
	assert.EqualError(t, err, "external traffic policy must be 'Cluster' or 'Local'")
}

// ---------------------------------------------------------------------------
// validateScalingConfig - more paths via PreviewCreate
// ---------------------------------------------------------------------------

func TestDomainTemplateService_PreviewCreate_FixedScalingNoReplicas(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		TLSMode:      "tls_only",
		ScalingConfig: &models.ScalingConfig{
			Type: "fixed",
		},
	}

	_, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)
	assert.EqualError(t, err, "fixed scaling requires replicas >= 1")
}

func TestDomainTemplateService_PreviewCreate_HPAMissingMin(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		TLSMode:      "tls_only",
		ScalingConfig: &models.ScalingConfig{
			Type: "hpa",
		},
	}

	_, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)
	assert.EqualError(t, err, "HPA scaling requires minReplicas >= 1")
}

func TestDomainTemplateService_PreviewCreate_HPAMissingMax(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	min := int32(2)
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		TLSMode:      "tls_only",
		ScalingConfig: &models.ScalingConfig{
			Type:        "hpa",
			MinReplicas: &min,
		},
	}

	_, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)
	assert.EqualError(t, err, "HPA scaling requires maxReplicas >= 1")
}

func TestDomainTemplateService_PreviewCreate_HPAMaxLessThanMin(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	min := int32(5)
	max := int32(2)
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		TLSMode:      "tls_only",
		ScalingConfig: &models.ScalingConfig{
			Type:        "hpa",
			MinReplicas: &min,
			MaxReplicas: &max,
		},
	}

	_, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)
	assert.EqualError(t, err, "HPA maxReplicas must be >= minReplicas")
}

// ---------------------------------------------------------------------------
// NormalizeEmptyTelemetryMetrics
// ---------------------------------------------------------------------------

func TestDomainTemplate_NormalizeEmptyMetrics_StoresNil(t *testing.T) {
	in := &models.TelemetryMetricsConfig{
		Prometheus:             nil,
		EnableVirtualHostStats: false,
		EnablePerEndpointStats: false,
		Sinks:                  nil,
	}
	got := services.NormalizeEmptyTelemetryMetrics(in)
	assert.Nil(t, got)
}

func TestDomainTemplate_NormalizeEmptyMetrics_PreservesNonDefault(t *testing.T) {
	in := &models.TelemetryMetricsConfig{EnableVirtualHostStats: true}
	got := services.NormalizeEmptyTelemetryMetrics(in)
	assert.Equal(t, in, got)
}

func TestDomainTemplate_NormalizeEmptyMetrics_PromExplicitlyEnabled(t *testing.T) {
	in := &models.TelemetryMetricsConfig{Prometheus: &models.TelemetryPrometheusConfig{Disable: false}}
	got := services.NormalizeEmptyTelemetryMetrics(in)
	assert.NotNil(t, got)
	assert.NotNil(t, got.Prometheus)
}

// ---------------------------------------------------------------------------
// ValidateDomainTemplateTelemetry
// ---------------------------------------------------------------------------

func TestDomainTemplate_Validate_RejectsBadAccessLog(t *testing.T) {
	dt := &models.DomainTemplate{
		Name: "bad-al",
		TelemetryAccessLog: &models.TelemetryAccessLogConfig{
			Format: models.TelemetryAccessLogFormat{Type: "text", Text: ""},
			Sink:   models.TelemetryAccessLogSink{Type: "file", File: &models.TelemetryAccessLogFileSink{Path: "/dev/stdout"}},
		},
	}
	err := services.ValidateDomainTemplateTelemetry(dt)
	assert.Error(t, err)
}

func TestDomainTemplate_Validate_AcceptsAllNil(t *testing.T) {
	dt := &models.DomainTemplate{Name: "ok"}
	assert.NoError(t, services.ValidateDomainTemplateTelemetry(dt))
}

// ---------------------------------------------------------------------------
// NormalizeEmptyPodPlacement
// ---------------------------------------------------------------------------

func TestDomainTemplate_NormalizeEmptyPodPlacement_StoresNil(t *testing.T) {
	in := &models.PodPlacementConfig{}
	got := services.NormalizeEmptyPodPlacement(in)
	assert.Nil(t, got)
}

func TestDomainTemplate_NormalizeEmptyPodPlacement_PreservesAnyField(t *testing.T) {
	cases := []*models.PodPlacementConfig{
		{NodeSelector: map[string]string{"k": "v"}},
		{Tolerations: []models.TolerationConfig{{Key: "k", Operator: "Exists"}}},
		{TopologySpreadConstraints: []models.TopologySpreadConstraintConfig{{MaxSkew: 1, TopologyKey: "z", WhenUnsatisfiable: "ScheduleAnyway"}}},
		{PriorityClassName: "high"},
	}
	for _, in := range cases {
		got := services.NormalizeEmptyPodPlacement(in)
		assert.NotNil(t, got, "non-empty config should be preserved")
	}
}

// ---------------------------------------------------------------------------
// ValidateDomainTemplatePodScheduling
// ---------------------------------------------------------------------------

func TestDomainTemplate_ValidatePodScheduling_RejectsBadPDB(t *testing.T) {
	dt := &models.DomainTemplate{
		Name:      "bad-pdb",
		PDBConfig: &models.PDBConfig{Kind: "either", Amount: "1"},
	}
	assert.Error(t, services.ValidateDomainTemplatePodScheduling(dt))
}

func TestDomainTemplate_ValidatePodScheduling_AcceptsAllNil(t *testing.T) {
	dt := &models.DomainTemplate{Name: "ok"}
	assert.NoError(t, services.ValidateDomainTemplatePodScheduling(dt))
}
