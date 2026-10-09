package repository_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// insertTemplate inserts a DomainTemplate for the project and registers cleanup.
// Capability flags are set explicitly because EnableDomain has no GORM default.
// userID must already exist (domain_templates.created_by).
func insertTemplate(t *testing.T, db *gorm.DB, projectID, userID uuid.UUID, enableStream bool) uuid.UUID {
	t.Helper()
	tmpl := &models.DomainTemplate{
		ProjectID:    projectID,
		Name:         "test-tmpl-" + uuid.NewString(),
		CreatedBy:    userID,
		EnableDomain: !enableStream,
		EnableStream: enableStream,
	}
	require.NoError(t, db.Create(tmpl).Error)
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM streams WHERE gateway_template_id = ?`, tmpl.ID).Error
		_ = db.Exec(`DELETE FROM domain_templates WHERE id = ?`, tmpl.ID).Error
	})
	return tmpl.ID
}

func TestStreamRepository_CRUD(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, _, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)

	s := &models.Stream{ProjectID: projectID, Name: "db-gw", Namespace: "fastgateway-system",
		GatewayTemplateID: tmplID, K8sGatewayName: "str-db-gw", K8sGatewayClass: "public-lb"}
	require.NoError(t, repo.Create(s))

	got, err := repo.GetByID(s.ID)
	require.NoError(t, err)
	assert.Equal(t, "db-gw", got.Name)
	assert.Equal(t, "pending", got.Status)

	list, err := repo.ListByProjectID(projectID)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	count, err := repo.CountByGatewayTemplateID(tmplID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	got.Status = "active"
	got.StatusMessage = "ok"
	require.NoError(t, repo.Update(got))
	updated, err := repo.GetByID(s.ID)
	require.NoError(t, err)
	assert.Equal(t, "active", updated.Status)
	assert.Equal(t, "ok", updated.StatusMessage)

	require.NoError(t, repo.Delete(s.ID))
	count, err = repo.CountByGatewayTemplateID(tmplID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}
