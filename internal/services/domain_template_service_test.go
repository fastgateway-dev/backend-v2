package services_test

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// previewListeners is a minimal valid listener list (one HTTPS Terminate
// listener) for tests that exercise other PreviewCreate validation paths.
var previewListeners = []models.TemplateListener{
	{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate},
}

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
	dtRepo.On("ListByProjectID", projectID, 1, 20, "").Return(templates, int64(2), nil)

	result, total, err := svc.ListByProjectID(projectID, 1, 20, "")

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, int64(2), total)
	dtRepo.AssertExpectations(t)
}

func TestDomainTemplateService_ListByProjectID_Empty(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	dtRepo.On("ListByProjectID", projectID, 1, 10, "").Return([]models.DomainTemplate{}, int64(0), nil)

	result, total, err := svc.ListByProjectID(projectID, 1, 10, "")

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

// stubStreamCounter satisfies the template service's stream-side dependency
// (UsedL4Ports for merged-listener validation + CountByGatewayTemplateID for
// the delete in-use guard) without a generated mock.
type stubStreamCounter struct {
	count int64
	err   error
}

func (s stubStreamCounter) UsedL4Ports(uuid.UUID, *uuid.UUID) ([]services.PortUse, error) {
	return nil, nil
}
func (s stubStreamCounter) CountByGatewayTemplateID(uuid.UUID) (int64, error) {
	return s.count, s.err
}

// TestDomainTemplateService_Delete_RejectsWhenDomainsExist is the in-use guard:
// a template still referenced by a domain must not be deleted (deleting it
// would orphan the domain and, on its next apply, drop traffic). The guard runs
// BEFORE any Kubernetes teardown, so it needs no k8s mock.
func TestDomainTemplateService_Delete_RejectsWhenDomainsExist(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	domainRepo := new(mocks.MockDomainRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, domainRepo, nil, nil)

	id := uuid.New()
	dtRepo.On("GetByID", id).Return(&models.DomainTemplate{ID: id, Name: "tpl"}, nil)
	domainRepo.On("ListByTemplateID", id).Return([]models.Domain{{ID: uuid.New()}}, nil)

	err := svc.Delete(id)

	require.Error(t, err)
	assert.True(t, errors.Is(err, services.ErrDomainTemplateInUse))
	dtRepo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
}

// TestDomainTemplateService_Delete_RejectsWhenStreamsExist covers the stream
// side of the guard: a template with no domains but a stream on it (even a
// route-less one) must not be deleted.
func TestDomainTemplateService_Delete_RejectsWhenStreamsExist(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	domainRepo := new(mocks.MockDomainRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, domainRepo, nil, nil)
	svc.SetPortSources(stubStreamCounter{count: 1}, nil)

	id := uuid.New()
	dtRepo.On("GetByID", id).Return(&models.DomainTemplate{ID: id, Name: "tpl"}, nil)
	domainRepo.On("ListByTemplateID", id).Return([]models.Domain{}, nil)

	err := svc.Delete(id)

	require.Error(t, err)
	assert.True(t, errors.Is(err, services.ErrDomainTemplateInUse))
	dtRepo.AssertExpectations(t)
	domainRepo.AssertExpectations(t)
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
		Listeners:    previewListeners,
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	assert.Nil(t, result)
	assert.EqualError(t, err, "exposure type must be 'LoadBalancer' or 'ClusterIP'")
}

func TestDomainTemplateService_PreviewCreate_NoListeners(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		Listeners:    nil,
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	assert.Nil(t, result)
	assert.ErrorIs(t, err, services.ErrNoListener)
}

func TestDomainTemplateService_PreviewCreate_InvalidControllerName(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:           "my-template",
		ExposureType:   "LoadBalancer",
		Listeners:      previewListeners,
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
		Listeners:    previewListeners,
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
		Listeners:    previewListeners,
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
		Listeners:    []models.TemplateListener{{Name: "http", Protocol: models.ListenerHTTP, Port: 99999}},
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	assert.Nil(t, result)
	assert.ErrorIs(t, err, services.ErrInvalidListenerPort)
}

func TestDomainTemplateService_PreviewCreate_Success(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		Listeners:    previewListeners,
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	require.NoError(t, err)
	assert.NotEmpty(t, result.GatewayClassYaml)
	assert.NotEmpty(t, result.EnvoyProxyYaml)
	assert.NotEmpty(t, result.GatewayYaml)
	assert.Nil(t, result.AIReview)
	// The example Gateway binds every hostname-routed listener of the template.
	assert.Contains(t, result.GatewayYaml, "name: https")
	assert.Contains(t, result.GatewayYaml, "port: 443")
}

func TestDomainTemplateService_PreviewCreate_ExampleGatewayBindsAllHostnameListeners(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		Listeners: []models.TemplateListener{
			{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
			{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate},
			{Name: "tcpudp", Protocol: models.ListenerTCP, PortRangeMin: 9000, PortRangeMax: 9100},
		},
	}

	result, err := svc.PreviewCreate(uuid.New(), input, uuid.New(), nil)

	require.NoError(t, err)
	assert.Contains(t, result.GatewayYaml, "name: http")
	assert.Contains(t, result.GatewayYaml, "name: https")
	// The stream range belongs to stream Gateways, not the domain example.
	assert.NotContains(t, result.GatewayYaml, "tcpudp")
}

func TestDomainTemplateService_PreviewCreate_TLSPassthroughNotSupported(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		Listeners:    []models.TemplateListener{{Name: "tls", Protocol: models.ListenerTLS, Port: 8443, TLSMode: models.TLSListenerPassthrough}},
	}

	result, err := svc.PreviewCreate(projectID, input, uuid.New(), nil)

	assert.Nil(t, result)
	assert.ErrorIs(t, err, services.ErrTLSPassthroughNotSupported)
}

