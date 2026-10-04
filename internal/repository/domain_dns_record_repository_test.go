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

// seedDNSProviderCredential creates a DNS provider credential with the given name.
// userID must already exist in the database. Returns the credential ID.
func seedDNSProviderCredential(t *testing.T, db *gorm.DB, createdByUserID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	err := db.Exec(`
		INSERT INTO dns_provider_credentials (id, name, provider_type, credentials, created_by, created_at, updated_at)
		VALUES (?, ?, ?, '{}', ?, NOW(), NOW())`,
		id, name, "cloudflare", createdByUserID).Error
	require.NoError(t, err)
	return id
}

// TestDomainDNSRecordRepository_CreateGetUpdateDelete tests the full CRUD lifecycle.
func TestDomainDNSRecordRepository_CreateGetUpdateDelete(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewDomainDNSRecordRepository(db)

	// Seed project and domain
	_, domainID, _, userID := seedProject(t, db)

	// Seed DNS provider credential
	credID := seedDNSProviderCredential(t, db, userID, "test-credential")

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM domain_dns_records WHERE domain_id = ?`, domainID).Error
		_ = db.Exec(`DELETE FROM dns_provider_credentials WHERE id = ?`, credID).Error
	})

	// Create a DNS record
	record := &models.DomainDNSRecord{
		DomainID:             domainID,
		ProviderCredentialID: credID,
		RecordType:           models.DNSRecordTypeAuto,
		TTL:                  nil,
		Proxied:              false,
		ResolvedTarget:       "192.0.2.1",
		Status:               models.DNSRecordStatusPending,
		StatusMessage:        "Pending sync",
		EndpointName:         "gateway-endpoint",
		CreatedBy:            userID,
	}

	err := repo.Create(record)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, record.ID, "Create should populate ID")

	// GetByDomainID
	retrieved, err := repo.GetByDomainID(domainID)
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	assert.Equal(t, record.ID, retrieved.ID)
	assert.Equal(t, domainID, retrieved.DomainID)
	assert.Equal(t, credID, retrieved.ProviderCredentialID)
	assert.Equal(t, models.DNSRecordStatusPending, retrieved.Status)

	// Update the record
	retrieved.Status = models.DNSRecordStatusReady
	retrieved.ResolvedTarget = "192.0.2.2"
	err = repo.Update(retrieved)
	require.NoError(t, err)

	// Verify the update
	updated, err := repo.GetByDomainID(domainID)
	require.NoError(t, err)
	assert.Equal(t, models.DNSRecordStatusReady, updated.Status)
	assert.Equal(t, "192.0.2.2", updated.ResolvedTarget)

	// DeleteByDomainID
	err = repo.DeleteByDomainID(domainID)
	require.NoError(t, err)

	// Verify deletion
	_, err = repo.GetByDomainID(domainID)
	assert.Error(t, err)
	assert.Equal(t, gorm.ErrRecordNotFound, err)
}

// TestDomainDNSRecordRepository_CountByCredential tests counting records by credential.
func TestDomainDNSRecordRepository_CountByCredential(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewDomainDNSRecordRepository(db)

	// Seed project and domain
	projectID, domainID1, _, userID := seedProject(t, db)

	// Seed another domain in the same project
	domainID2 := uuid.New()
	err := db.Exec(`
		INSERT INTO domains (id, project_id, name, hostname, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		domainID2, projectID, "test-domain-2-"+domainID2.String(), domainID2.String()+".test.example.com", userID).Error
	require.NoError(t, err)

	// Seed DNS provider credentials
	cred1ID := seedDNSProviderCredential(t, db, userID, "test-credential-1")
	cred2ID := seedDNSProviderCredential(t, db, userID, "test-credential-2")

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM domain_dns_records WHERE domain_id IN (?, ?)`, domainID1, domainID2).Error
		_ = db.Exec(`DELETE FROM domains WHERE id = ?`, domainID2).Error
		_ = db.Exec(`DELETE FROM dns_provider_credentials WHERE id IN (?, ?)`, cred1ID, cred2ID).Error
	})

	// Create records for the first credential (one per domain)
	record1 := &models.DomainDNSRecord{
		DomainID:             domainID1,
		ProviderCredentialID: cred1ID,
		RecordType:           models.DNSRecordTypeAuto,
		Status:               models.DNSRecordStatusReady,
		CreatedBy:            userID,
	}
	require.NoError(t, repo.Create(record1))

	record2 := &models.DomainDNSRecord{
		DomainID:             domainID2,
		ProviderCredentialID: cred1ID,
		RecordType:           models.DNSRecordTypeAuto,
		Status:               models.DNSRecordStatusReady,
		CreatedBy:            userID,
	}
	require.NoError(t, repo.Create(record2))

	// Create a record for the second credential
	record3 := &models.DomainDNSRecord{
		DomainID:             domainID1,
		ProviderCredentialID: cred2ID,
		RecordType:           models.DNSRecordTypeA,
		Status:               models.DNSRecordStatusPending,
		CreatedBy:            userID,
	}
	require.NoError(t, repo.Create(record3))

	// Count by first credential should be 2
	count1, err := repo.CountByCredential(cred1ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count1)

	// Count by second credential should be 1
	count2, err := repo.CountByCredential(cred2ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count2)

	// Count by non-existent credential should be 0
	count3, err := repo.CountByCredential(uuid.New())
	require.NoError(t, err)
	assert.Equal(t, int64(0), count3)
}

// Ensure the models package import is used.
var _ models.DomainDNSRecord
