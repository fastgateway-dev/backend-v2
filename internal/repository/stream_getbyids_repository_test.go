package repository_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamRepository_GetByIDs(t *testing.T) {
	db := requirePostgres(t)
	projectID, _, _, userID := seedProject(t, db)
	tmplID := insertTemplate(t, db, projectID, userID, true)
	repo := repository.NewStreamRepository(db)
	a := &models.Stream{ProjectID: projectID, Name: "ids-a", Namespace: "fastgateway-system", GatewayTemplateID: tmplID}
	b := &models.Stream{ProjectID: projectID, Name: "ids-b", Namespace: "fastgateway-system", GatewayTemplateID: tmplID}
	require.NoError(t, repo.Create(a))
	require.NoError(t, repo.Create(b))

	got, err := repo.GetByIDs([]uuid.UUID{a.ID, b.ID, uuid.New()})
	require.NoError(t, err)
	names := []string{}
	for _, s := range got {
		names = append(names, s.Name)
	}
	assert.ElementsMatch(t, []string{"ids-a", "ids-b"}, names, "unknown IDs are skipped")

	none, err := repo.GetByIDs(nil)
	require.NoError(t, err)
	assert.Nil(t, none)
}
