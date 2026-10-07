# DNS Collision Check at Domain Create — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reject a DNS-enabled domain create up-front when the hostname's DNS is already claimed — by another FastGateway project (inside) or a foreign provider record (outside) — with distinct, source-specific errors.

**Architecture:** A type-agnostic `RecordExistsForName` on every DNS provider client, a cross-project `HostnameClaimExists` repo query, and a normalized `DNSRecordService.CheckCollision` pre-flight (containment → inside DB → outside provider) wired at the top of `DomainService.Create` (fail with nothing persisted) and into first-time `Enable`. Hostname matching is normalized (lowercase + strip trailing dot), and the existing `reconcile` containment compare is fixed to match.

**Tech Stack:** Go / Gin / GORM / client-go / testify (backend-v2); Next.js 16 / TypeScript / Jest (frontend-v2).

**Spec:** `docs/superpowers/specs/2026-10-07-dns-collision-check-design.md`

## Global Constraints

- Repos: `backend-v2` (primary) + `frontend-v2` (surface the error). Isolated worktrees off `main`, local commits, confirm before push.
- Go module path: `github.com/fastgateway-dev/backend-v2`.
- Collision entry points are **domain-create** and **first-time enable** only. `Update` stays zone-immutable and is NOT guarded.
- Matching normalizes with `normalizeHostname(s) = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))`; the existing `reconcile` containment compare is fixed to normalize too.
- Inside check counts any `DomainDNSRecord` row regardless of status. Provider-unreachable during the outside check → fail (mapped 502). The inside-collision message must NOT name the other project.
- Mocks are generated (`make mocks`; `make mocks-check` must pass). OpenAPI unchanged (no new endpoints). Repo tests are Postgres integration tests gated by `INTEGRATION_DB_URL` (see spec/plan note below).
- Running the repo test needs Postgres: `docker run -d --name fgw-pg -e POSTGRES_USER=fastgateway -e POSTGRES_PASSWORD=fastgateway -e POSTGRES_DB=fastgateway -p 55432:5432 postgres:16-alpine`; `DATABASE_PORT=55432 go run cmd/migrate/main.go up`; `INTEGRATION_DB_URL="postgres://fastgateway:fastgateway@localhost:55432/fastgateway?sslmode=disable"`.

## Review Focus

- **Case / trailing-dot variance** (`App.Example.com.` vs stored `app.example.com`) must still collide and still pass containment → Task 3 (`CheckCollision` normalization) + Task 1/2 tests.
- **Provider unreachable** during the outside check must fail with 502, not a false "clear" → Task 3 (`ErrDNSProviderUnavailable`) + Task 5 handler mapping test.
- **Create with a bad/empty `hostedZoneId`** while `dns.enabled` → 400, nothing created → Task 5.
- **Out-of-cluster (`dnsRecords` nil)** → the whole check is skipped, create proceeds as before → Task 5.
- **Inside collision must not reveal the other project's name** → Task 3 (message asserted literally).

