package services_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeL4PortReader returns a canned set of stream L4 ports for any template.
type fakeL4PortReader struct {
	used  []services.PortUse
	calls int
}

func (f *fakeL4PortReader) UsedL4Ports(uuid.UUID, *uuid.UUID) ([]services.PortUse, error) {
	f.calls++
	return f.used, nil
}

// fakeDomainPortReader returns a canned set of domain ports for any template.
type fakeDomainPortReader struct{ used []services.PortUse }

func (f *fakeDomainPortReader) UsedPortsByTemplate(uuid.UUID) ([]services.PortUse, error) {
	return f.used, nil
}

// ---------------------------------------------------------------------------
// Domain create: a merged template's domain ports must not hit a stream L4 port
// ---------------------------------------------------------------------------

func domainCreateFixture(merged bool) (uuid.UUID, uuid.UUID, *services.CreateDomainInput, *models.DomainTemplate) {
	projectID, dtID := uuid.New(), uuid.New()
	input := &services.CreateDomainInput{
		Name: "d", Hostname: "new.example.com", DomainTemplateID: dtID.String(),
		BoundListeners: []string{"http", "https"}, TLSSecretName: "tls",
	}
	dt := &models.DomainTemplate{
		ID: dtID, ProjectID: projectID, Name: "tpl", Status: models.DomainTemplateStatusActive,
		MergeGateways: merged,
		Listeners: models.Listeners{
			{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
			{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate},
		},
	}
	return projectID, dtID, input, dt
}

func TestDomainService_Create_Merged_RejectsPortHittingStreamL4Port(t *testing.T) {
	for _, port := range []int{80, 443} {
		svc, domainRepo, _, dtRepo, _, _, _ := newTestDomainService()
		projectID, dtID, input, dt := domainCreateFixture(true)
		streams := &fakeL4PortReader{used: []services.PortUse{{Transport: "TCP", Port: port}}}
		svc.SetStreamPorts(streams)

		domainRepo.On("ExistsByHostname", projectID, input.Hostname).Return(false, nil)
		dtRepo.On("GetByID", dtID).Return(dt, nil)

		result, err := svc.Create(projectID, input, uuid.New())

		assert.Nil(t, result)
		assert.ErrorIs(t, err, services.ErrPortCollision, "stream tcp/%d on a merged template", port)
		domainRepo.AssertNotCalled(t, "Create", mock.Anything)
	}
}

func TestDomainService_Create_Merged_UDPStreamPortDoesNotCollide(t *testing.T) {
	svc, domainRepo, _, dtRepo, k8s, _, _ := newTestDomainService()
	projectID, dtID, input, dt := domainCreateFixture(true)
	svc.SetStreamPorts(&fakeL4PortReader{used: []services.PortUse{{Transport: "UDP", Port: 443}}})

	domainRepo.On("ExistsByHostname", projectID, input.Hostname).Return(false, nil)
	dtRepo.On("GetByID", dtID).Return(dt, nil)
	domainRepo.On("Create", mock.Anything).Return(nil)
	domainRepo.On("Update", mock.Anything).Return(nil)
	k8s.On("CreateGateway", mock.Anything, projectID, mock.Anything).Return(nil)

	_, err := svc.Create(projectID, input, uuid.New())
	assert.NotErrorIs(t, err, services.ErrPortCollision)
}

func TestDomainService_Create_NotMerged_SkipsStreamPortCheck(t *testing.T) {
	svc, domainRepo, _, dtRepo, _, _, _ := newTestDomainService()
	projectID, dtID, input, dt := domainCreateFixture(false)
	streams := &fakeL4PortReader{used: []services.PortUse{{Transport: "TCP", Port: 443}}}
	svc.SetStreamPorts(streams)

	domainRepo.On("ExistsByHostname", projectID, input.Hostname).Return(false, nil)
	dtRepo.On("GetByID", dtID).Return(dt, nil)
	// Stop the create right after the port check with a downstream error.
	domainRepo.On("Create", mock.Anything).Return(assert.AnError)

	_, err := svc.Create(projectID, input, uuid.New())
	assert.NotErrorIs(t, err, services.ErrPortCollision)
	assert.Zero(t, streams.calls, "an unmerged template has per-domain Gateways: no stream-port scan")
}

// ---------------------------------------------------------------------------
// Template update: enabling a flag on a merged template re-validates the set
// ---------------------------------------------------------------------------

func newPortCheckedTemplateService(t *testing.T, tmpl *models.DomainTemplate, streamUsed, domainUsed []services.PortUse) (*services.DomainTemplateService, *mocks.MockDomainTemplateRepository, *fakeL4PortReader) {
	t.Helper()
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)
	streams := &fakeL4PortReader{used: streamUsed}
	svc.SetPortSources(streams, &fakeDomainPortReader{used: domainUsed})
	dtRepo.On("GetByID", tmpl.ID).Return(tmpl, nil)
	dtRepo.On("Update", mock.Anything).Return(nil).Maybe()
	return svc, dtRepo, streams
}

