package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
)

// ErrTopologyNotFound is returned by topology service methods when the
// requested resource does not exist OR when the requested resource exists
// but does not belong to the project (the two cases are deliberately
// indistinguishable to callers to prevent project-membership info leaks).
var ErrTopologyNotFound = errors.New("topology resource not found")

// appendUniqueUUID appends v to in only if not already present.
func appendUniqueUUID(in []uuid.UUID, v uuid.UUID) []uuid.UUID {
	for _, x := range in {
		if x == v {
			return in
		}
	}
	return append(in, v)
}

// SecurityFeatureFlags mirrors the frontend type SecurityFeatureFlags exactly.
type SecurityFeatureFlags struct {
	IPAllowlist bool `json:"ipAllowlist"`
	MTLS        bool `json:"mtls"`
	APIKey      bool `json:"apiKey"`
	JWT         bool `json:"jwt"`
	BasicAuth   bool `json:"basicAuth"`
	HeaderAuth  bool `json:"headerAuth"`
	RateLimit   bool `json:"rateLimit"`
	ExtAuth     bool `json:"extAuth"`
	OIDC        bool `json:"oidc"`
	WAF         bool `json:"waf"`
}

// --- Project topology DTOs -----------------------------------------------

type ProjectTopologyResponse struct {
	Domains []ProjectTopologyDomain `json:"domains"`
	Clients []ProjectTopologyClient `json:"clients"`
	IPs     []TopologyIPRow         `json:"ips"`
}

type ProjectTopologyDomain struct {
	ID            uuid.UUID                  `json:"id"`
	Name          string                     `json:"name"`
	Hostname      string                     `json:"hostname"`
	SecurityMode  string                     `json:"securityMode"`
	TemplateName  *string                    `json:"templateName"`
	GatewayStatus TopologyStatus             `json:"gatewayStatus"`
	Counts        ProjectTopologyDomainCount `json:"counts"`
}

type ProjectTopologyDomainCount struct {
	Routes                int `json:"routes"`
	ClientsAttached       int `json:"clientsAttached"`
	RoutesWithIPAllowlist int `json:"routesWithIpAllowlist"`
	RoutesWithMTLS        int `json:"routesWithMtls"`
}

type ProjectTopologyClient struct {
	ID           uuid.UUID                                 `json:"id"`
	Name         string                                    `json:"name"`
	TeamID       uuid.UUID                                 `json:"teamId"`
	TeamName     string                                    `json:"teamName"`
	Capabilities ClientCapabilities                        `json:"capabilities"`
	PerDomain    map[string]ProjectTopologyClientPerDomain `json:"perDomain"`
}

type ClientCapabilities struct {
	APIKey          bool `json:"apiKey"`
	JWT             bool `json:"jwt"`
	MTLS            bool `json:"mtls"`
	IPAllowlistSize int  `json:"ipAllowlistSize"`
}

type ProjectTopologyClientPerDomain struct {
	RouteCount      int            `json:"routeCount"`
	AggregateStatus TopologyStatus `json:"aggregateStatus"`
}

type TopologyIPRow struct {
	CIDR      string              `json:"cidr"`
	Source    string              `json:"source"`
	SourceRef TopologyIPSourceRef `json:"sourceRef"`
	Reach     TopologyIPReach     `json:"reach"`
	UpdatedAt string              `json:"updatedAt"`
}

type TopologyIPSourceRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type TopologyIPReach struct {
	RouteIDs  []uuid.UUID `json:"routeIds"`
	DomainIDs []uuid.UUID `json:"domainIds"`
}

// --- Domain topology DTOs ------------------------------------------------

type DomainTopologyResponse struct {
	Domain      DomainTopologyDomain       `json:"domain"`
	Gateway     DomainTopologyGateway      `json:"gateway"`
	Routes      []DomainTopologyRoute      `json:"routes"`
	Backends    []DomainTopologyBackend    `json:"backends"`
	Clients     []DomainTopologyClient     `json:"clients"`
	Attachments []DomainTopologyAttachment `json:"attachments"`
}

