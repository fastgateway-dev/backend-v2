package services

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/stretchr/testify/assert"
)

// TestTemplateFromCreateInput_CopiesListeners pins that the projection used by
// PreviewCreate carries the input's listener list verbatim, so the preview
// resolves the same listeners Create persists.
func TestTemplateFromCreateInput_CopiesListeners(t *testing.T) {
	input := &CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "LoadBalancer",
		Listeners: []models.TemplateListener{
			{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate},
			{Name: "tcpudp", Protocol: models.ListenerTCP, PortRangeMin: 9000, PortRangeMax: 9100},
		},
	}

	projected := templateFromCreateInput(
		input,
		kubernetes.EnvoyGatewayControllerName, "my-template-loadbalancer", "my-template-loadbalancer-config",
		models.ExposureTypeLoadBalancer, models.ExternalTrafficPolicy(""),
	)

	assert.Equal(t, models.Listeners(input.Listeners), projected.Listeners,
		"projection must carry the input's listeners verbatim")
	assert.Equal(t, []string{"https"}, hostnameListenerNames(projected),
		"only hostname-routed listeners are bindable by a domain")
}
