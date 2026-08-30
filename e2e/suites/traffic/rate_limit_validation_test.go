//go:build e2e

package traffic

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// rateLimitValidationConfig builds a route config identical in shape to
// rate_limiting/test_validation.py's _make_config helper: a single-backend
// route at a unique path, with rate-limit rules supplied by the caller so
// each test can exercise one specific (models.RateLimitConfig).Validate
// rule (internal/models/backend_traffic_policy.go).
func rateLimitValidationConfig(t *testing.T, rules []models.RateLimitRule) services.CreateRouteInput {
	t.Helper()
	_, _, cfg := backendRouteConfig(t)
	cfg.BackendTrafficPolicy = &services.BackendTrafficPolicyInput{
		RateLimit: &models.RateLimitConfig{Global: &models.GlobalRateLimitConfig{Rules: rules}},
	}
	return cfg
}

// TestRateLimitValidationInvalidUnit ports
// rate_limiting/test_validation.py:test_invalid_unit: a rate-limit rule
// with an unrecognized unit ("Weekly", not one of Second/Minute/Hour/Day)
// must be rejected at route creation.
func TestRateLimitValidationInvalidUnit(t *testing.T) {
	t.Parallel()
	cfg := rateLimitValidationConfig(t, []models.RateLimitRule{
		{Limit: models.RateLimitValue{Requests: 10, Unit: "Weekly"}},
	})
	expectCreateRejected(t, cfg)
}

// TestRateLimitValidationZeroRequests ports
// rate_limiting/test_validation.py:test_zero_requests: a rate-limit rule
// with requests=0 must be rejected at route creation.
func TestRateLimitValidationZeroRequests(t *testing.T) {
	t.Parallel()
	cfg := rateLimitValidationConfig(t, []models.RateLimitRule{
		{Limit: models.RateLimitValue{Requests: 0, Unit: "Minute"}},
	})
	expectCreateRejected(t, cfg)
}

// TestRateLimitValidationEmptyRules ports
// rate_limiting/test_validation.py:test_empty_rules: a rateLimit.global
// with zero rules must be rejected at route creation.
func TestRateLimitValidationEmptyRules(t *testing.T) {
	t.Parallel()
	cfg := rateLimitValidationConfig(t, []models.RateLimitRule{})
	expectCreateRejected(t, cfg)
}
