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

// seedDNSHostedZone creates a DNS hosted zone with the given name and credential.
// Returns the zone ID.
func seedDNSHostedZone(t *testing.T, db *gorm.DB, createdByUserID, credID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	err := db.Exec(`
		INSERT INTO dns_hosted_zones (id, name, provider_credential_id, provider_zone_id, status, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		id, name, credID, "zone_"+id.String(), "ready", createdByUserID).Error
	require.NoError(t, err)
	return id
}

// TestDomainDNSRecordRepository_CreateGetUpdateDelete tests the full CRUD lifecycle.
func TestDomainDNSRecordRepository_CreateGetUpdateDelete(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewDomainDNSRecordRepository(db)

	// Seed project and domain
	_, domainID, _, userID := seedProject(t, db)

	// Seed DNS provider credential and hosted zone
	credID := seedDNSProviderCredential(t, db, userID, "test-credential")
	zoneID := seedDNSHostedZone(t, db, userID, credID, "example.com")

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM domain_dns_records WHERE domain_id = ?`, domainID).Error
		_ = db.Exec(`DELETE FROM dns_hosted_zones WHERE id = ?`, zoneID).Error
		_ = db.Exec(`DELETE FROM dns_provider_credentials WHERE id = ?`, credID).Error
	})

	// Create a DNS record
	record := &models.DomainDNSRecord{
		DomainID:       domainID,
		HostedZoneID:   zoneID,
		RecordType:     models.DNSRecordTypeAuto,
		TTL:            nil,
		Proxied:        false,
		ResolvedTarget: "192.0.2.1",
		Status:         models.DNSRecordStatusPending,
		StatusMessage:  "Pending sync",
		CreatedBy:      userID,
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
	assert.Equal(t, zoneID, retrieved.HostedZoneID)
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

// TestDomainDNSRecordRepository_CountByZone tests counting records by hosted zone.
func TestDomainDNSRecordRepository_CountByZone(t *testing.T) {
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

	// Seed hosted zones
	zone1ID := seedDNSHostedZone(t, db, userID, cred1ID, "example.com")
	zone2ID := seedDNSHostedZone(t, db, userID, cred2ID, "example.org")

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM domain_dns_records WHERE domain_id IN (?, ?)`, domainID1, domainID2).Error
		_ = db.Exec(`DELETE FROM dns_hosted_zones WHERE id IN (?, ?)`, zone1ID, zone2ID).Error
		_ = db.Exec(`DELETE FROM domains WHERE id = ?`, domainID2).Error
		_ = db.Exec(`DELETE FROM dns_provider_credentials WHERE id IN (?, ?)`, cred1ID, cred2ID).Error
	})

	// Create records for the first zone (one per domain)
	record1 := &models.DomainDNSRecord{
		DomainID:     domainID1,
		HostedZoneID: zone1ID,
		RecordType:   models.DNSRecordTypeAuto,
		Status:       models.DNSRecordStatusReady,
		CreatedBy:    userID,
	}
	require.NoError(t, repo.Create(record1))

	record2 := &models.DomainDNSRecord{
		DomainID:     domainID2,
		HostedZoneID: zone1ID,
		RecordType:   models.DNSRecordTypeAuto,
		Status:       models.DNSRecordStatusReady,
		CreatedBy:    userID,
	}
	require.NoError(t, repo.Create(record2))

	// Create a record for the second zone
	record3 := &models.DomainDNSRecord{
		DomainID:     domainID1,
		HostedZoneID: zone2ID,
		RecordType:   models.DNSRecordTypeA,
		Status:       models.DNSRecordStatusPending,
		CreatedBy:    userID,
	}
	require.NoError(t, repo.Create(record3))

	// Count by first zone should be 2
	count1, err := repo.CountByZone(zone1ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count1)

	// Count by second zone should be 1
	count2, err := repo.CountByZone(zone2ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count2)

	// Count by non-existent zone should be 0
	count3, err := repo.CountByZone(uuid.New())
	require.NoError(t, err)
	assert.Equal(t, int64(0), count3)
}

// TestDomainDNSRecordRepository_ListByProjectID verifies the project-wide list
// aggregates every domain's record in the project, enriched with the domain
// hostname and hosted-zone name, and excludes records from other projects.
func TestDomainDNSRecordRepository_ListByProjectID(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewDomainDNSRecordRepository(db)

	// Project 1 with its seeded domain, plus a second domain in the same project.
	project1, domain1a, _, user1 := seedProject(t, db)
	domain1b := uuid.New()
	require.NoError(t, db.Exec(`
		INSERT INTO domains (id, project_id, name, hostname, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		domain1b, project1, "d1b-"+domain1b.String(), "b.example.com", user1).Error)

	// Project 2 with its own domain (must not leak into project 1's list).
	project2, domain2, _, user2 := seedProject(t, db)

	cred1 := seedDNSProviderCredential(t, db, user1, "cred-"+uuid.NewString())
	zoneExample := seedDNSHostedZone(t, db, user1, cred1, "example.com")
	cred2 := seedDNSProviderCredential(t, db, user2, "cred-"+uuid.NewString())
	zoneOther := seedDNSHostedZone(t, db, user2, cred2, "other.com")

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM domain_dns_records WHERE domain_id IN (?, ?, ?)`, domain1a, domain1b, domain2).Error
		_ = db.Exec(`DELETE FROM dns_hosted_zones WHERE id IN (?, ?)`, zoneExample, zoneOther).Error
		_ = db.Exec(`DELETE FROM dns_provider_credentials WHERE id IN (?, ?)`, cred1, cred2).Error
		_ = db.Exec(`DELETE FROM domains WHERE id = ?`, domain1b).Error
	})

	require.NoError(t, repo.Create(&models.DomainDNSRecord{
		DomainID: domain1a, HostedZoneID: zoneExample, RecordType: models.DNSRecordTypeAuto,
		ResolvedTarget: "192.0.2.1", Status: models.DNSRecordStatusReady, CreatedBy: user1,
	}))
	require.NoError(t, repo.Create(&models.DomainDNSRecord{
		DomainID: domain1b, HostedZoneID: zoneExample, RecordType: models.DNSRecordTypeCNAME,
		ResolvedTarget: "gw.example.com", Status: models.DNSRecordStatusPending, CreatedBy: user1,
	}))
	require.NoError(t, repo.Create(&models.DomainDNSRecord{
		DomainID: domain2, HostedZoneID: zoneOther, RecordType: models.DNSRecordTypeA,
		ResolvedTarget: "198.51.100.9", Status: models.DNSRecordStatusReady, CreatedBy: user2,
	}))

	// Project 1 -> exactly its two records, enriched.
	items, err := repo.ListByProjectID(project1)
	require.NoError(t, err)
	require.Len(t, items, 2)

	byHost := map[string]models.DNSRecordListItem{}
	for _, it := range items {
		byHost[it.DomainHostname] = it
		assert.NotEqual(t, domain2, it.DomainID, "project 2's record must not appear")
	}
	b, ok := byHost["b.example.com"]
	require.True(t, ok, "second domain's record should be listed")
	assert.Equal(t, domain1b, b.DomainID)
	assert.Equal(t, "example.com", b.ZoneName)
	assert.Equal(t, models.DNSRecordTypeCNAME, b.RecordType)
	assert.Equal(t, "gw.example.com", b.ResolvedTarget)
	assert.Equal(t, models.DNSRecordStatusPending, b.Status)

	// Project 2 -> its single record, enriched.
	items2, err := repo.ListByProjectID(project2)
	require.NoError(t, err)
	require.Len(t, items2, 1)
	assert.Equal(t, domain2, items2[0].DomainID)
	assert.Equal(t, "other.com", items2[0].ZoneName)
}

// Ensure the models package import is used.
var _ models.DomainDNSRecord
