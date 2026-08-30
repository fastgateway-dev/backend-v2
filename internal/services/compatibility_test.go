package services_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/stretchr/testify/assert"
)

func TestClassifyVersionPair_Supported(t *testing.T) {
	for _, p := range services.SupportedVersionPairs {
		t.Run(p.EnvoyGateway+"_"+p.GatewayAPI, func(t *testing.T) {
			got := services.ClassifyVersionPair(p.EnvoyGateway, p.GatewayAPI)
			assert.Equal(t, services.VersionStatusSupported, got)
		})
	}
}

func TestClassifyVersionPair_Untested(t *testing.T) {
	cases := []struct{ eg, gw string }{
		{"1.8.0", "1.4.1"},
		{"1.7.0", "1.5.1"},
		{"1.7.0", "1.4.0"},
		{"99.99.99", "99.99.99"},
	}
	for _, c := range cases {
		t.Run(c.eg+"_"+c.gw, func(t *testing.T) {
			assert.Equal(t, services.VersionStatusUntested, services.ClassifyVersionPair(c.eg, c.gw))
		})
	}
}

func TestClassifyVersionPair_Unknown(t *testing.T) {
	assert.Equal(t, services.VersionStatusUnknown, services.ClassifyVersionPair("", "1.4.1"))
	assert.Equal(t, services.VersionStatusUnknown, services.ClassifyVersionPair("1.7.0", ""))
	assert.Equal(t, services.VersionStatusUnknown, services.ClassifyVersionPair("", ""))
}

func TestSupportedVersionPairs_NoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range services.SupportedVersionPairs {
		key := p.EnvoyGateway + "|" + p.GatewayAPI
		assert.False(t, seen[key], "duplicate pair: %s", key)
		seen[key] = true
	}
}
