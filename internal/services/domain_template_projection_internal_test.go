package services

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/stretchr/testify/assert"
)

// TestTemplateFromCreateInput_CarriesDefaultedPorts_And_TLSPolicy is a
// regression test for Phase 2H Task 1 fix round 1: templateFromCreateInput
// used to read HTTPPort, HTTPSPort and TLSPolicy straight off the raw input,
// silently dropping the defaults PreviewCreate (and Create) compute just
// above the call site -- an unset HTTPPort/HTTPSPort/TLSPolicy stays 0/0/""
// on the projection instead of becoming 80/443/"terminate", the values
// Create actually persists for the same input.
//
// Nothing reads these three fields off the projection today (neither
// templateplan.BuildGatewayClassConfig nor BuildEnvoyProxyConfig touches
// HTTPPort, HTTPSPort or TLSPolicy), so the defect was inert -- but the
// helper's own contract ("project the input into the models.DomainTemplate
// shape the builders consume") is violated regardless of who reads the
// result today, and Task 2 is expected to add a caller that does read them.
func TestTemplateFromCreateInput_CarriesDefaultedPorts_And_TLSPolicy(t *testing.T) {
	input := &CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		TLSMode:      "tls_only",
		// Deliberately unset -- the caller is expected to default these
		// before projecting, exactly as Create and PreviewCreate do.
		HTTPPort:  0,
		HTTPSPort: 0,
		TLSPolicy: "",
	}

	// The defaulted values a caller (PreviewCreate/Create) computes and must
	// pass in -- NOT input.HTTPPort/HTTPSPort/TLSPolicy directly.
	const defaultedHTTPPort = 80
	const defaultedHTTPSPort = 443
	defaultedTLSPolicy := models.TLSPolicyTerminate

	projected := templateFromCreateInput(
		input,
		kubernetes.EnvoyGatewayControllerName, "my-template-loadbalancer", "my-template-loadbalancer-config",
		models.ExposureTypeLoadBalancer, models.ExternalTrafficPolicy(""),
		defaultedHTTPPort, defaultedHTTPSPort,
		defaultedTLSPolicy,
	)

	assert.Equal(t, defaultedHTTPPort, projected.HTTPPort,
		"projection must carry the caller's defaulted HTTPPort, not the raw (possibly zero) input value")
	assert.Equal(t, defaultedHTTPSPort, projected.HTTPSPort,
		"projection must carry the caller's defaulted HTTPSPort, not the raw (possibly zero) input value")
	assert.Equal(t, defaultedTLSPolicy, projected.TLSPolicy,
		"projection must carry the caller's defaulted TLSPolicy, not the raw (possibly empty) input value")
}
