package services_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/services"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeCIDR(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"10.0.0.0/24", "10.0.0.0/24", false},
		{"1.2.3.4", "1.2.3.4/32", false},
		{"::1", "::1/128", false},
		{"2001:db8::/32", "2001:db8::/32", false},
		{"  192.168.0.1  ", "192.168.0.1/32", false},
		{"not-an-ip", "", true},
		{"", "", true},
		{"10.0.0.0/99", "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := services.NormalizeTopologyCIDR(c.in)
			if c.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestTopologyIPDedupKey(t *testing.T) {
	a := services.TopologyIPDedupKey("route", "rid-1", "10.0.0.0/24")
	b := services.TopologyIPDedupKey("route", "rid-1", "10.0.0.0/24")
	c := services.TopologyIPDedupKey("client", "rid-1", "10.0.0.0/24")
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, c)
}
