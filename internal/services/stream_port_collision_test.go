package services_test

import (
	"encoding/json"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/mocks"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/fastgateway-dev/backend-v2/internal/streamplan"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// collisionEnv is a seeded project (user + team + project) plus a real,
// Postgres-backed StreamService wired with the port sources.
type collisionEnv struct {
	db        *gorm.DB
	projectID uuid.UUID
	teamID    uuid.UUID
	userID    uuid.UUID
	svc       *services.StreamService
}

func newCollisionEnv(t *testing.T) *collisionEnv {
	t.Helper()
	// Silent logger: the not-found test would otherwise print gorm's
	// "record not found" trace.
	db := requireTopologyDB(t).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	e := &collisionEnv{db: db, projectID: uuid.New(), teamID: uuid.New(), userID: uuid.New()}
	suffix := e.projectID.String()

	require.NoError(t, db.Exec(`INSERT INTO users (id, username, email, password_hash, role, is_active, created_at, updated_at)
		VALUES (?, ?, ?, '', 'owner', true, NOW(), NOW())`,
		e.userID, "u-"+suffix, "u-"+suffix+"@example.com").Error)
	require.NoError(t, db.Exec(`INSERT INTO teams (id, name, created_at, updated_at) VALUES (?, ?, NOW(), NOW())`,
		e.teamID, "team-"+suffix).Error)
	require.NoError(t, db.Exec(`INSERT INTO projects (id, name, k8s_api_url, k8s_token_encrypted, created_by, created_at, updated_at)
		VALUES (?, ?, '', '', ?, NOW(), NOW())`, e.projectID, "p-"+suffix, e.userID).Error)

	t.Cleanup(func() {
		// routes cascade from streams; domains and templates are removed explicitly.
		_ = db.Exec(`DELETE FROM routes WHERE team_id = ?`, e.teamID).Error
		_ = db.Exec(`DELETE FROM streams WHERE project_id = ?`, e.projectID).Error
		_ = db.Exec(`DELETE FROM domains WHERE project_id = ?`, e.projectID).Error
		_ = db.Exec(`DELETE FROM domain_templates WHERE project_id = ?`, e.projectID).Error
		_ = db.Exec(`DELETE FROM projects WHERE id = ?`, e.projectID).Error
		_ = db.Exec(`DELETE FROM teams WHERE id = ?`, e.teamID).Error
		_ = db.Exec(`DELETE FROM users WHERE id = ?`, e.userID).Error
	})

	applier := mocks.NewMockGatewayApplier(t)
	applier.EXPECT().CreateGateway(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	streamRepo := repository.NewStreamRepository(db)
	e.svc = services.NewStreamService(streamRepo, repository.NewDomainTemplateRepository(db),
		repository.NewRouteRepository(db), applier, allowAllNamespaces{})
	e.svc.SetPortSources(streamRepo, repository.NewDomainRepository(db))
	return e
}

// template inserts a Gateway Template. Capability flags and MergeGateways are
// set explicitly (EnableDomain has no GORM default).
func (e *collisionEnv) template(t *testing.T, merge bool) uuid.UUID {
	t.Helper()
	tmpl := &models.DomainTemplate{
		ProjectID: e.projectID, Name: "tmpl-" + uuid.NewString(), CreatedBy: e.userID,
		EnableDomain: true, EnableStream: true, MergeGateways: merge,
	}
	require.NoError(t, e.db.Create(tmpl).Error)
	return tmpl.ID
}

func (e *collisionEnv) stream(t *testing.T, tmplID uuid.UUID) uuid.UUID {
	t.Helper()
	name := "s-" + uuid.NewString()[:8]
	s := &models.Stream{ProjectID: e.projectID, Name: name, Namespace: "fastgateway-system",
		GatewayTemplateID: tmplID, K8sGatewayName: "str-" + name, K8sGatewayClass: "public-lb"}
	require.NoError(t, repository.NewStreamRepository(e.db).Create(s))
	return s.ID
}

func (e *collisionEnv) domain(t *testing.T, tmplID uuid.UUID, httpPort, httpsPort int) {
	t.Helper()
	id := uuid.New()
	require.NoError(t, e.db.Exec(`INSERT INTO domains (id, project_id, domain_template_id, name, hostname, http_port, https_port, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		id, e.projectID, tmplID, "d-"+id.String(), id.String()+".test.example.com", httpPort, httpsPort, e.userID).Error)
}

// l4Route inserts an L4 route of the given protocol ("tcp"/"udp") on a stream.
func (e *collisionEnv) l4Route(t *testing.T, streamID uuid.UUID, protocol string, port int, status string) uuid.UUID {
	t.Helper()
	cfg, err := json.Marshal(map[string]any{"listenerPort": port})
	require.NoError(t, err)
	id := uuid.New()
	require.NoError(t, e.db.Exec(`INSERT INTO routes (id, stream_id, team_id, name, protocol, security_mode, status, config, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'general', ?, ?::jsonb, ?, NOW(), NOW())`,
		id, streamID, e.teamID, "r-"+id.String(), protocol, status, string(cfg), e.userID).Error)
	return id
}

func TestCheckPortCollision_SamePortSameProto_WithinStream(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.template(t, false))
	e.l4Route(t, streamID, "tcp", 5432, "active")

	err := e.svc.CheckPortCollision(streamID, "TCP", 5432, nil)
	require.ErrorIs(t, err, services.ErrPortCollision)
	assert.Contains(t, err.Error(), "TCP/5432")
}

func TestCheckPortCollision_TCPvsUDP_SameNumber_OK(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.template(t, false))
	e.l4Route(t, streamID, "tcp", 53, "active")

	assert.NoError(t, e.svc.CheckPortCollision(streamID, "UDP", 53, nil))
	// ...but another TCP/53 still collides.
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 53, nil), services.ErrPortCollision)
}

func TestCheckPortCollision_Merged_HitsDomainHTTPSPort(t *testing.T) {
	e := newCollisionEnv(t)
	tmplID := e.template(t, true)
	e.domain(t, tmplID, 80, 443)
	streamID := e.stream(t, tmplID)

	// HTTPS 443 and HTTP 80 are TCP-transport listeners on the merged class.
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 443, nil), services.ErrPortCollision)
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 80, nil), services.ErrPortCollision)
	// UDP/443 is a different transport; an unused TCP port is free.
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "UDP", 443, nil))
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 5432, nil))
}

func TestCheckPortCollision_NotMerged_OtherStreamSamePort_OK(t *testing.T) {
	e := newCollisionEnv(t)
	tmplID := e.template(t, false)
	streamA := e.stream(t, tmplID)
	streamB := e.stream(t, tmplID)
	e.l4Route(t, streamA, "tcp", 5432, "active")
	// Domains on an unmerged template have their own Gateways: no collision.
	e.domain(t, tmplID, 80, 443)

	assert.NoError(t, e.svc.CheckPortCollision(streamB, "TCP", 5432, nil))
	assert.NoError(t, e.svc.CheckPortCollision(streamA, "TCP", 443, nil))
}

func TestCheckPortCollision_Merged_OtherStreamOnSameTemplate(t *testing.T) {
	e := newCollisionEnv(t)
	tmplID := e.template(t, true)
	streamA := e.stream(t, tmplID)
	streamB := e.stream(t, tmplID)
	e.l4Route(t, streamA, "udp", 5353, "active")

	assert.ErrorIs(t, e.svc.CheckPortCollision(streamB, "UDP", 5353, nil), services.ErrPortCollision)
	assert.NoError(t, e.svc.CheckPortCollision(streamB, "TCP", 5353, nil))

	// A different template (even merged) is a different set of Gateways.
	otherStream := e.stream(t, e.template(t, true))
	assert.NoError(t, e.svc.CheckPortCollision(otherStream, "UDP", 5353, nil))
}

func TestCheckPortCollision_ExcludeRouteID(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.template(t, false))
	routeID := e.l4Route(t, streamID, "tcp", 5432, "active")

	// Updating the route that owns the port must not collide with itself.
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 5432, &routeID))
	// A different route id does not get the exemption.
	other := uuid.New()
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 5432, &other), services.ErrPortCollision)
}

func TestCheckPortCollision_RejectedRouteDoesNotHoldPort(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.template(t, false))
	e.l4Route(t, streamID, "tcp", 5432, "rejected")
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 5432, nil))

	e.l4Route(t, streamID, "tcp", 6000, "pending_create")
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 6000, nil), services.ErrPortCollision)
}

func TestCheckPortCollision_UnknownStream(t *testing.T) {
	e := newCollisionEnv(t)
	assert.ErrorIs(t, e.svc.CheckPortCollision(uuid.New(), "TCP", 80, nil), services.ErrStreamNotFound)
}

func TestCheckPortCollision_InvalidTransport(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.template(t, false))
	assert.Error(t, e.svc.CheckPortCollision(streamID, "HTTP", 80, nil))
}

// templateWithDomain inserts a Gateway Template with explicit MergeGateways
// and EnableDomain (EnableStream is always on so the template stays valid).
func (e *collisionEnv) templateWithDomain(t *testing.T, merge, enableDomain bool, httpPort, httpsPort int) uuid.UUID {
	t.Helper()
	tmpl := &models.DomainTemplate{
		ProjectID: e.projectID, Name: "tmpl-" + uuid.NewString(), CreatedBy: e.userID,
		EnableDomain: enableDomain, EnableStream: true, MergeGateways: merge,
		HTTPPort: httpPort, HTTPSPort: httpsPort,
	}
	require.NoError(t, e.db.Create(tmpl).Error)
	return tmpl.ID
}

var defaultReserved = services.DefaultReservedPorts

func TestValidateListenerPort_RejectsEnvoyInternal(t *testing.T) {
	assert.ErrorIs(t, services.ValidateListenerPort(19000, defaultReserved), services.ErrReservedPort)
	assert.ErrorIs(t, services.ValidateListenerPort(19001, defaultReserved), services.ErrReservedPort)
}

func TestValidateListenerPort_RejectsPlaceholder(t *testing.T) {
	assert.ErrorIs(t, services.ValidateListenerPort(streamplan.PlaceholderPort, defaultReserved), services.ErrReservedPort)
}

func TestValidateListenerPort_RejectsOutOfRange(t *testing.T) {
	assert.ErrorIs(t, services.ValidateListenerPort(0, defaultReserved), services.ErrInvalidListenerPort)
	assert.ErrorIs(t, services.ValidateListenerPort(-1, defaultReserved), services.ErrInvalidListenerPort)
	assert.ErrorIs(t, services.ValidateListenerPort(70000, defaultReserved), services.ErrInvalidListenerPort)
}

func TestValidateListenerPort_RejectsDomainDefaults(t *testing.T) {
	reserved := services.ReservedPorts{DomainDefaults: []int{80, 443}}
	assert.ErrorIs(t, services.ValidateListenerPort(443, reserved), services.ErrReservedPort)
	assert.ErrorIs(t, services.ValidateListenerPort(80, reserved), services.ErrReservedPort)
}

func TestValidateListenerPort_AcceptsOrdinaryPorts(t *testing.T) {
	assert.NoError(t, services.ValidateListenerPort(5432, defaultReserved))
	assert.NoError(t, services.ValidateListenerPort(1, defaultReserved))
	assert.NoError(t, services.ValidateListenerPort(65535, defaultReserved))
	// 80/443 are free unless the caller reserves them (merged+domain-enabled).
	assert.NoError(t, services.ValidateListenerPort(443, defaultReserved))
}

func TestCheckPortCollision_MergedDomainEnabled_Reserves80And443(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.templateWithDomain(t, true, true, 80, 443))

	// No Domain exists yet, but the template's default ports are reserved.
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 443, nil), services.ErrReservedPort)
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 80, nil), services.ErrReservedPort)
	// UDP is a different transport; ordinary TCP ports are free.
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "UDP", 443, nil))
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 5432, nil))
}

func TestCheckPortCollision_MergedDomainEnabled_ReservesCustomTemplatePorts(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.templateWithDomain(t, true, true, 8080, 8443))

	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 8443, nil), services.ErrReservedPort)
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 8080, nil), services.ErrReservedPort)
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 443, nil))
}

func TestCheckPortCollision_MergedDomainDisabled_Allows443(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.templateWithDomain(t, true, false, 80, 443))
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 443, nil))
}

func TestCheckPortCollision_NotMerged_Allows443(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.templateWithDomain(t, false, true, 80, 443))
	assert.NoError(t, e.svc.CheckPortCollision(streamID, "TCP", 443, nil))
}

func TestCheckPortCollision_RejectsStaticReserved(t *testing.T) {
	e := newCollisionEnv(t)
	streamID := e.stream(t, e.template(t, false))
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 19001, nil), services.ErrReservedPort)
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "UDP", streamplan.PlaceholderPort, nil), services.ErrReservedPort)
	assert.ErrorIs(t, e.svc.CheckPortCollision(streamID, "TCP", 0, nil), services.ErrInvalidListenerPort)
}