// streamOnlyMerged is a merged template with only a TCP/UDP stream range; the
// updates below add hostname-routed listeners to it.
func streamOnlyMerged(id uuid.UUID, merged bool) *models.DomainTemplate {
	return &models.DomainTemplate{
		ID: id, MergeGateways: merged,
		Listeners: models.Listeners{{Name: "stream", Protocol: models.ListenerTCP, PortRangeMin: 1024, PortRangeMax: 65535}},
	}
}

func httpHTTPSListeners() *models.Listeners {
	return &models.Listeners{
		{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
		{Name: "https", Protocol: models.ListenerHTTPS, Port: 443},
	}
}

func TestDomainTemplateService_Update_ListenersOnMerged_RejectsStreamHittingDomainPort(t *testing.T) {
	id := uuid.New()
	tmpl := streamOnlyMerged(id, true)
	// A stream already serves tcp:8443 and a domain on the template uses 8443.
	svc, dtRepo, _ := newPortCheckedTemplateService(t, tmpl,
		[]services.PortUse{{Transport: "TCP", Port: 8443}},
		[]services.PortUse{{Transport: "TCP", Port: 8443}})

	result, err := svc.Update(id, &services.UpdateDomainTemplateInput{Listeners: httpHTTPSListeners()})

	assert.Nil(t, result)
	assert.ErrorIs(t, err, services.ErrPortCollision)
	dtRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestDomainTemplateService_Update_ListenersOnMerged_RejectsStreamOnTemplateHTTPSPort(t *testing.T) {
	id := uuid.New()
	tmpl := streamOnlyMerged(id, true)
	// The new HTTP/HTTPS listeners claim 80/443: a stream on tcp:443 clashes.
	svc, dtRepo, _ := newPortCheckedTemplateService(t, tmpl, []services.PortUse{{Transport: "TCP", Port: 443}}, nil)

	_, err := svc.Update(id, &services.UpdateDomainTemplateInput{Listeners: httpHTTPSListeners()})

	assert.ErrorIs(t, err, services.ErrPortCollision)
	dtRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestDomainTemplateService_Update_ListenersOnMerged_NoClash_Succeeds(t *testing.T) {
	id := uuid.New()
	tmpl := streamOnlyMerged(id, true)
	svc, _, _ := newPortCheckedTemplateService(t, tmpl,
		[]services.PortUse{{Transport: "TCP", Port: 5432}, {Transport: "UDP", Port: 443}}, nil)

	result, err := svc.Update(id, &services.UpdateDomainTemplateInput{Listeners: httpHTTPSListeners()})

	require.NoError(t, err)
	assert.Len(t, result.Listeners, 2)
}

func TestDomainTemplateService_Update_Listeners_InvalidRejected(t *testing.T) {
	id := uuid.New()
	svc, dtRepo, _ := newPortCheckedTemplateService(t, streamOnlyMerged(id, false), nil, nil)

	empty := models.Listeners{}
	_, err := svc.Update(id, &services.UpdateDomainTemplateInput{Listeners: &empty})

	assert.ErrorIs(t, err, services.ErrNoListener)
	dtRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestDomainTemplateService_Update_NotMergedOrNoListeners_SkipsCheck(t *testing.T) {
	// Unmerged template: changing listeners cannot create a shared listener set.
	id := uuid.New()
	svc, _, streams := newPortCheckedTemplateService(t, streamOnlyMerged(id, false), []services.PortUse{{Transport: "TCP", Port: 443}}, nil)
	_, err := svc.Update(id, &services.UpdateDomainTemplateInput{Listeners: httpHTTPSListeners()})
	require.NoError(t, err)
	assert.Zero(t, streams.calls)

	// Merged template, but the update changes no listeners.
	id2 := uuid.New()
	svc, _, streams = newPortCheckedTemplateService(t, streamOnlyMerged(id2, true), []services.PortUse{{Transport: "TCP", Port: 443}}, nil)
	_, err = svc.Update(id2, &services.UpdateDomainTemplateInput{Description: "x"})
	require.NoError(t, err)
	assert.Zero(t, streams.calls)
}

func TestDomainService_Create_Merged_UnwiredStreamPortsFailsClosed(t *testing.T) {
	svc, domainRepo, _, dtRepo, _, _, _ := newTestDomainService()
	projectID, dtID, input, dt := domainCreateFixture(true)

	domainRepo.On("ExistsByHostname", projectID, input.Hostname).Return(false, nil)
	dtRepo.On("GetByID", dtID).Return(dt, nil)

	result, err := svc.Create(projectID, input, uuid.New())

	assert.Nil(t, result)
	assert.ErrorContains(t, err, "not configured")
	domainRepo.AssertNotCalled(t, "Create", mock.Anything)
}

func TestDomainTemplateService_Update_ListenersOnMerged_UnwiredFailsClosed(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)
	id := uuid.New()
	dtRepo.On("GetByID", id).Return(streamOnlyMerged(id, true), nil)

	result, err := svc.Update(id, &services.UpdateDomainTemplateInput{Listeners: httpHTTPSListeners()})

	assert.Nil(t, result)
	assert.ErrorContains(t, err, "not configured")
	dtRepo.AssertNotCalled(t, "Update", mock.Anything)
}