type DomainTopologyDomain struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Hostname     string    `json:"hostname"`
	SecurityMode string    `json:"securityMode"`
	TemplateName *string   `json:"templateName"`
}

type DomainTopologyGateway struct {
	Status           TopologyStatus            `json:"status"`
	ListenerPort     int                       `json:"listenerPort"`
	ListenerProtocol string                    `json:"listenerProtocol"`
	TLS              *DomainTopologyGatewayTLS `json:"tls"`
	GatewayClass     string                    `json:"gatewayClass"`
}

type DomainTopologyGatewayTLS struct {
	SecretName      string `json:"secretName"`
	SecretNamespace string `json:"secretNamespace"`
}

type DomainTopologyRoute struct {
	ID                 uuid.UUID                   `json:"id"`
	Name               string                      `json:"name"`
	Protocol           string                      `json:"protocol"`
	MatcherSummary     string                      `json:"matcherSummary"`
	Method             *string                     `json:"method"`
	Status             TopologyStatus              `json:"status"`
	RouteLevelSecurity SecurityFeatureFlags        `json:"routeLevelSecurity"`
	BackendIDs         []string                    `json:"backendIds"`
	BackendRoles       []DomainTopologyBackendRole `json:"backendRoles"`
}

type DomainTopologyBackendRole struct {
	BackendID string `json:"backendId"`
	Role      string `json:"role"`
	Weight    *int   `json:"weight"`
}

type DomainTopologyBackend struct {
	ID          string  `json:"id"`
	Type        string  `json:"type"`
	Service     *string `json:"service,omitempty"`
	Namespace   *string `json:"namespace,omitempty"`
	Address     *string `json:"address,omitempty"`
	AddressType *string `json:"addressType,omitempty"`
	Port        int     `json:"port"`
	HitCount    int     `json:"hitCount"`
}

type DomainTopologyClient struct {
	ID           uuid.UUID          `json:"id"`
	Name         string             `json:"name"`
	TeamID       uuid.UUID          `json:"teamId"`
	TeamName     string             `json:"teamName"`
	Capabilities ClientCapabilities `json:"capabilities"`
}

type DomainTopologyAttachment struct {
	ID           uuid.UUID            `json:"id"`
	ClientID     uuid.UUID            `json:"clientId"`
	RouteID      uuid.UUID            `json:"routeId"`
	Status       TopologyStatus       `json:"status"`
	Enforced     SecurityFeatureFlags `json:"enforced"`
	HasRateLimit bool                 `json:"hasRateLimit"`
	HasExtAuth   bool                 `json:"hasExtAuth"`
}

// --- TopologyService ------------------------------------------------------

// TopologyService aggregates topology views for a project and per-domain.
type TopologyService struct {
	domainRepo         repository.DomainRepositoryInterface
	routeRepo          repository.RouteRepositoryInterface
	attachmentRepo     repository.ClientAttachmentRepositoryInterface
	clientRepo         repository.ClientRepositoryInterface
	clientIPRepo       repository.ClientIPRepositoryInterface
	securityPolicyRepo repository.SecurityPolicyRepositoryInterface
	wafPolicyRepo      repository.WafPolicyRepositoryInterface
	btpRepo            repository.BackendTrafficPolicyRepositoryInterface
	teamRepo           repository.TeamRepositoryInterface
	domainTemplateRepo repository.DomainTemplateRepositoryInterface
}

// NewTopologyService constructs a TopologyService with the repositories it
// needs to assemble both project- and domain-level topology views.
func NewTopologyService(
	domainRepo repository.DomainRepositoryInterface,
	routeRepo repository.RouteRepositoryInterface,
	attachmentRepo repository.ClientAttachmentRepositoryInterface,
	clientRepo repository.ClientRepositoryInterface,
	clientIPRepo repository.ClientIPRepositoryInterface,
	securityPolicyRepo repository.SecurityPolicyRepositoryInterface,
	wafPolicyRepo repository.WafPolicyRepositoryInterface,
	btpRepo repository.BackendTrafficPolicyRepositoryInterface,
	teamRepo repository.TeamRepositoryInterface,
	domainTemplateRepo repository.DomainTemplateRepositoryInterface,
) *TopologyService {
	return &TopologyService{
		domainRepo:         domainRepo,
		routeRepo:          routeRepo,
		attachmentRepo:     attachmentRepo,
		clientRepo:         clientRepo,
		clientIPRepo:       clientIPRepo,
		securityPolicyRepo: securityPolicyRepo,
		wafPolicyRepo:      wafPolicyRepo,
		btpRepo:            btpRepo,
		teamRepo:           teamRepo,
		domainTemplateRepo: domainTemplateRepo,
	}
}

