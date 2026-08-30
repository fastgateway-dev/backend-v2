//go:build e2e

package domain

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/services"
)

// domainSettingsEnvelope decodes both GET and PUT/POST responses for the
// domain settings endpoints: DomainHandler.GetDomainSettings returns
// *DomainSettingsResponse{Settings *models.DomainSettingsConfig
// `json:"settings"`}, and DomainHandler.UpdateDomainSettings /
// AddDomainMTLSCA both return *models.DomainSettings{Config
// models.DomainSettingsConfig `json:"settings"`} -- one envelope type
// decodes all three responses. Mirrors
// e2e/suites/security/domain_mtls_helpers_test.go's identical type (that
// package predates this one and could not depend on it).
type domainSettingsEnvelope struct {
	Settings models.DomainSettingsConfig `json:"settings"`
}

// getDomainSettings mirrors regression/helpers/api.py:get_domain_settings
// (GET /projects/:projectId/domains/:domainId/settings). Neither
// e2e/harness/api.go nor any earlier suite exposes a typed wrapper for
// this endpoint, so this package -- like security before it -- calls
// API.Do directly against the route registered in cmd/server/main.go's
// "/domains/:domainId" group.
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
// services.UpdateDomainSettingsInput{} (every field nil, marshaling to
// "{}") is what DomainService.UpdateDomainSettings treats as "everything
// empty": it deletes the ClientTrafficPolicy and settings row entirely --
// exactly mirroring the Python source's `update_domain_settings(...,{})`
// reset call used by every domain_settings test's cleanup.
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
// whatever mTLS config is already present (DomainService.AddDomainMTLSCA)
// -- callers must have already enabled mTLS via updateDomainSettings
// first, the same two-step sequence the Python source uses.
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

// cleanupDomainSettings removes any leftover mTLS CAs and resets every
// domain-level setting (mTLS, clientIPDetection, clientConnection, ...)
// back to empty. It mirrors
// regression/tests/domain_settings/conftest.py:cleanup_domain_mtls, and is
// used by every test in this package that mutates the shared domain (all
// 11 domain_settings tests -- both mTLS AND the non-mTLS settings the
// client_ip_detection/tcp_keepalive tests touch, since
// updateDomainSettings(..., UpdateDomainSettingsInput{}) resets the whole
// row regardless of which fields were last set).
//
// Every step is best-effort when called as PRE-cleanup (report=false --
// a previous crashed run may have left nothing, or something partial,
// behind). The t.Cleanup-registered call (report=true) additionally fails
// the test via t.Errorf on any failure, matching
// harness.Fixture.Route's own cleanup convention, so a real leak still
// fails the test instead of silently accumulating -- exactly mirroring
// e2e/suites/security/domain_mtls_helpers_test.go:cleanupDomainMTLS.
func cleanupDomainSettings(t *testing.T, report bool) {
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
