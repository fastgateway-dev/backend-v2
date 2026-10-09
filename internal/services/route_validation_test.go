package services_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func oneK8sBackend() []models.RouteBackend {
	return []models.RouteBackend{{Type: models.BackendTypeKubernetes, Service: "pg", Namespace: "db", Port: 5432}}
}

func validL4Route(proto models.RouteProtocol) models.Route {
	return models.Route{Protocol: proto, Config: models.RouteConfig{ListenerPort: 5432, Backends: oneK8sBackend()}}
}

func TestValidateL4_AcceptsMinimalTCPAndUDP(t *testing.T) {
	assert.NoError(t, services.ValidateL4RouteConfig(validL4Route(models.RouteProtocolTCP)))
	assert.NoError(t, services.ValidateL4RouteConfig(validL4Route(models.RouteProtocolUDP)))
	r := validL4Route(models.RouteProtocolTCP)
	r.SecurityMode = models.SecurityModeGeneral
	r.Config.RouteType = models.RouteTypeBackend
	assert.NoError(t, services.ValidateL4RouteConfig(r))
}

func TestValidateL4_RejectsPathMatch(t *testing.T) {
	r := models.Route{Protocol: models.RouteProtocolTCP, Config: models.RouteConfig{ListenerPort: 5432,
		Matches: []models.RouteMatch{{Path: &models.PathMatch{Type: "Prefix", Value: "/"}}}}}
	assert.ErrorIs(t, services.ValidateL4RouteConfig(r), services.ErrL4RejectsL7Field)
}

func TestValidateL4_RejectsExternalBackend(t *testing.T) {
	r := models.Route{Protocol: models.RouteProtocolTCP, Config: models.RouteConfig{ListenerPort: 5432,
		Backends: []models.RouteBackend{{Type: "external", AddressType: "fqdn", Address: "db.example.com", Port: 5432}}}}
	assert.ErrorIs(t, services.ValidateL4RouteConfig(r), services.ErrL4ExternalBackend)
}

func TestValidateL4_RequiresListenerPortAndBackend(t *testing.T) {
	assert.ErrorIs(t, services.ValidateL4RouteConfig(models.Route{Protocol: models.RouteProtocolUDP}), services.ErrL4MissingListenerPort)

	noBackend := models.Route{Protocol: models.RouteProtocolTCP, Config: models.RouteConfig{ListenerPort: 5432}}
	assert.ErrorIs(t, services.ValidateL4RouteConfig(noBackend), services.ErrL4MissingBackend)
}

func TestValidateL4_RejectsSecurityMode(t *testing.T) {
	r := models.Route{Protocol: models.RouteProtocolTCP, SecurityMode: models.SecurityModeClient,
		Config: models.RouteConfig{ListenerPort: 5432, Backends: oneK8sBackend()}}
	assert.ErrorIs(t, services.ValidateL4RouteConfig(r), services.ErrL4RejectsL7Field)
}

func TestValidateL4_RejectsEveryL7ConfigField(t *testing.T) {
	cases := map[string]func(c *models.RouteConfig){
		"header match": func(c *models.RouteConfig) {
			c.Matches = []models.RouteMatch{{Headers: []models.HeaderMatch{{Name: "x"}}}}
		},
		"method match": func(c *models.RouteConfig) { c.Matches = []models.RouteMatch{{Method: "GET"}} },
		"query match": func(c *models.RouteConfig) {
			c.Matches = []models.RouteMatch{{QueryParams: []models.QueryParamMatch{{Name: "q"}}}}
		},
		"grpc match":              func(c *models.RouteConfig) { c.Matches = []models.RouteMatch{{GRPCService: &models.GRPCMethodMatch{}}} },
		"empty match element":     func(c *models.RouteConfig) { c.Matches = []models.RouteMatch{{}} },
		"redirect route type":     func(c *models.RouteConfig) { c.RouteType = models.RouteTypeRedirect },
		"direct response type":    func(c *models.RouteConfig) { c.RouteType = models.RouteTypeDirectResponse },
		"redirect":                func(c *models.RouteConfig) { c.Redirect = &models.RedirectConfig{} },
		"direct response":         func(c *models.RouteConfig) { c.DirectResponse = &models.DirectResponseConfig{} },
		"url rewrite":             func(c *models.RouteConfig) { c.URLRewrite = &models.URLRewrite{} },
		"request header modifier": func(c *models.RouteConfig) { c.RequestHeaderModifier = &models.HeaderModifier{} },
		"response header modifier": func(c *models.RouteConfig) {
			c.ResponseHeaderModifier = &models.HeaderModifier{}
		},
		"mirrors":                func(c *models.RouteConfig) { c.Mirrors = []models.MirrorBackend{{Service: "m", Port: 1}} },
		"default traffic policy": func(c *models.RouteConfig) { c.DefaultTrafficPolicy = models.DefaultTrafficPolicyDeny },
		"default allowed cidrs":  func(c *models.RouteConfig) { c.DefaultAllowedCIDRs = []string{"10.0.0.0/8"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := validL4Route(models.RouteProtocolTCP)
			mutate(&r.Config)
			err := services.ValidateL4RouteConfig(r)
			require.Error(t, err)
			assert.ErrorIs(t, err, services.ErrL4RejectsL7Field)
		})
	}
}