// backendID returns a stable identifier for deduplication of route backends.
func backendID(b models.RouteBackend) string {
	switch b.Type {
	case models.BackendTypeKubernetes:
		return fmt.Sprintf("k8s|%s|%s|%d", b.Namespace, b.Service, b.Port)
	case models.BackendTypeExternal:
		return fmt.Sprintf("ext|%s|%s|%d", b.AddressType, b.Address, b.Port)
	default:
		return fmt.Sprintf("unknown|%s|%s|%s|%d", b.Type, b.Namespace, b.Service, b.Port)
	}
}

// mirrorBackendID returns a stable identifier for a mirror backend.
func mirrorBackendID(m models.MirrorBackend) string {
	// MirrorBackend is kubernetes-only today.
	return fmt.Sprintf("k8s|%s|%s|%d", m.Namespace, m.Service, m.Port)
}

// summarizeMatcher returns a single human-readable string describing the
// route's match (path / gRPC service+method / fallback to "/" wildcard).
func summarizeMatcher(r models.Route) string {
	if len(r.Config.Matches) == 0 {
		return "/"
	}
	m := r.Config.Matches[0]
	if r.Protocol == models.RouteProtocolGRPC {
		svc := ""
		mth := ""
		if m.GRPCService != nil {
			svc = m.GRPCService.Value
		}
		if m.GRPCMethod != nil {
			mth = m.GRPCMethod.Value
		}
		switch {
		case svc != "" && mth != "":
			return svc + "/" + mth
		case svc != "":
			return svc
		case mth != "":
			return "*/" + mth
		default:
			return "*"
		}
	}
	if m.Path != nil && m.Path.Value != "" {
		return m.Path.Value
	}
	return "/"
}

// httpMethod returns a non-nil pointer to the HTTP method string when the
// first match constrains it, else nil.
func httpMethod(r models.Route) *string {
	if r.Protocol == models.RouteProtocolGRPC {
		return nil
	}
	if len(r.Config.Matches) == 0 {
		return nil
	}
	if r.Config.Matches[0].Method == "" {
		return nil
	}
	v := r.Config.Matches[0].Method
	return &v
}

// newBackendDTO builds a topology backend node from a route's primary backend.
func newBackendDTO(id string, b models.RouteBackend) DomainTopologyBackend {
	dto := DomainTopologyBackend{
		ID:       id,
		Type:     string(b.Type),
		Port:     b.Port,
		HitCount: 1,
	}
	switch b.Type {
	case models.BackendTypeKubernetes:
		svc := b.Service
		ns := b.Namespace
		dto.Service = &svc
		dto.Namespace = &ns
	case models.BackendTypeExternal:
		addr := b.Address
		at := string(b.AddressType)
		dto.Address = &addr
		dto.AddressType = &at
	}
	return dto
}

// newMirrorBackendDTO builds a topology backend node from a mirror destination.
func newMirrorBackendDTO(id string, m models.MirrorBackend) DomainTopologyBackend {
	svc := m.Service
	ns := m.Namespace
	return DomainTopologyBackend{
		ID:        id,
		Type:      string(m.Type),
		Service:   &svc,
		Namespace: &ns,
		Port:      m.Port,
		HitCount:  1,
	}
}