func TestDomainTemplateService_PreviewCreate_InvalidExternalTrafficPolicy(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)

	projectID := uuid.New()
	input := &services.CreateDomainTemplateInput{
		Name:                  "my-template",
		ExposureType:          "LoadBalancer",
		Listeners:             previewListeners,
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
		Listeners:    previewListeners,
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
		Listeners:    previewListeners,
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
		Listeners:    previewListeners,
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
		Listeners:    previewListeners,
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

// ---------------------------------------------------------------------------
// ListByProjectID capability filter (integration; needs INTEGRATION_DB_URL)
// ---------------------------------------------------------------------------

func TestDomainTemplateService_List_CapabilityFilter(t *testing.T) {
	db := requireTopologyDB(t)

	userID := uuid.New()
	projectID := uuid.New()
	suffix := projectID.String()
	require.NoError(t, db.Exec(`INSERT INTO users (id, username, email, password_hash, role, is_active, created_at, updated_at)
		VALUES (?, ?, ?, '', 'owner', true, NOW(), NOW())`,
		userID, "u-"+suffix, "u-"+suffix+"@example.com").Error)
	require.NoError(t, db.Exec(`INSERT INTO projects (id, name, k8s_api_url, k8s_token_encrypted, created_by, created_at, updated_at)
		VALUES (?, ?, '', '', ?, NOW(), NOW())`, projectID, "p-"+suffix, userID).Error)
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM domain_templates WHERE project_id = ?`, projectID).Error
		_ = db.Exec(`DELETE FROM projects WHERE id = ?`, projectID).Error
		_ = db.Exec(`DELETE FROM users WHERE id = ?`, userID).Error
	})

	// Capability is inferred from the listeners column: a domain template has
	// an HTTP/HTTPS listener, a stream template a TCP/UDP range, and a merged
	// template both.
	httpsL := models.TemplateListener{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate}
	rangeL := models.TemplateListener{Name: "tcpudp", Protocol: models.ListenerTCP, PortRangeMin: 1024, PortRangeMax: 65535}
	fixtures := []struct {
		name      string
		listeners models.Listeners
	}{
		{"tmplA", models.Listeners{httpsL}},
		{"tmplB", models.Listeners{rangeL}},
		{"tmplC", models.Listeners{httpsL, rangeL}},
	}
	for _, f := range fixtures {
		require.NoError(t, db.Create(&models.DomainTemplate{
			ProjectID: projectID,
			Name:      f.name,
			Listeners: f.listeners,
			CreatedBy: userID,
		}).Error)
	}

	svc := services.NewDomainTemplateService(repository.NewDomainTemplateRepository(db), nil, nil, nil, nil)

	names := func(ts []models.DomainTemplate) []string {
		out := make([]string, 0, len(ts))
		for _, tmpl := range ts {
			out = append(out, tmpl.Name)
		}
		return out
	}

	stream, total, err := svc.ListByProjectID(projectID, 1, 100, "stream")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"tmplB", "tmplC"}, names(stream))
	assert.Equal(t, int64(2), total)

	domain, total, err := svc.ListByProjectID(projectID, 1, 100, "domain")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"tmplA", "tmplC"}, names(domain))
	assert.Equal(t, int64(2), total)

	all, total, err := svc.ListByProjectID(projectID, 1, 100, "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"tmplA", "tmplB", "tmplC"}, names(all))
	assert.Equal(t, int64(3), total)
}
