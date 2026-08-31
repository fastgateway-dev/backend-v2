package cluster_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/cluster"
	"github.com/stretchr/testify/assert"
)

func TestTLSSecretInfo_Struct(t *testing.T) {
	secret := cluster.TLSSecretInfo{
		Name:                 "my-cert",
		Namespace:            "fastgateway-system",
		ManagedByFastgateway: false,
		Labels:               map[string]string{"app": "test"},
		CreatedAt:            "2026-01-01T00:00:00Z",
	}
	assert.Equal(t, "my-cert", secret.Name)
	assert.Equal(t, "fastgateway-system", secret.Namespace)
	assert.False(t, secret.ManagedByFastgateway)
	assert.Equal(t, "2026-01-01T00:00:00Z", secret.CreatedAt)
}