// routeLevelSecurity computes per-route security feature flags from the route's
// own SecurityPolicy / BackendTrafficPolicy / WafPolicy. Client-mode attachments
// are not folded in here — that's the per-attachment Enforced surface.
func (s *TopologyService) routeLevelSecurity(routeID uuid.UUID) SecurityFeatureFlags {
	flags := SecurityFeatureFlags{}

	if sp, err := s.securityPolicyRepo.GetByRouteID(routeID); err == nil && sp != nil {
		flags.APIKey = sp.Config.APIKeyAuth != nil
		flags.JWT = sp.Config.JWT != nil
		flags.BasicAuth = sp.Config.BasicAuth != nil
		flags.OIDC = sp.Config.OIDC != nil
		flags.ExtAuth = sp.Config.ExtAuth != nil
		if sp.Config.Authorization != nil {
			for _, rule := range sp.Config.Authorization.Rules {
				if len(rule.Principal.ClientCIDRs) > 0 {
					flags.IPAllowlist = true
				}
				if len(rule.Principal.Headers) > 0 {
					flags.HeaderAuth = true
				}
			}
		}
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		// Non-not-found errors are swallowed: topology is best-effort, and
		// we already have the route — partial flags are better than failing
		// the whole response.
	}

	if btp, err := s.btpRepo.GetByRouteID(routeID); err == nil && btp != nil {
		flags.RateLimit = btp.Config.RateLimit != nil
	}

	if wp, err := s.wafPolicyRepo.GetByRouteID(routeID); err == nil && wp != nil {
		flags.WAF = true
	}

	return flags
}

