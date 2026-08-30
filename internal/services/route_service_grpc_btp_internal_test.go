package services

import (
	"strings"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// validateGRPCBackendTrafficPolicy is what stops a gRPC route from being
// created with a request-size cap Envoy Gateway cannot enforce. If it ever
// stops rejecting, the route is accepted, the BackendTrafficPolicy reports
// Accepted=True, and over-limit gRPC requests sail through with nothing
// anywhere reporting that the configured limit is inert -- which is the
// exact failure this guard exists to make impossible.
func TestValidateGRPCBackendTrafficPolicy(t *testing.T) {
	t.Run("nil policy is allowed", func(t *testing.T) {
		if err := validateGRPCBackendTrafficPolicy(nil); err != nil {
			t.Fatalf("nil BackendTrafficPolicy: unexpected error %v", err)
		}
	})

	t.Run("policy without requestBuffer is allowed", func(t *testing.T) {
		limit := int64(5)
		btp := &BackendTrafficPolicyInput{
			CircuitBreaker: &models.CircuitBreakerConfig{MaxParallelRequests: &limit},
		}
		if err := validateGRPCBackendTrafficPolicy(btp); err != nil {
			t.Fatalf("BackendTrafficPolicy without requestBuffer: unexpected error %v", err)
		}
	})

	t.Run("requestBuffer is rejected", func(t *testing.T) {
		btp := &BackendTrafficPolicyInput{
			RequestBuffer: &models.RequestBufferConfig{Limit: "1Ki"},
		}
		err := validateGRPCBackendTrafficPolicy(btp)
		if err == nil {
			t.Fatal("requestBuffer on a gRPC route was accepted, want rejection")
		}
		// The message is the only thing the user sees, and "not
		// supported" alone would read as an arbitrary product limit
		// rather than the silent-no-op hazard it actually is.
		if !strings.Contains(err.Error(), "requestBuffer") {
			t.Errorf("error should name the offending field, got: %v", err)
		}
		if !strings.Contains(err.Error(), "silently ignored") {
			t.Errorf("error should say the limit would be silently ignored, got: %v", err)
		}
	})
}
