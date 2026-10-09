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
	input := &services.CreateDomainInput{Name: "d", Hostname: "new.example.com", DomainTemplateID: dtID.String()}
	dt := &models.DomainTemplate{
		ID: dtID, ProjectID: projectID, Name: "tpl", Status: models.DomainTemplateStatusActive,
		MergeGateways: merged, EnableDomain: true, HTTPPort: 80, HTTPSPort: 443,
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

func TestDomainTemplateService_Update_EnableDomainOnMerged_RejectsStreamHittingDomainPort(t *testing.T) {
	id := uuid.New()
	tmpl := &models.DomainTemplate{ID: id, MergeGateways: true, EnableDomain: false, EnableStream: true, HTTPPort: 80, HTTPSPort: 443}
	// A stream already serves tcp:8443 and a domain on the template uses 8443.
	svc, dtRepo, _ := newPortCheckedTemplateService(t, tmpl,
		[]services.PortUse{{Transport: "TCP", Port: 8443}},
		[]services.PortUse{{Transport: "TCP", Port: 8443}})

	tru := true
	result, err := svc.Update(id, &services.UpdateDomainTemplateInput{EnableDomain: &tru})

	assert.Nil(t, result)
	assert.ErrorIs(t, err, services.ErrPortCollision)
	dtRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestDomainTemplateService_Update_EnableDomainOnMerged_RejectsStreamOnTemplateHTTPSPort(t *testing.T) {
	id := uuid.New()
	tmpl := &models.DomainTemplate{ID: id, MergeGateways: true, EnableDomain: false, EnableStream: true, HTTPPort: 80, HTTPSPort: 443}
	// Enabling domains reserves 80/443 for HTTP/HTTPS: a stream on tcp:443 clashes.
	svc, dtRepo, _ := newPortCheckedTemplateService(t, tmpl, []services.PortUse{{Transport: "TCP", Port: 443}}, nil)

	tru := true
	_, err := svc.Update(id, &services.UpdateDomainTemplateInput{EnableDomain: &tru})

	assert.ErrorIs(t, err, services.ErrPortCollision)
	dtRepo.AssertNotCalled(t, "Update", mock.Anything)
}

func TestDomainTemplateService_Update_EnableDomainOnMerged_NoClash_Succeeds(t *testing.T) {
	id := uuid.New()
	tmpl := &models.DomainTemplate{ID: id, MergeGateways: true, EnableDomain: false, EnableStream: true, HTTPPort: 80, HTTPSPort: 443}
	svc, _, _ := newPortCheckedTemplateService(t, tmpl,
		[]services.PortUse{{Transport: "TCP", Port: 5432}, {Transport: "UDP", Port: 443}}, nil)

	tru := true
	result, err := svc.Update(id, &services.UpdateDomainTemplateInput{EnableDomain: &tru})

	require.NoError(t, err)
	assert.True(t, result.EnableDomain)
}

func TestDomainTemplateService_Update_NotMergedOrNoNewFlag_SkipsCheck(t *testing.T) {
	// Unmerged template: enabling a flag cannot create a shared listener set.
	id := uuid.New()
	unmerged := &models.DomainTemplate{ID: id, MergeGateways: false, EnableDomain: false, EnableStream: true}
	svc, _, streams := newPortCheckedTemplateService(t, unmerged, []services.PortUse{{Transport: "TCP", Port: 443}}, nil)
	tru := true
	_, err := svc.Update(id, &services.UpdateDomainTemplateInput{EnableDomain: &tru})
	require.NoError(t, err)
	assert.Zero(t, streams.calls)

	// Merged template, but the update enables nothing new.
	id2 := uuid.New()
	merged := &models.DomainTemplate{ID: id2, MergeGateways: true, EnableDomain: true, EnableStream: true}
	svc, _, streams = newPortCheckedTemplateService(t, merged, []services.PortUse{{Transport: "TCP", Port: 443}}, nil)
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

func TestDomainTemplateService_Update_EnableOnMerged_UnwiredFailsClosed(t *testing.T) {
	dtRepo := new(mocks.MockDomainTemplateRepository)
	svc := services.NewDomainTemplateService(dtRepo, nil, nil, nil, nil)
	id := uuid.New()
	dtRepo.On("GetByID", id).Return(&models.DomainTemplate{ID: id, MergeGateways: true, EnableDomain: false, EnableStream: true}, nil)

	tru := true
	result, err := svc.Update(id, &services.UpdateDomainTemplateInput{EnableDomain: &tru})

	assert.Nil(t, result)
	assert.ErrorContains(t, err, "not configured")
	dtRepo.AssertNotCalled(t, "Update", mock.Anything)
}