// GetDomainTopology returns the per-domain topology view (general mode only).
// Client mode (clients + attachments) will be added by a later task; for now
// Clients and Attachments are always returned as empty, non-nil slices.
func (s *TopologyService) GetDomainTopology(ctx context.Context, projectID, domainID uuid.UUID) (*DomainTopologyResponse, error) {
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}
	if domain == nil {
		return nil, fmt.Errorf("get domain: %w", ErrTopologyNotFound)
	}
	if domain.ProjectID != projectID {
		// Deliberately use the same error so callers can't distinguish
		// "wrong project" from "missing domain" — prevents info leak.
		return nil, fmt.Errorf("get domain: %w", ErrTopologyNotFound)
	}

	// Resolve template name (nullable).
	var templateName *string
	if domain.DomainTemplateID != nil {
		if dt, terr := s.domainTemplateRepo.GetByID(*domain.DomainTemplateID); terr == nil && dt != nil {
			n := dt.Name
			templateName = &n
		}
	}

	// List routes for this domain. We use a generous page/limit and no
	// status filter so the topology surface includes pending/draft routes
	// alongside active ones.
	routes, _, err := s.routeRepo.ListByDomainID(domainID, 1, 10000, nil, "", "", "", nil)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}

	resp := &DomainTopologyResponse{
		Domain: DomainTopologyDomain{
			ID:           domain.ID,
			Name:         domain.Name,
			Hostname:     domain.Hostname,
			TemplateName: templateName,
		},
		Gateway: DomainTopologyGateway{
			Status:           MapGatewayStatus(domain.Status == models.DomainStatusActive, domain.StatusMessage),
			ListenerPort:     domain.HTTPSPort,
			ListenerProtocol: "HTTPS",
			GatewayClass:     domain.K8sGatewayClass,
		},
		Routes:      []DomainTopologyRoute{},
		Backends:    []DomainTopologyBackend{},
		Clients:     []DomainTopologyClient{},
		Attachments: []DomainTopologyAttachment{},
	}

	if domain.TLSSecretName != "" {
		resp.Gateway.TLS = &DomainTopologyGatewayTLS{
			SecretName:      domain.TLSSecretName,
			SecretNamespace: domain.TLSSecretNamespace,
		}
	}

	// Infer domain security mode from routes: any client-mode route flips
	// the whole domain to "client", else "general".
	securityMode := "general"
	for _, r := range routes {
		if r.SecurityMode == models.SecurityModeClient {
			securityMode = "client"
			break
		}
	}
	resp.Domain.SecurityMode = securityMode

	// Build routes + dedup backend nodes.
	backendIndex := make(map[string]int) // id -> index into resp.Backends
	for i := range routes {
		r := routes[i]

		method := httpMethod(r)
		dtoRoute := DomainTopologyRoute{
			ID:                 r.ID,
			Name:               r.Name,
			Protocol:           string(r.Protocol),
			MatcherSummary:     summarizeMatcher(r),
			Method:             method,
			Status:             MapRouteStatus(r.Status),
			RouteLevelSecurity: s.routeLevelSecurity(r.ID),
			BackendIDs:         []string{},
			BackendRoles:       []DomainTopologyBackendRole{},
		}

		// Primary backends.
		for _, b := range r.Config.Backends {
			id := backendID(b)
			if idx, ok := backendIndex[id]; ok {
				resp.Backends[idx].HitCount++
			} else {
				backendIndex[id] = len(resp.Backends)
				resp.Backends = append(resp.Backends, newBackendDTO(id, b))
			}
			dtoRoute.BackendIDs = append(dtoRoute.BackendIDs, id)

			role := "primary"
			if b.Fallback {
				role = "fallback"
			}
			var weight *int
			if b.Weight != 0 {
				w := b.Weight
				weight = &w
			}
			dtoRoute.BackendRoles = append(dtoRoute.BackendRoles, DomainTopologyBackendRole{
				BackendID: id,
				Role:      role,
				Weight:    weight,
			})
		}

		// Mirror backends.
		for _, m := range r.Config.Mirrors {
			id := mirrorBackendID(m)
			if idx, ok := backendIndex[id]; ok {
				resp.Backends[idx].HitCount++
			} else {
				backendIndex[id] = len(resp.Backends)
				resp.Backends = append(resp.Backends, newMirrorBackendDTO(id, m))
			}
			dtoRoute.BackendIDs = append(dtoRoute.BackendIDs, id)
			dtoRoute.BackendRoles = append(dtoRoute.BackendRoles, DomainTopologyBackendRole{
				BackendID: id,
				Role:      "mirror",
				Weight:    nil,
			})
		}

		resp.Routes = append(resp.Routes, dtoRoute)
	}

	// Client mode: populate Clients + Attachments.
	if securityMode == "client" {
		clientIDSet := map[uuid.UUID]struct{}{}
		for i := range routes {
			atts, aerr := s.attachmentRepo.ListByRouteID(routes[i].ID)
			if aerr != nil {
				return nil, fmt.Errorf("list attachments by route: %w", aerr)
			}
			for j := range atts {
				a := &atts[j]
				clientIDSet[a.ClientID] = struct{}{}
				resp.Attachments = append(resp.Attachments, DomainTopologyAttachment{
					ID:       a.ID,
					ClientID: a.ClientID,
					RouteID:  a.RouteID,
					Status:   MapAttachmentStatus(a.Status),
					Enforced: SecurityFeatureFlags{
						IPAllowlist: a.EnableIPAllowlist,
						MTLS:        a.EnableMTLS,
						APIKey:      a.EnableAPIKey,
						JWT:         a.EnableJWT,
						BasicAuth:   a.EnableBasicAuth,
						HeaderAuth:  a.EnableHeaderAuth,
						RateLimit:   a.RateLimitConfig != nil,
						ExtAuth:     a.ExtAuth != nil,
					},
					HasRateLimit: a.RateLimitConfig != nil,
					HasExtAuth:   a.ExtAuth != nil,
				})
			}
		}
		for cid := range clientIDSet {
			c, cerr := s.clientRepo.GetByID(cid)
			if cerr != nil || c == nil {
				continue
			}
			ipCount, _ := s.clientIPRepo.CountByClientID(cid)
			teamName := ""
			if tm, terr := s.teamRepo.GetByID(c.TeamID); terr == nil && tm != nil {
				teamName = tm.Name
			}
			resp.Clients = append(resp.Clients, DomainTopologyClient{
				ID:       c.ID,
				Name:     c.Name,
				TeamID:   c.TeamID,
				TeamName: teamName,
				Capabilities: ClientCapabilities{
					APIKey:          c.APIKeyEnabled,
					JWT:             c.JWTEnabled,
					MTLS:            c.MTLSEnabled,
					IPAllowlistSize: int(ipCount),
				},
			})
		}
	}

	_ = ctx // ctx is reserved for repo methods that accept it in the future.

	return resp, nil
}

