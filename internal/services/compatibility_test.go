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

// TestClassifyVersionPair_MinorMatch pins the policy that a pair is supported
// when it matches a tested pair on major.minor for BOTH versions — patch
// releases within a tested minor line (e.g. EG 1.9.2 on the tested 1.9.1 line)
// are treated as supported, and a leading "v" is tolerated.
func TestClassifyVersionPair_MinorMatch(t *testing.T) {
	cases := []struct{ eg, gw string }{
		{"1.9.2", "1.6.1"},   // EG one patch ahead of the tested 1.9.1 line
		{"1.9.5", "1.6.9"},   // both patches ahead on the tested 1.9/1.6 line
		{"1.7.0", "1.4.0"},   // GW patch below the tested 1.4.1 — same minor line
		{"v1.9.2", "v1.6.1"}, // leading "v" tolerated
	}
	for _, c := range cases {
		t.Run(c.eg+"_"+c.gw, func(t *testing.T) {
			assert.Equal(t, services.VersionStatusSupported, services.ClassifyVersionPair(c.eg, c.gw))
		})
	}
}

func TestClassifyVersionPair_Untested(t *testing.T) {
	cases := []struct{ eg, gw string }{
		{"1.8.0", "1.4.1"}, // EG 1.8 line is only tested against GW 1.5, not 1.4
		{"1.7.0", "1.5.1"}, // EG 1.7 line is only tested against GW 1.4, not 1.5
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
