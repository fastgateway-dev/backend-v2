//go:build e2e

package security

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// TestClientModeMTLS is the only test in this package that mutates
// domain-level settings, so its supporting helpers live in their own file.
// They mirror regression/tests/domain_settings/conftest.py's
// cleanup_domain_mtls and helpers/api.py's get_domain_settings/
// update_domain_settings/add_domain_mtls_ca -- none of which
// e2e/harness/api.go exposes typed wrappers for (the domain_settings suite
// itself has not been ported yet), so these use API.Do directly against
// the endpoints registered in cmd/server/main.go's "/domains/:domainId"
// group. Both GET and PUT/POST for domain settings nest the actual config
// under a top-level "settings" key -- DomainHandler.GetDomainSettings
// returns *DomainSettingsResponse{Settings *models.DomainSettingsConfig
// `json:"settings"`, ...} and DomainHandler.UpdateDomainSettings /
// AddDomainMTLSCA both return *models.DomainSettings{Config
// models.DomainSettingsConfig `json:"settings"`, ...} -- so one envelope
// type decodes all three responses.
type domainSettingsEnvelope struct {
	Settings models.DomainSettingsConfig `json:"settings"`
}

// getDomainSettings mirrors api.py:get_domain_settings (GET
// /projects/:projectId/domains/:domainId/settings).
func getDomainSettings(ctx context.Context, projectID, domainID string) (models.DomainSettingsConfig, error) {
	var out domainSettingsEnvelope
	path := "/projects/" + projectID + "/domains/" + domainID + "/settings"
	if _, err := env.Admin.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return models.DomainSettingsConfig{}, err
	}
	return out.Settings, nil
}

// updateDomainSettings mirrors api.py:update_domain_settings (PUT
// /projects/:projectId/domains/:domainId/settings). Sending a zero-value
// services.UpdateDomainSettingsInput{} (all nil fields, marshaling to
// "{}") is what DomainService.UpdateDomainSettings treats as "everything
// empty" -- it deletes the ClientTrafficPolicy and settings row entirely,
// exactly mirroring the Python source's `update_domain_settings(...,{})`
// reset call.
func updateDomainSettings(ctx context.Context, projectID, domainID string, input services.UpdateDomainSettingsInput) (models.DomainSettingsConfig, error) {
	var out domainSettingsEnvelope
	path := "/projects/" + projectID + "/domains/" + domainID + "/settings"
	if _, err := env.Admin.Do(ctx, http.MethodPut, path, input, &out); err != nil {
		return models.DomainSettingsConfig{}, err
	}
	return out.Settings, nil
}

// addDomainMTLSCA mirrors api.py:add_domain_mtls_ca (POST
// /projects/:projectId/domains/:domainId/settings/mtls/ca). Merges into
// whatever mTLS config is already present (see DomainService.
// AddDomainMTLSCA) -- callers must have already enabled MTLS via
// updateDomainSettings first, same two-step sequence the Python source
// uses.
func addDomainMTLSCA(ctx context.Context, projectID, domainID, name, caPEM string) (models.DomainSettingsConfig, error) {
	body := services.AddDomainMTLSCAInput{Name: name, CAPem: caPEM}
	var out domainSettingsEnvelope
	path := "/projects/" + projectID + "/domains/" + domainID + "/settings/mtls/ca"
	if _, err := env.Admin.Do(ctx, http.MethodPost, path, body, &out); err != nil {
		return models.DomainSettingsConfig{}, err
	}
	return out.Settings, nil
}

// removeDomainMTLSCA mirrors api.py:remove_domain_mtls_ca (DELETE
// /projects/:projectId/domains/:domainId/settings/mtls/ca/:caId).
func removeDomainMTLSCA(ctx context.Context, projectID, domainID, caID string) error {
	path := "/projects/" + projectID + "/domains/" + domainID + "/settings/mtls/ca/" + caID
	_, err := env.Admin.Do(ctx, http.MethodDelete, path, nil, nil)
	return err
}

// cleanupDomainMTLS mirrors conftest.py:cleanup_domain_mtls: best-effort
// remove any leftover CAs, then reset domain settings to empty. Every step
// is best-effort (mirroring the Python original's bare `except Exception:
// pass`) because this is also used as a PRE-cleanup (before mutating
// state) when a previous run may have left nothing, or something partial,
// behind; but the version registered via t.Cleanup additionally reports
// failures through t.Errorf, matching harness.Fixture.Route's own
// cleanup convention, so a real leak still fails the test.
func cleanupDomainMTLS(t *testing.T, report bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	settings, err := getDomainSettings(ctx, env.ProjectID, env.DomainID)
	if err == nil && settings.MTLS != nil {
		for _, ca := range settings.MTLS.CACerts {
			if err := removeDomainMTLSCA(ctx, env.ProjectID, env.DomainID, ca.ID); err != nil && report {
				t.Errorf("cleanup: remove domain mTLS CA %s: %v", ca.ID, err)
			}
		}
	}
	if _, err := updateDomainSettings(ctx, env.ProjectID, env.DomainID, services.UpdateDomainSettingsInput{}); err != nil && report {
		t.Errorf("cleanup: reset domain settings: %v", err)
	}
}