---

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/dnsprovider/provider.go` | add `RecordExistsForName` to `DNSClient`. |
| `internal/dnsprovider/{cloudflare,route53,google}.go` | implement it per provider. |
| `internal/dnsprovider/*_client_test.go` | per-provider tests. |
| `internal/repository/domain_dns_record_repository.go` (+ interfaces.go) | `HostnameClaimExists` query. |
| `internal/services/dns_record_service.go` | `normalizeHostname`, sentinels, `CheckCollision`, containment fix, `Enable` wiring. |
| `internal/services/domain_service.go` | `DNSRecordManager.CheckCollision`, `Create` pre-flight. |
| `internal/handlers/dns_record_handler.go` | map new sentinels (Enable path). |
| `internal/handlers/domain_handler.go` | map new sentinels (Create path). |
| `internal/mocks/*` | regenerated. |
| `frontend-v2/.../domains/create/page.tsx` | surface the 409. |

---

## Task 1: Provider `RecordExistsForName` (type-agnostic)

**Repo:** `backend-v2`.

**Files:**
- Modify: `internal/dnsprovider/provider.go`, `cloudflare.go`, `route53.go`, `google.go`
- Modify (fakes): `internal/services/dns_record_service_test.go` (the `recDNSClient` fake)
- Test: `internal/dnsprovider/cloudflare_client_test.go`, `route53_client_test.go`, `google_client_test.go`

**Interfaces:**
- Produces: `DNSClient.RecordExistsForName(ctx context.Context, providerZoneID, name string) (bool, error)` — true iff any A/AAAA/CNAME record exists at `name`.

- [ ] **Step 1: Add to the interface** — in `provider.go`, inside `type DNSClient interface`, after `DeleteRecord`:

```go
	// RecordExistsForName reports whether any A, AAAA, or CNAME record exists
	// at name in the zone, regardless of type. Used by the create-time
	// collision check, where the eventual record type (auto -> A/AAAA/CNAME)
	// is not yet known.
	RecordExistsForName(ctx context.Context, providerZoneID, name string) (bool, error)
```

- [ ] **Step 2: Write the failing Cloudflare test** — in `cloudflare_client_test.go`:

```go
func TestCloudflareClient_RecordExistsForName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// dns_records?name=app.example.com -> one A record
		w.Write([]byte(`{"success":true,"result":[{"id":"r1","type":"A","name":"app.example.com"}],"result_info":{"page":1,"total_pages":1}}`))
	}))
	defer srv.Close()
	c, err := cloudflare{}.newClientWithBaseURL(map[string]string{"apiToken": "tok"}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	exists, err := c.RecordExistsForName(context.Background(), "zone123", "app.example.com")
	if err != nil || !exists {
		t.Fatalf("RecordExistsForName=%v,%v want true,nil", exists, err)
	}
}

func TestCloudflareClient_RecordExistsForName_None(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"result":[],"result_info":{"page":1,"total_pages":1}}`))
	}))
	defer srv.Close()
	c, _ := cloudflare{}.newClientWithBaseURL(map[string]string{"apiToken": "tok"}, srv.URL)
	exists, err := c.RecordExistsForName(context.Background(), "zone123", "app.example.com")
	if err != nil || exists {
		t.Fatalf("RecordExistsForName=%v,%v want false,nil", exists, err)
	}
}
```

- [ ] **Step 3: Run; expect FAIL** — `go test ./internal/dnsprovider/ -run TestCloudflareClient_RecordExistsForName` → build error (method undefined).

- [ ] **Step 4: Implement all three providers**

`cloudflare.go` (after `GetRecord`):
```go
func (c *cloudflareClient) RecordExistsForName(ctx context.Context, providerZoneID, name string) (bool, error) {
	records, _, err := c.api.ListDNSRecords(ctx, cf.ZoneIdentifier(providerZoneID), cf.ListDNSRecordsParams{Name: name})
	if err != nil {
		return false, err
	}
	for _, r := range records {
		switch r.Type {
		case "A", "AAAA", "CNAME":
			return true, nil
		}
	}
	return false, nil
}
```

`route53.go` (after `GetRecord`):
```go
func (c *route53Client) RecordExistsForName(ctx context.Context, providerZoneID, name string) (bool, error) {
	out, err := c.api.ListResourceRecordSets(ctx, &r53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(providerZoneID),
		StartRecordName: aws.String(name),
		MaxItems:        aws.Int32(10),
	})
	if err != nil {
		return false, err
	}
	for _, rrs := range out.ResourceRecordSets {
		if strings.TrimSuffix(aws.ToString(rrs.Name), ".") != name {
			continue
		}
		switch string(rrs.Type) {
		case "A", "AAAA", "CNAME":
			return true, nil
		}
	}
	return false, nil
}
```

`google.go` (after `GetRecord`):
```go
func (c *googleClient) RecordExistsForName(ctx context.Context, providerZoneID, name string) (bool, error) {
	out, err := c.svc.ResourceRecordSets.List(c.project, providerZoneID).Name(name + ".").Context(ctx).Do()
	if err != nil {
		return false, err
	}
	for _, rr := range out.Rrsets {
		switch rr.Type {
		case "A", "AAAA", "CNAME":
			return true, nil
		}
	}
	return false, nil
}
```

- [ ] **Step 5: Add route53 + google tests** — mirror the Cloudflare pair in `route53_client_test.go` and `google_client_test.go`, following each file's existing httptest-server pattern: a "name has an A record" case → true, and an "empty" case → false. (Route53's fake returns a `ListResourceRecordSets` XML/JSON body with one `A` set named `app.example.com.`; Google's returns `{"rrsets":[{"type":"A","name":"app.example.com."}]}`.)

- [ ] **Step 6: Satisfy the service-test fake** — in `internal/services/dns_record_service_test.go`, add to the `recDNSClient` fake (so the services package still compiles):

```go
func (c *recDNSClient) RecordExistsForName(ctx context.Context, providerZoneID, name string) (bool, error) {
	return c.existsForName, c.existsErr
}
```
and add fields `existsForName bool` and `existsErr error` to the `recDNSClient` struct.

- [ ] **Step 7: Run; expect PASS** — `go test ./internal/dnsprovider/ ./internal/services/ 2>&1 | tail` → ok (services compiles; provider tests pass).

- [ ] **Step 8: Commit**

```bash
git add internal/dnsprovider/ internal/services/dns_record_service_test.go
git commit -m "feat(dns): type-agnostic RecordExistsForName on all providers"
```

---

## Task 2: Repo `HostnameClaimExists` (cross-project inside check)

**Files:**
- Modify: `internal/repository/domain_dns_record_repository.go`, `internal/repository/interfaces.go`
- Regenerate: `internal/mocks/mock_repositories.go` via `make mocks`
- Test: `internal/repository/domain_dns_record_repository_test.go`

**Interfaces:**
- Produces: `DomainDNSRecordRepositoryInterface.HostnameClaimExists(hostname string, zoneID, excludeDomainID uuid.UUID) (bool, error)` — true iff a record exists for the (normalized) hostname in the zone on a domain other than `excludeDomainID`.

- [ ] **Step 1: Add to the interface** — in `interfaces.go`, inside `DomainDNSRecordRepositoryInterface`, after `ListByProjectID`:

```go
	HostnameClaimExists(hostname string, zoneID, excludeDomainID uuid.UUID) (bool, error)
```

- [ ] **Step 2: Write the failing repo test** — add to `domain_dns_record_repository_test.go`:

```go
func TestDomainDNSRecordRepository_HostnameClaimExists(t *testing.T) {
	db := requirePostgres(t)
	repo := repository.NewDomainDNSRecordRepository(db)

	projectA, domainA, _, userA := seedProject(t, db)
	projectB := uuid.New()
	domainB := uuid.New()
	// project B + a domain with the SAME hostname (mixed case + trailing dot).
	require.NoError(t, db.Exec(`INSERT INTO projects (id, name, created_by, created_at, updated_at) VALUES (?, ?, ?, NOW(), NOW())`, projectB, "projB-"+projectB.String(), userA).Error)
	require.NoError(t, db.Exec(`INSERT INTO domains (id, project_id, name, hostname, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		domainB, projectB, "dB-"+domainB.String(), "App.Example.com.", userA).Error)

	cred := seedDNSProviderCredential(t, db, userA, "cred-"+uuid.NewString())
	zone := seedDNSHostedZone(t, db, userA, cred, "example.com")
	otherZone := seedDNSHostedZone(t, db, userA, cred, "other.com")

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM domain_dns_records WHERE domain_id IN (?, ?)`, domainA, domainB).Error
		_ = db.Exec(`DELETE FROM dns_hosted_zones WHERE id IN (?, ?)`, zone, otherZone).Error
		_ = db.Exec(`DELETE FROM dns_provider_credentials WHERE id = ?`, cred).Error
		_ = db.Exec(`DELETE FROM domains WHERE id = ?`, domainB).Error
		_ = db.Exec(`DELETE FROM projects WHERE id = ?`, projectB).Error
	})

	// project B claims app.example.com in `zone`.
	require.NoError(t, repo.Create(&models.DomainDNSRecord{DomainID: domainB, HostedZoneID: zone, RecordType: models.DNSRecordTypeAuto, Status: models.DNSRecordStatusReady, CreatedBy: userA}))

	// A new create for the same hostname (normalized) in the same zone, excluding nothing -> claimed.
	got, err := repo.HostnameClaimExists("app.example.com", zone, uuid.Nil)
	require.NoError(t, err)
	assert.True(t, got, "same hostname+zone on another project is a claim (case/dot-insensitive)")

	// Different zone -> not claimed.
	got, err = repo.HostnameClaimExists("app.example.com", otherZone, uuid.Nil)
	require.NoError(t, err)
	assert.False(t, got)

	// Excluding the only claimant -> not claimed.
	got, err = repo.HostnameClaimExists("app.example.com", zone, domainB)
	require.NoError(t, err)
	assert.False(t, got)

	_ = projectA
	_ = domainA
}
```

- [ ] **Step 3: Run; expect FAIL** — `INTEGRATION_DB_URL=... go test ./internal/repository/ -run TestDomainDNSRecordRepository_HostnameClaimExists` → build error (method undefined).

- [ ] **Step 4: Implement** — in `domain_dns_record_repository.go`, after `ListByProjectID`:

```go
// HostnameClaimExists reports whether any DomainDNSRecord exists for hostname in
// zoneID on a domain other than excludeDomainID (uuid.Nil excludes nothing).
// Matching is case- and trailing-dot-insensitive on both sides. Because a
// hostname is unique within a project, a match is always another project.
func (r *DomainDNSRecordRepository) HostnameClaimExists(hostname string, zoneID, excludeDomainID uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.Raw(`
		SELECT EXISTS(
			SELECT 1 FROM domain_dns_records rec
			JOIN domains d ON d.id = rec.domain_id
			WHERE LOWER(TRIM(TRAILING '.' FROM d.hostname)) = LOWER(TRIM(TRAILING '.' FROM ?))
			  AND rec.hosted_zone_id = ?
			  AND rec.domain_id <> ?)`,
		hostname, zoneID, excludeDomainID).Scan(&exists).Error
	return exists, err
}
```

- [ ] **Step 5: Regenerate mocks** — `make mocks` (adds the method to `MockDomainDNSRecordRepository`).

- [ ] **Step 6: Run; expect PASS** — `INTEGRATION_DB_URL=... go test ./internal/repository/ -run TestDomainDNSRecordRepository_HostnameClaimExists -v` → PASS; `make mocks-check` → clean.

- [ ] **Step 7: Commit**

```bash
git add internal/repository/ internal/mocks/mock_repositories.go
git commit -m "feat(dns): HostnameClaimExists cross-project claim query"
```

---

## Task 3: Service `CheckCollision` + normalization + containment fix

**Files:**
- Modify: `internal/services/dns_record_service.go`
- Test: `internal/services/dns_record_service_test.go`

**Interfaces:**
- Consumes: `RecordExistsForName` (Task 1), `HostnameClaimExists` (Task 2), `s.zoneRepo.GetByID`, `s.creds.DecryptedCredentials`, `dnsProviderLookup`.
- Produces: `normalizeHostname(string) string`; sentinels `ErrHostnameClaimed`, `ErrForeignRecordExists`, `ErrDNSProviderUnavailable`; `(*DNSRecordService).CheckCollision(hostname string, zoneID, excludeDomainID uuid.UUID) error`.

- [ ] **Step 1: Write failing service tests** — add to `dns_record_service_test.go` (the harness's `recZoneRepo`, `recCredReader`, and `recDNSClient` already exist; set `h.repo` behavior via the fake). Build a small helper that calls `h.svc.CheckCollision`. Cases:
  - containment mismatch (`other.org` in `example.com`) → `ErrHostedZoneMismatch`.
  - inside claimed (`fakeRecRepo.HostnameClaimExists` returns true) → `ErrHostnameClaimed`, and the error string does NOT contain a project name/uuid.
  - outside exists (`recDNSClient.existsForName = true`) → `ErrForeignRecordExists`.
  - provider error (`recDNSClient.existsErr = errors.New("boom")`) → `errors.Is(err, ErrDNSProviderUnavailable)`.
  - all clear → nil.
  - case/dot: hostname `App.Example.com.` in zone `example.com` passes containment.

  (Add a `claimExists bool` + `claimErr error` to `fakeRecRepo` and implement `HostnameClaimExists` on it returning those.)

- [ ] **Step 2: Run; expect FAIL** — `go test ./internal/services/ -run TestCheckCollision` → build error.

- [ ] **Step 3: Implement** — in `dns_record_service.go`:

```go
var (
	ErrHostnameClaimed       = errors.New("DNS for this hostname is already managed by another project in this hosted zone")
	ErrForeignRecordExists   = errors.New("a DNS record already exists at the provider for this hostname that FastGateway does not manage")
	ErrDNSProviderUnavailable = errors.New("could not verify DNS at the provider; try again")
)

// normalizeHostname lowercases, trims surrounding space, and strips one trailing
// dot, so DNS comparisons don't depend on case or FQDN form.
func normalizeHostname(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

// CheckCollision validates that hostname can take a managed DNS record in zoneID
// without clobbering an existing claim. excludeDomainID is the domain being
// changed (uuid.Nil at create). Returns, in order: ErrHostedZoneMismatch (not in
// zone), ErrHostnameClaimed (another project), ErrForeignRecordExists (a foreign
// provider record), ErrDNSProviderUnavailable (could not verify), or nil.
func (s *DNSRecordService) CheckCollision(hostname string, zoneID, excludeDomainID uuid.UUID) error {
	zone, err := s.zoneRepo.GetByID(zoneID)
	if err != nil {
		return err
	}
	nh, nz := normalizeHostname(hostname), normalizeHostname(zone.Name)
	if nh != nz && !strings.HasSuffix(nh, "."+nz) {
		return ErrHostedZoneMismatch
	}
	claimed, err := s.repo.HostnameClaimExists(hostname, zoneID, excludeDomainID)
	if err != nil {
		return err
	}
	if claimed {
		return ErrHostnameClaimed
	}
	providerType, creds, err := s.creds.DecryptedCredentials(zone.ProviderCredentialID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDNSProviderUnavailable, err)
	}
	prov, ok := dnsProviderLookup(providerType)
	if !ok {
		return fmt.Errorf("%w: unsupported DNS provider %q", ErrDNSProviderUnavailable, providerType)
	}
	client, err := prov.NewClient(creds)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDNSProviderUnavailable, err)
	}
	exists, err := client.RecordExistsForName(context.Background(), zone.ProviderZoneID, nh)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDNSProviderUnavailable, err)
	}
	if exists {
		return ErrForeignRecordExists
	}
	return nil
}
```

- [ ] **Step 4: Fix the containment compare in `reconcile`** — replace the containment check (currently `if domain.Hostname != zone.Name && !strings.HasSuffix(domain.Hostname, "."+zone.Name)`) with the normalized form, and the apex-CNAME guard `domain.Hostname == zone.Name`:

```go
	nh, nz := normalizeHostname(domain.Hostname), normalizeHostname(zone.Name)
	if nh != nz && !strings.HasSuffix(nh, "."+nz) {
		setErr(ErrHostedZoneMismatch.Error())
		return
	}
```
and later:
```go
	if rt == models.DNSRecordTypeCNAME && nh == nz {
		setErr(ErrApexCNAME.Error())
		return
	}
```

- [ ] **Step 5: Run; expect PASS** — `go test ./internal/services/ -run 'TestCheckCollision|TestDetectVersions|Test' 2>&1 | tail` → the CheckCollision tests pass and the existing reconcile tests still pass (normalization is a superset of the old exact-match for already-normalized inputs). If any existing reconcile test used a non-normalized hostname/zone that previously failed, update it per the ruling that normalized matching is the intended behavior.

- [ ] **Step 6: Commit**

```bash
git add internal/services/dns_record_service.go internal/services/dns_record_service_test.go
git commit -m "feat(dns): CheckCollision pre-flight + hostname normalization"
```

---

## Task 4: Wire `Enable` + map the new errors (settings path)

**Files:**
- Modify: `internal/services/dns_record_service.go` (`Enable`)
- Modify: `internal/handlers/dns_record_handler.go` (`mapDNSRecordServiceError`)
- Test: `internal/services/dns_record_service_test.go`, `internal/handlers/dns_record_handler_test.go`

**Interfaces:**
- Consumes: `CheckCollision` (Task 3). Produces: `Enable` now returns the collision sentinels; handler maps them to 409/502.

- [ ] **Step 1: Write failing tests** — service: `Enable` with a claimed hostname returns `ErrHostnameClaimed` and creates no record (`repo.GetByDomainID` still `ErrRecordNotFound`). Handler: `mapDNSRecordServiceError` maps `ErrHostnameClaimed`/`ErrForeignRecordExists` → 409 and `ErrDNSProviderUnavailable` → 502. (Add a handler test calling a handler whose mock service returns each sentinel from `Enable`.)

- [ ] **Step 2: Run; expect FAIL.**

- [ ] **Step 3: Wire `Enable`** — in `Enable`, after the `isValidRecordType(rt)` check and before building `rec`:

```go
	domain, err := s.domainRepo.GetByID(domainID)
	if err != nil {
		return nil, err
	}
	if err := s.CheckCollision(domain.Hostname, *in.HostedZoneID, domainID); err != nil {
		return nil, err
	}
```

- [ ] **Step 4: Map the errors** — in `mapDNSRecordServiceError` (`dns_record_handler.go`), add cases before the default:

```go
	case errors.Is(err, services.ErrHostnameClaimed), errors.Is(err, services.ErrForeignRecordExists):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrDNSProviderUnavailable):
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
```

- [ ] **Step 5: Run; expect PASS** — `go test ./internal/services/ ./internal/handlers/ 2>&1 | tail`.

- [ ] **Step 6: Commit**

```bash
git add internal/services/dns_record_service.go internal/handlers/dns_record_handler.go internal/services/dns_record_service_test.go internal/handlers/dns_record_handler_test.go
git commit -m "feat(dns): reject enable on collision; map 409/502"
```

---

## Task 5: Create pre-flight + `DNSRecordManager` + domain handler mapping

**Files:**
- Modify: `internal/services/domain_service.go` (`DNSRecordManager`, `Create`)
- Modify: `internal/handlers/domain_handler.go` (`Create` error mapping)
- Regenerate: mocks (if `DNSRecordManager` is mocked)
- Test: `internal/services/domain_service_test.go`, `internal/handlers/domain_handler_test.go`

**Interfaces:**
- Consumes: `DNSRecordService.CheckCollision` (Task 3). Produces: create fails on collision with nothing persisted; 409/400/502 at the handler.

- [ ] **Step 1: Extend `DNSRecordManager`** — in `domain_service.go`:

```go
type DNSRecordManager interface {
	Enable(domainID, projectID, createdBy uuid.UUID, in DNSRecordInput) (*models.DomainDNSRecord, error)
	Delete(domainID, projectID uuid.UUID) error
	CheckCollision(hostname string, hostedZoneID, excludeDomainID uuid.UUID) error
}
```
(`*DNSRecordService` already satisfies it from Task 3.)

- [ ] **Step 2: Write the failing create test** — in `domain_service_test.go`, with a fake `DNSRecordManager` whose `CheckCollision` returns `ErrHostnameClaimed`: `Create` with `input.DNS.Enabled` + a valid `HostedZoneID` returns that error, and `domainRepo.Create` is never called (no domain persisted, no Gateway call). A `CheckCollision` returning nil proceeds as today. (If the existing domain_service test uses a hand fake for `DNSRecordManager`, add `CheckCollision` to it; if it uses the generated mock, `make mocks`.)

- [ ] **Step 3: Run; expect FAIL.**

- [ ] **Step 4: Add the pre-flight** — at the very top of `Create`, before the `ExistsByHostname` check:

```go
	// DNS collision pre-flight: when DNS is requested, reject the whole create
	// up-front (nothing persisted) if the hostname's DNS is already claimed
	// inside FastGateway (another project) or by a foreign provider record.
	if input.DNS != nil && input.DNS.Enabled && s.dnsRecords != nil {
		if input.DNS.HostedZoneID == "" {
			return nil, ErrNoHostedZone
		}
		zoneID, err := uuid.Parse(input.DNS.HostedZoneID)
		if err != nil {
			return nil, errors.New("invalid hostedZoneId")
		}
		if err := s.dnsRecords.CheckCollision(input.Hostname, zoneID, uuid.Nil); err != nil {
			return nil, err
		}
	}
```

- [ ] **Step 5: Map the errors in the domain handler** — in `domain_handler.go` `Create`, replace the single `c.JSON(http.StatusBadRequest, ...)` after `h.domainService.Create(...)` with:

```go
	domain, err := h.domainService.Create(projectID, &input, user.ID)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrHostnameClaimed), errors.Is(err, services.ErrForeignRecordExists):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case errors.Is(err, services.ErrDNSProviderUnavailable):
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		}
		return
	}
```
(Add the `errors` and `services` imports if not present.)

- [ ] **Step 6: Run; expect PASS** — `go test ./internal/services/ ./internal/handlers/ 2>&1 | tail`; `make mocks-check`; `go build ./...`.

- [ ] **Step 7: Commit**

```bash
git add internal/services/domain_service.go internal/handlers/domain_handler.go internal/services/domain_service_test.go internal/handlers/domain_handler_test.go internal/mocks/
git commit -m "feat(dns): fail domain-create up-front on DNS collision"
```

---

## Task 6: Frontend — surface the 409 on the create form

**Repo:** `frontend-v2`.

**Files:**
- Modify (if needed): `src/app/projects/[projectId]/domains/create/page.tsx`
- Test: `src/app/projects/[projectId]/domains/create/page.test.tsx` (create if absent)

**Interfaces:** Consumes the backend 409 from `domainsApi.create`.

- [ ] **Step 1: Write the failing test** — mock `domainsApi.create` to reject with an axios-shaped 409 (`{ response: { data: { error: 'DNS for this hostname is already managed by another project in this hosted zone' } } }`); render the create page, fill name/hostname/template, submit, and assert the message appears and the form is not stuck (submit re-enabled).

```tsx
test('shows the DNS collision error from a 409 on create', async () => {
  (domainsApi.create as jest.Mock).mockRejectedValue({ response: { data: { error: 'DNS for this hostname is already managed by another project in this hosted zone' } } });
  // render, fill required fields, click Create Domain
  // await waitFor -> expect screen.getByText(/already managed by another project/i)
});
```

- [ ] **Step 2: Run; expect FAIL** (or confirm it already passes if the page's existing catch surfaces `error.response.data.error`).

- [ ] **Step 3: Implement if needed** — verify `handleCreate`'s catch sets an error state from `error.response?.data?.error` and renders it near the submit button, and re-enables the button. If the existing handler already does this (it shows create errors today), no code change — the test documents the behavior. If it swallows the message, wire it to the error banner.

- [ ] **Step 4: Run; expect PASS** — `npx jest create/page.test`; `npx tsc --noEmit`.

- [ ] **Step 5: Commit**

```bash
git add "src/app/projects/[projectId]/domains/create/page.test.tsx" "src/app/projects/[projectId]/domains/create/page.tsx"
git commit -m "test(dns): create page surfaces the DNS collision 409"
```

---

## Out of Scope

- Making `Update` honor a hosted-zone change (and guarding it) / fixing the edit-modal zone picker — tracked as its own task.
- Global domain uniqueness, project-scoped hosted zones, coordinated multi-project sharing, ownership transfer.