// GetProjectTopology aggregates project-wide topology: per-domain summary cards
// and per-client per-domain rollups (route counts + aggregate statuses). IP
// rows are deferred to a later task and returned as an empty slice.
//
// Implementation note: this calls GetDomainTopology once per domain (N+1 by
// design). Acceptable for v1 since topology is read-only and the inner method
// already covers all per-domain joins.
func (s *TopologyService) GetProjectTopology(ctx context.Context, projectID uuid.UUID) (*ProjectTopologyResponse, error) {
	domains, _, err := s.domainRepo.ListByProjectID(projectID, 1, 10000, "", "", nil)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}

	resp := &ProjectTopologyResponse{
		Domains: []ProjectTopologyDomain{},
		Clients: []ProjectTopologyClient{},
		IPs:     []TopologyIPRow{},
	}

	clientToPerDomain := map[uuid.UUID]map[string]ProjectTopologyClientPerDomain{}
	clientStatuses := map[uuid.UUID]map[string][]TopologyStatus{}
	clientSet := map[uuid.UUID]struct{}{}

	for i := range domains {
		d := &domains[i]
		dt, err := s.GetDomainTopology(ctx, projectID, d.ID)
		if err != nil {
			return nil, err
		}

		ipCount := 0
		mtlsRoutes := 0
		for _, r := range dt.Routes {
			if r.RouteLevelSecurity.IPAllowlist {
				ipCount++
			}
			if r.RouteLevelSecurity.MTLS {
				mtlsRoutes++
			}
		}
		for _, a := range dt.Attachments {
			if a.Enforced.IPAllowlist {
				ipCount++
			}
			if a.Enforced.MTLS {
				mtlsRoutes++
			}
		}

		var tn *string
		if d.DomainTemplateID != nil {
			if t, terr := s.domainTemplateRepo.GetByID(*d.DomainTemplateID); terr == nil && t != nil {
				name := t.Name
				tn = &name
			}
		}

		resp.Domains = append(resp.Domains, ProjectTopologyDomain{
			ID:            d.ID,
			Name:          d.Name,
			Hostname:      d.Hostname,
			SecurityMode:  dt.Domain.SecurityMode,
			TemplateName:  tn,
			GatewayStatus: dt.Gateway.Status,
			Counts: ProjectTopologyDomainCount{
				Routes:                len(dt.Routes),
				ClientsAttached:       len(dt.Clients),
				RoutesWithIPAllowlist: ipCount,
				RoutesWithMTLS:        mtlsRoutes,
			},
		})

		// per-client per-domain rollup
		clientRouteCount := map[uuid.UUID]map[uuid.UUID]struct{}{}
		domainKey := d.ID.String()
		for _, a := range dt.Attachments {
			if _, ok := clientRouteCount[a.ClientID]; !ok {
				clientRouteCount[a.ClientID] = map[uuid.UUID]struct{}{}
			}
			clientRouteCount[a.ClientID][a.RouteID] = struct{}{}
			if clientStatuses[a.ClientID] == nil {
				clientStatuses[a.ClientID] = map[string][]TopologyStatus{}
			}
			clientStatuses[a.ClientID][domainKey] = append(clientStatuses[a.ClientID][domainKey], a.Status)
			clientSet[a.ClientID] = struct{}{}
		}
		for cid, routes := range clientRouteCount {
			if _, ok := clientToPerDomain[cid]; !ok {
				clientToPerDomain[cid] = map[string]ProjectTopologyClientPerDomain{}
			}
			clientToPerDomain[cid][domainKey] = ProjectTopologyClientPerDomain{
				RouteCount:      len(routes),
				AggregateStatus: AggregateStatus(clientStatuses[cid][domainKey]),
			}
		}
	}

	for cid := range clientSet {
		c, cerr := s.clientRepo.GetByID(cid)
		if cerr != nil || c == nil {
			continue
		}
		ipCount, _ := s.clientIPRepo.CountByClientID(cid)
		teamName := ""
		if tm, terr := s.teamRepo.GetByID(c.TeamID); terr == nil && tm != nil {
			teamName = tm.Name
		}
		resp.Clients = append(resp.Clients, ProjectTopologyClient{
			ID:       c.ID,
			Name:     c.Name,
			TeamID:   c.TeamID,
			TeamName: teamName,
			Capabilities: ClientCapabilities{
				APIKey:          c.APIKeyEnabled,
				JWT:             c.JWTEnabled,
				MTLS:            c.MTLSEnabled,
				IPAllowlistSize: int(ipCount),
			},
			PerDomain: clientToPerDomain[cid],
		})
	}

	// IP rows: one set sourced from route-level SecurityPolicy authorization
	// rules, another from client-level attachments with EnableIPAllowlist set
	// -- tracked separately below because they key by different IDs (route
	// vs. client) before both feed into resp.IPs.
	type clientReach struct {
		clientID  uuid.UUID
		routeIDs  []uuid.UUID
		domainIDs []uuid.UUID
	}
	clientReachMap := map[uuid.UUID]*clientReach{}

	for i := range domains {
		d := &domains[i]
		// IP audit must surface failures rather than silently empty out —
		// this loop is the entire point of the audit feature, so swallowing
		// repo errors here would defeat the purpose.
		routes, _, rerr := s.routeRepo.ListByDomainID(d.ID, 1, 10000, nil, "", "", "", nil)
		if rerr != nil {
			return nil, fmt.Errorf("list routes for domain %s: %w", d.ID, rerr)
		}
		for ri := range routes {
			r := &routes[ri]
			// Route-source IPs from SecurityPolicy authorization
			sp, sperr := s.securityPolicyRepo.GetByRouteID(r.ID)
			if sperr != nil && !errors.Is(sperr, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("security policy for route %s: %w", r.ID, sperr)
			}
			if sp != nil && sp.Config.Authorization != nil {
				for _, rule := range sp.Config.Authorization.Rules {
					for _, raw := range rule.Principal.ClientCIDRs {
						norm, nerr := NormalizeTopologyCIDR(raw)
						if nerr != nil {
							continue
						}
						resp.IPs = append(resp.IPs, TopologyIPRow{
							CIDR:      norm,
							Source:    "route",
							SourceRef: TopologyIPSourceRef{ID: r.ID, Name: r.Name},
							Reach:     TopologyIPReach{RouteIDs: []uuid.UUID{r.ID}, DomainIDs: []uuid.UUID{d.ID}},
							UpdatedAt: sp.UpdatedAt.Format(time.RFC3339),
						})
					}
				}
			}
			// Track client reach for client-source IPs (only when enableIPAllowlist=true)
			atts, aerr := s.attachmentRepo.ListByRouteID(r.ID)
			if aerr != nil && !errors.Is(aerr, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("list attachments for route %s: %w", r.ID, aerr)
			}
			for _, a := range atts {
				if !a.EnableIPAllowlist {
					continue
				}
				if _, ok := clientReachMap[a.ClientID]; !ok {
					clientReachMap[a.ClientID] = &clientReach{clientID: a.ClientID}
				}
				clientReachMap[a.ClientID].routeIDs = append(clientReachMap[a.ClientID].routeIDs, r.ID)
				clientReachMap[a.ClientID].domainIDs = appendUniqueUUID(clientReachMap[a.ClientID].domainIDs, d.ID)
			}
		}
	}
	// Emit client-source IP rows
	for cid, cr := range clientReachMap {
		c, cerr := s.clientRepo.GetByID(cid)
		if cerr != nil || c == nil {
			continue
		}
		ips, iperr := s.clientIPRepo.ListByClientID(cid)
		if iperr != nil && !errors.Is(iperr, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("list client IPs for client %s: %w", cid, iperr)
		}
		for _, row := range ips {
			norm, nerr := NormalizeTopologyCIDR(row.CIDR)
			if nerr != nil {
				continue
			}
			resp.IPs = append(resp.IPs, TopologyIPRow{
				CIDR:      norm,
				Source:    "client",
				SourceRef: TopologyIPSourceRef{ID: c.ID, Name: c.Name},
				Reach:     TopologyIPReach{RouteIDs: cr.routeIDs, DomainIDs: cr.domainIDs},
				UpdatedAt: row.CreatedAt.Format(time.RFC3339),
			})
		}
	}

	return resp, nil
}
