# DNS Direct-Provider Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace external-dns delegation with FastGateway writing DNS records directly via each provider's official Go SDK (Cloudflare, Route53, Google), organized around registered hosted zones.

**Architecture:** A `DNSProvider.NewClient` → `DNSClient` (FindZone/GetRecord/Upsert/Delete) seam backed by official SDKs; a first-class `dns_hosted_zones` entity (zone name + credential + cached provider zone id); the per-domain record references a hosted zone and is reconciled lazily (5s wait + one-shot deferred retry + resolve-on-read). external-dns, the `DNSEndpoint` CR, and the "active credential" machinery are removed.

**Tech Stack:** Go + Gin + GORM; `github.com/cloudflare/cloudflare-go` (v0), `github.com/aws/aws-sdk-go-v2/service/route53`, `google.golang.org/api/dns/v1`; Next.js + TypeScript (frontend-v2).

**Spec:** `docs/superpowers/specs/2026-10-05-dns-direct-provider-design.md` (read it alongside this plan).

**Repos:** backend-v2 `/Users/zufardhiyaulhaq/Documents/personal/github/backend-v2` (branch `feat/dns-direct-provider`, already created from `main`); frontend-v2 `/Users/zufardhiyaulhaq/Documents/personal/github/frontend-v2`; helm-chart `/Users/.../fastgateway.dev/helm-chart`; site `/Users/.../fastgateway-site/fastgateway.dev`.

**Starting point:** `main` carries the external-dns DNS feature (this session built it). This plan **modifies/removes** that code. Tasks are ordered so the tree compiles at each commit where practical; where a removal necessarily breaks the build until a later rewrite, the tasks are adjacent and the plan says so.

## Global Constraints
- **Providers (v1):** cloudflare, route53, google. **Azure is removed** (future follow-up).
- **Cloudflare SDK:** `github.com/cloudflare/cloudflare-go` **v0** (pin the latest v0.x; `proxied` records force TTL to auto — when proxied, send auto / ignore the user TTL).
- **Route53:** needs a `region` (default `us-east-1`); UPSERT via `ChangeResourceRecordSets`; DELETE needs the exact current RRSet.
- **Google:** `google.golang.org/api/dns/v1`; updates via atomic `Changes.Create`; `project` passed on every call.
- **Status enum:** `pending | ready | error`. **Ready on successful upsert, no read-back.** One `GetRecord` only on first write (clobber check, when `resolved_target == ""`).
- **Ownership:** `resolved_target != ""` ⇒ FastGateway owns the record. Refuse to clobber foreign records.
- **Apex + CNAME → error** (`hostname == zone.Name` and target is a hostname). A/AAAA at apex is fine.
- **Record references `hosted_zone_id`** (the zone carries credential + provider + `provider_zone_id`); no per-record credential.
- **Reconcile:** request-driven; 5s bounded wait on enable; one-shot deferred-retry goroutine (~3 min cap) per enable when pending; resolve-on-read (provider call only when pending or IP drifted); Refresh.
- **Deletes:** record Delete returns the provider error + keeps the row on real failure (NotFound ignored); domain delete best-effort + log.
- **In-use guards (409):** hosted zone in use by a record; credential in use by a hosted zone (plus the existing ACME-issuer guard).
- **Migration: squash** — edit `000046`, add a `dns_hosted_zones` migration, delete `000047`.
- **Commit trailers:** backend-v2 and frontend-v2 OMIT the Claude trailer; helm-chart and site INCLUDE `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
- backend gates: `go build ./...`, `go vet ./...`, `go test ./...`, `make mocks-check`, `make openapi-check` all clean. Frontend: `npx tsc --noEmit` 0 errors, `npx eslint` no new errors, `npm test`.

## Review Focus
1. **Gateway IP never resolves** → record stays `pending`, no provider write attempted, deferred-retry exits cleanly after its cap. (Task 11 tests)
2. **Foreign record exists at the hostname on first enable** → `error` (clobber), FastGateway never overwrites it. (Task 11 test)
3. **Apex hostname + CNAME target** → `error` at enable, no broken record written. (Task 11 test)
4. **Provider API failure on delete** → record Delete returns the error + keeps the row (retryable); NotFound still deletes the row. (Task 11 test)
5. **Hosted zone / credential deleted while in use** → 409, not a raw FK 500, so infra can't be pulled from under a live record. (Task 8 + Task 14 tests)

---

## Phase A — The DNSClient seam + provider SDK clients

### Task 1: Refocus `DNSProvider`, add `DNSClient`, drop Azure + external-dns bits

**Files:**
- Modify: `internal/dnsprovider/provider.go`
- Modify: `internal/dnsprovider/route53.go` (add optional `region`)
- Delete: `internal/dnsprovider/azure.go`
- Modify: `internal/dnsprovider/cloudflare.go`, `google.go` (drop `RenderSecret`/`ExternalDNSFlag`)
- Modify: `internal/dnsprovider/provider_test.go`

**Interfaces:**
- Produces: `DNSClient`, `Record` types; `DNSProvider` with `NewClient(cred) (DNSClient, error)` and no `RenderSecret`/`ExternalDNSFlag`/`SecretName`; registry of cloudflare/route53/google only.

- [ ] **Step 1: Write the failing test** — append to `provider_test.go`:
```go
func TestSupportedProviders_ThreeProviders(t *testing.T) {
	want := map[string]bool{"cloudflare": true, "route53": true, "google": true}
	if got := Supported(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Supported() = %v, want %v", got, want)
	}
}
func TestRoute53_RegionOptional(t *testing.T) {
	p, _ := Get("route53")
	// region is NOT required (defaults later), only the keys are
	if err := p.Validate(map[string]string{"accessKeyId": "AK", "secretAccessKey": "SK"}); err != nil {
		t.Fatalf("route53 without region should validate: %v", err)
	}
}
```
Remove the old `TestRenderSecret_PerProvider`/`TestExternalDNSFlag`/azure assertions from this file.

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/dnsprovider/ 2>&1 | head` → FAIL (azure still registered; RenderSecret/ExternalDNSFlag still referenced).

- [ ] **Step 3: Implement** — in `provider.go` replace the interface + header comment:
```go
// Package dnsprovider isolates everything provider-specific about DNS:
// credential validation and the SDK-backed DNS client (zone lookup + record CRUD).
package dnsprovider

import (
	"context"
	"fmt"
	"sort"
)

type DNSProvider interface {
	Type() string
	RequiredFields() []string
	Validate(cred map[string]string) error
	NewClient(cred map[string]string) (DNSClient, error)
}

// DNSClient talks to one provider account.
type DNSClient interface {
	FindZone(ctx context.Context, zoneName string) (providerZoneID string, found bool, err error)
	GetRecord(ctx context.Context, providerZoneID, name, recordType string) (rec Record, found bool, err error)
	UpsertRecord(ctx context.Context, providerZoneID string, r Record) error
	DeleteRecord(ctx context.Context, providerZoneID, name, recordType string) error
}

type Record struct {
	Name    string
	Type    string // A | AAAA | CNAME
	Target  string
	TTL     *int
	Proxied bool
}
```
Delete `SecretName`, `register`'s unchanged. Keep `Get`, `Supported`, `requireFields`.
Delete `internal/dnsprovider/azure.go`. In `cloudflare.go`/`google.go`/`route53.go`: delete `RenderSecret` and `ExternalDNSFlag` methods; add a `NewClient` method (implemented in Tasks 2–4 — for now, add a stub `func (cloudflare) NewClient(cred map[string]string) (DNSClient, error) { return nil, errors.New("not implemented") }` so the package compiles; Tasks 2–4 replace each stub). In `route53.go`, keep `RequiredFields()=["accessKeyId","secretAccessKey"]` (region stays optional, read in Task 3's client with a `us-east-1` default).

- [ ] **Step 4: Run tests** — `go test ./internal/dnsprovider/ -v` PASS; `go build ./internal/dnsprovider/` compiles. (The wider repo will NOT build yet — `dns_infra_service.go` uses `RenderSecret`; that's removed in Task 5. Do not run `go build ./...` here.)

- [ ] **Step 5: Commit**
```bash
git add internal/dnsprovider/
git commit -m "feat(dns): refocus DNSProvider to NewClient/DNSClient, drop azure + external-dns bits"
```

### Task 2: Cloudflare `DNSClient`

**Files:**
- Modify: `internal/dnsprovider/cloudflare.go`
- Test: `internal/dnsprovider/cloudflare_client_test.go`
- Modify: `go.mod`/`go.sum` (`go get github.com/cloudflare/cloudflare-go@latest` — pin the latest **v0.x**)

**Interfaces:**
- Consumes: `DNSClient`/`Record` (Task 1).
- Produces: `cloudflare.NewClient` returning a working `DNSClient`.

- [ ] **Step 1: Pin the SDK** — `go get github.com/cloudflare/cloudflare-go@latest` (confirm it resolves to a `v0.x` tag, not v4+). Record the exact version in the commit.

- [ ] **Step 2: Write the failing test** — exercise the client against the Cloudflare SDK's test transport by pointing it at an `httptest.Server`. cloudflare-go v0 accepts `cloudflare.BaseURL(url)` as an option, so:
```go
package dnsprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCloudflareClient_FindZoneAndUpsert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/zones") && r.Method == http.MethodGet && !strings.Contains(r.URL.Path, "dns_records"):
			w.Write([]byte(`{"success":true,"result":[{"id":"zone123","name":"example.com"}],"result_info":{"page":1,"total_pages":1}}`))
		case strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodGet:
			w.Write([]byte(`{"success":true,"result":[],"result_info":{"page":1,"total_pages":1}}`))
		case strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodPost:
			w.Write([]byte(`{"success":true,"result":{"id":"rec1"}}`))
		default:
			w.Write([]byte(`{"success":true,"result":{}}`))
		}
	}))
	defer srv.Close()

	c, err := cloudflare{}.newClientWithBaseURL(map[string]string{"apiToken": "tok"}, srv.URL)
	if err != nil { t.Fatal(err) }
	zid, found, err := c.FindZone(context.Background(), "example.com")
	if err != nil || !found || zid != "zone123" { t.Fatalf("FindZone=%q,%v,%v", zid, found, err) }
	if err := c.UpsertRecord(context.Background(), "zone123", Record{Name: "app.example.com", Type: "A", Target: "203.0.113.5"}); err != nil {
		t.Fatalf("UpsertRecord: %v", err)
	}
}
```
(The exact JSON envelope + the `newClientWithBaseURL` test seam are chosen so the test drives real SDK calls without a live account. If the pinned v0 version's option is named differently than `cloudflare.BaseURL`, adjust the seam to that version — the behavior asserted is unchanged.)

- [ ] **Step 3: Run to verify it fails** — `go test ./internal/dnsprovider/ -run TestCloudflareClient 2>&1 | head` → FAIL (NewClient stub / undefined seam).

- [ ] **Step 4: Implement** — in `cloudflare.go`, implement the client using cloudflare-go v0. Representative shape (align method names/params to the pinned v0's godoc):
```go
import (
	"context"
	"fmt"
	"strings"
	cf "github.com/cloudflare/cloudflare-go"
)

type cloudflareClient struct{ api *cf.API }

func (c cloudflare) NewClient(cred map[string]string) (DNSClient, error) {
	api, err := cf.NewWithAPIToken(cred["apiToken"])
	if err != nil { return nil, err }
	return &cloudflareClient{api: api}, nil
}
// test seam: same but with cf.BaseURL(baseURL)
func (c cloudflare) newClientWithBaseURL(cred map[string]string, baseURL string) (DNSClient, error) {
	api, err := cf.NewWithAPIToken(cred["apiToken"], cf.BaseURL(baseURL))
	if err != nil { return nil, err }
	return &cloudflareClient{api: api}, nil
}

func (c *cloudflareClient) FindZone(ctx context.Context, zoneName string) (string, bool, error) {
	zones, err := c.api.ListZones(ctx) // returns []cf.Zone with .Name/.ID
	if err != nil { return "", false, err }
	best := ""
	var bestID string
	for _, z := range zones {
		if (zoneName == z.Name || strings.HasSuffix(zoneName, "."+z.Name)) && len(z.Name) > len(best) {
			best, bestID = z.Name, z.ID
		}
	}
	if bestID == "" { return "", false, nil }
	return bestID, true, nil
}
```
Implement `GetRecord` (`api.ListDNSRecords(ctx, cf.ZoneIdentifier(zoneID), cf.ListDNSRecordsParams{Name:name, Type:recordType})` → map first match to `Record`), `UpsertRecord` (list by name+type; if found `api.UpdateDNSRecord`, else `api.CreateDNSRecord` with `Content`, `TTL`, `Proxied`; **when `r.Proxied`, do not set a custom TTL** — leave TTL unset so Cloudflare uses auto), and `DeleteRecord` (list to find the record id, then `api.DeleteDNSRecord`). **Note:** the exact v0 `ZoneIdentifier`/`ResourceContainer` + param-struct names vary by v0 minor; adjust to the pinned version's godoc. Keep all SDK calls inside this file.

- [ ] **Step 5: Run tests** — `go test ./internal/dnsprovider/ -run TestCloudflareClient -v` PASS.

- [ ] **Step 6: Commit**
```bash
git add internal/dnsprovider/cloudflare.go internal/dnsprovider/cloudflare_client_test.go go.mod go.sum
git commit -m "feat(dns): cloudflare DNSClient via cloudflare-go"
```

### Task 3: Route53 `DNSClient`

**Files:** Modify `internal/dnsprovider/route53.go`; Test `internal/dnsprovider/route53_client_test.go`; `go get github.com/aws/aws-sdk-go-v2/service/route53 github.com/aws/aws-sdk-go-v2/config github.com/aws/aws-sdk-go-v2/credentials`.

**Interfaces:** Produces `route53.NewClient`. The client reads `cred["region"]` (default `us-east-1`), builds a static-credentials config, and talks to Route53.

- [ ] **Step 1: Write the failing test** — point the SDK at an httptest server via a custom `BaseEndpoint`/`EndpointResolverV2`. Assert `FindZone` parses `ListHostedZones`, `UpsertRecord` issues a `ChangeResourceRecordSets` with `Action=UPSERT`, `DeleteRecord` issues `Action=DELETE`. (Use the aws-sdk-go-v2 `route53.Options{BaseEndpoint: aws.String(srv.URL), Credentials: ...}` constructor seam; the test server returns canned XML for `ListHostedZones`/`ChangeResourceRecordSets`.)

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/dnsprovider/ -run TestRoute53Client` → FAIL.

- [ ] **Step 3: Implement**:
```go
func (route53P) NewClient(cred map[string]string) (DNSClient, error) {
	region := cred["region"]; if region == "" { region = "us-east-1" }
	creds := credentials.NewStaticCredentialsProvider(cred["accessKeyId"], cred["secretAccessKey"], "")
	cfg := aws.Config{Region: region, Credentials: creds}
	return &route53Client{api: route53.NewFromConfig(cfg)}, nil
}
```
`FindZone`: `ListHostedZones` (paginate via `Marker`), pick the longest `.Name` that is a suffix of the hostname (Route53 zone names have a trailing dot — strip it when comparing); return the hosted zone id (strip the `/hostedzone/` prefix). `GetRecord`: `ListResourceRecordSets(StartRecordName, StartRecordType, MaxItems=1)` and match name+type. `UpsertRecord`: `ChangeResourceRecordSets` with `ChangeBatch{Changes:[{Action: UPSERT, ResourceRecordSet:{Name, Type, TTL, ResourceRecords:[{Value: target}]}}]}` (Route53 ignores `Proxied`). `DeleteRecord`: first `GetRecord` to get exact values, then `ChangeResourceRecordSets` `Action=DELETE` with those exact values (Route53 requires them); if the record isn't present, treat as success (NotFound-equivalent). (Rename the provider struct to avoid clashing with the package import — e.g. the struct stays `route53` and import the SDK as `r53 "github.com/aws/aws-sdk-go-v2/service/route53"`.)

- [ ] **Step 4: Run tests** — PASS.
- [ ] **Step 5: Commit** — `feat(dns): route53 DNSClient via aws-sdk-go-v2 (region default us-east-1)`.

### Task 4: Google Cloud DNS `DNSClient`

**Files:** Modify `internal/dnsprovider/google.go`; Test `internal/dnsprovider/google_client_test.go`; `go get google.golang.org/api/dns/v1 google.golang.org/api/option`.

**Interfaces:** Produces `google.NewClient`. Auth via `option.WithCredentialsJSON([]byte(cred["serviceAccountKey"]))`; `project = cred["project"]` on every call.

- [ ] **Step 1: Write the failing test** — construct the `dns.Service` against an httptest server with `option.WithEndpoint(srv.URL)` + `option.WithoutAuthentication()` (so no real SA needed in the test); assert `FindZone` parses `ManagedZones.List`, `UpsertRecord` issues a `Changes.Create` with the right additions/deletions, `DeleteRecord` a deletion.

- [ ] **Step 2: Run to verify it fails** → FAIL.

- [ ] **Step 3: Implement**:
```go
func (google) NewClient(cred map[string]string) (DNSClient, error) {
	svc, err := dns.NewService(context.Background(), option.WithCredentialsJSON([]byte(cred["serviceAccountKey"])))
	if err != nil { return nil, err }
	return &googleClient{svc: svc, project: cred["project"]}, nil
}
```
`FindZone`: `svc.ManagedZones.List(project).Do()`, longest-suffix match on `.DnsName` (Google zone DnsName has a trailing dot). `GetRecord`: `svc.ResourceRecordSets.List(project, managedZone).Name(fqdn).Type(recordType).Do()`. `UpsertRecord`: atomic `svc.Changes.Create(project, managedZone, &dns.Change{Additions:[rrset], Deletions:[existing if any]}).Do()` (build the rrset with `Name=fqdn+".", Type, Ttl, Rrdatas:[target]`; Google ignores `Proxied`). `DeleteRecord`: `Changes.Create` with `Deletions:[existing]`; absent → success. (Google requires a trailing dot on record names — append `.` to the hostname.) Provide a `newClientWithService(svc, project)` test seam.

- [ ] **Step 4: Run tests** — PASS.
- [ ] **Step 5: Commit** — `feat(dns): google cloud dns DNSClient via dns/v1`.

---

## Phase B — Remove external-dns + active-credential machinery, reshape schema

### Task 5: Remove external-dns code + active-credential machinery

**Files:**
- Delete: `internal/kubernetes/externaldns.go`, `internal/kubernetes/externaldns_test.go`, `internal/kubernetes/testdata/golden/externaldns/`
- Modify: `internal/kubernetes/gvr.go` (remove `DNSEndpointGVR`)
- Delete: `internal/services/dns_infra_service.go`, `internal/services/dns_infra_service_test.go`
- Delete: `internal/handlers/dns_active_credential_handler.go`
- Modify: `internal/services/system_settings_service.go` (remove `GetActiveDNSCredentialID`/`SetActiveDNSCredentialID` + the `ActiveDNSCredentialReader` interface if it lives here), `internal/services/system_settings_service_test.go`
- Modify: `internal/models/system_settings.go` (remove `ActiveDNSCredentialID` field)
- Modify: `cmd/server/main.go` (remove DNSInfraService + DNSActiveCredentialHandler construction + routes + `Dependencies` fields)
- Modify: `internal/services/dns_credential_service.go` (remove `ErrDNSCredentialIsActive` + the active-credential check + the `ActiveDNSCredentialReader`/`Settings` dep added for it)

**Interfaces:** Produces a tree with no external-dns / active-credential references. NOTE: `dns_record_service.go` still references the removed `DNSEndpoint`/active-credential symbols — it is rewritten in Task 11; to keep this task's commit buildable, this task also **stubs** `dns_record_service.go`'s reconcile to a no-op that sets `pending` (temporary) OR Tasks 5+10+11 are implemented as one branch before the build is run green. Recommended: do Task 5, 10, 11 back-to-back; run `go build ./...` green only after Task 11. Mark this task DONE_WITH_CONCERNS if the build is red pending Task 11.

- [ ] **Step 1** — delete the files/symbols listed above; `grep -rn "externaldns\|DNSEndpoint\|ActiveDNSCredential\|RenderSecret\|dns_infra\|ErrDNSCredentialIsActive" internal cmd --include=*.go | grep -v _test` to find every reference and remove it.
- [ ] **Step 2** — `go build ./...` will fail only in `dns_record_service.go` (DNSEndpoint usage) — that's expected; everything else must compile. Confirm the ONLY remaining errors are in `dns_record_service.go`.
- [ ] **Step 3** — `make mocks` (regen mocks after removing the interfaces); confirm `make mocks-check` is clean except for anything referencing the record service (Task 11).
- [ ] **Step 4: Commit** — `refactor(dns): remove external-dns + active-credential machinery` (expect record service rewrite to follow immediately).

### Task 6: Squash migration + reshape models (`DomainDNSRecord` + new `DNSHostedZone`)

**Files:**
- Modify: `migrations/000046_add_domain_dns_records.up.sql` (final schema), `.down.sql`
- Create: `migrations/000048_add_dns_hosted_zones.up.sql` / `.down.sql` (next free number after confirming; `000047` is being deleted but keep numbering monotonic — use `000048`)
- Delete: `migrations/000047_add_system_settings_active_dns_credential.up.sql` / `.down.sql`
- Modify: `internal/models/domain_dns_record.go`
- Create: `internal/models/dns_hosted_zone.go`
- Test: `internal/models/dns_hosted_zone_test.go`

- [ ] **Step 1: Rewrite `000046.up.sql`** to the final record schema:
```sql
CREATE TABLE domain_dns_records (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id UUID NOT NULL UNIQUE REFERENCES domains(id) ON DELETE CASCADE,
    hosted_zone_id UUID NOT NULL REFERENCES dns_hosted_zones(id),
    record_type VARCHAR(16) NOT NULL DEFAULT 'auto',
    ttl INTEGER,
    proxied BOOLEAN NOT NULL DEFAULT FALSE,
    resolved_target TEXT,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    status_message TEXT,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_domain_dns_records_zone ON domain_dns_records(hosted_zone_id);
```
NOTE: `domain_dns_records` now references `dns_hosted_zones`, so the hosted-zones migration must run **first**. Make the hosted-zones table migration `000046` and the records table `000047`? No — simplest: put `CREATE TABLE dns_hosted_zones` in `000046` **above** `CREATE TABLE domain_dns_records` (one migration file, both tables), and delete the old `000047`. **Decision: fold both tables into `000046.up.sql`** (hosted zones first, then records), so FK order is satisfied in one migration. Update `000046.down.sql` to drop both (records first).

`dns_hosted_zones`:
```sql
CREATE TABLE dns_hosted_zones (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    provider_credential_id UUID NOT NULL REFERENCES dns_provider_credentials(id),
    provider_zone_id TEXT,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    status_message TEXT,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_dns_hosted_zones_credential ON dns_hosted_zones(provider_credential_id);
```
Delete `migrations/000047_*` and do NOT create `000048` (both tables are in `000046`).

- [ ] **Step 2: Reshape `domain_dns_record.go`** — replace `ProviderCredentialID uuid.UUID` with `HostedZoneID uuid.UUID gorm:"type:uuid;not null;index" json:"hostedZoneId"`; remove `EndpointName` if still present. Keep the `DNSRecordStatus`/`DNSRecordType` enums. Remove `DNSRecordStatusSyncing` if present (enum is pending/ready/error) — update any references.

- [ ] **Step 3: Create `dns_hosted_zone.go`**:
```go
package models

import ("time"; "github.com/google/uuid")

type DNSZoneStatus string
const ( DNSZoneStatusReady DNSZoneStatus = "ready"; DNSZoneStatusError DNSZoneStatus = "error"; DNSZoneStatusPending DNSZoneStatus = "pending" )

type DNSHostedZone struct {
	ID                   uuid.UUID     `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Name                 string        `gorm:"not null" json:"name"`
	ProviderCredentialID uuid.UUID     `gorm:"type:uuid;not null;index" json:"providerCredentialId"`
	ProviderZoneID       string        `gorm:"column:provider_zone_id" json:"-"`
	Status               DNSZoneStatus `gorm:"not null;default:'pending'" json:"status"`
	StatusMessage        string        `gorm:"column:status_message" json:"statusMessage,omitempty"`
	CreatedBy            uuid.UUID     `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt            time.Time     `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt            time.Time     `gorm:"not null;default:now()" json:"updatedAt"`
}
func (DNSHostedZone) TableName() string { return "dns_hosted_zones" }
```

- [ ] **Step 4: Test** — `dns_hosted_zone_test.go` asserts `TableName()` and the status constants; update `domain_dns_record_test.go` if it referenced the removed field/enum. Run `go test ./internal/models/ -run 'DNSHostedZone|DomainDNSRecord|DNSRecordStatus'`.

- [ ] **Step 5: Commit** — `feat(dns): squash migration to hosted-zone schema + models`.

### Task 7: `DNSHostedZone` repository

**Files:** Create `internal/repository/dns_hosted_zone_repository.go`; Modify `internal/repository/interfaces.go`; Modify `internal/repository/domain_dns_record_repository.go` (replace `CountByCredential` with `CountByZone`).

**Interfaces:** Produces `DNSHostedZoneRepository` + `DNSHostedZoneRepositoryInterface` (`Create`, `GetByID`, `List`, `Update`, `Delete`, `CountByCredential(credID) int64`); `DomainDNSRecordRepository.CountByZone(zoneID) (int64, error)`.

- [ ] **Step 1: Implement the repo** (mirror `dns_provider_credential_repository.go`):
```go
type DNSHostedZoneRepository struct{ db *gorm.DB }
func NewDNSHostedZoneRepository(db *gorm.DB) *DNSHostedZoneRepository { return &DNSHostedZoneRepository{db: db} }
func (r *DNSHostedZoneRepository) Create(z *models.DNSHostedZone) error { return r.db.Create(z).Error }
func (r *DNSHostedZoneRepository) GetByID(id uuid.UUID) (*models.DNSHostedZone, error) { var z models.DNSHostedZone; if err := r.db.First(&z, "id = ?", id).Error; err != nil { return nil, err }; return &z, nil }
func (r *DNSHostedZoneRepository) List() ([]models.DNSHostedZone, error) { var zs []models.DNSHostedZone; return zs, r.db.Order("name").Find(&zs).Error }
func (r *DNSHostedZoneRepository) Update(z *models.DNSHostedZone) error { return r.db.Save(z).Error }
func (r *DNSHostedZoneRepository) Delete(id uuid.UUID) error { return r.db.Delete(&models.DNSHostedZone{}, "id = ?", id).Error }
func (r *DNSHostedZoneRepository) CountByCredential(credID uuid.UUID) (int64, error) { var n int64; return n, r.db.Model(&models.DNSHostedZone{}).Where("provider_credential_id = ?", credID).Count(&n).Error }
```
- [ ] **Step 2** — in `domain_dns_record_repository.go` replace `CountByCredential` with:
```go
func (r *DomainDNSRecordRepository) CountByZone(zoneID uuid.UUID) (int64, error) { var n int64; return n, r.db.Model(&models.DomainDNSRecord{}).Where("hosted_zone_id = ?", zoneID).Count(&n).Error }
```
- [ ] **Step 3** — add both interfaces (`DNSHostedZoneRepositoryInterface`, update `DomainDNSRecordRepositoryInterface`: `CountByZone` replaces `CountByCredential`) + compile-time assertions in `interfaces.go`.
- [ ] **Step 4** — `go build ./internal/repository/ && go vet ./internal/repository/`; run the repo round-trip test if one exists (gated by `requirePostgres`).
- [ ] **Step 5: Commit** — `feat(dns): hosted-zone repository + record CountByZone`.

### Task 8: `DNSHostedZone` service (register/validate/list/delete-guard)

**Files:** Create `internal/services/dns_hosted_zone_service.go`; Test `internal/services/dns_hosted_zone_service_test.go`.

**Interfaces:**
- Consumes: `DNSHostedZoneRepositoryInterface`, `DomainDNSRecordRepositoryInterface.CountByZone`, `DNSCredentialService.DecryptedCredentials`, `dnsprovider.Get(...).NewClient(...).FindZone`.
- Produces: `DNSHostedZoneService` with `Create(name string, credID, createdBy uuid.UUID) (*models.DNSHostedZone, error)`, `List()`, `GetByID`, `Delete(id) error` (409 guard via `ErrDNSHostedZoneInUse`). `var ErrDNSHostedZoneInUse`, `var ErrHostedZoneNotFound`.

- [ ] **Step 1: Write the failing tests** (fakes for the three deps):
```go
func TestHostedZone_Create_ValidatesAndCachesZoneID(t *testing.T) {
	svc, d := newTestHostedZoneService(t)
	d.findZoneID, d.findZoneFound = "cfzone1", true
	z, err := svc.Create("example.com", d.credID, d.userID)
	if err != nil { t.Fatal(err) }
	if z.ProviderZoneID != "cfzone1" || z.Status != models.DNSZoneStatusReady { t.Fatalf("got %+v", z) }
}
func TestHostedZone_Create_ZoneNotFound_Errors(t *testing.T) {
	svc, d := newTestHostedZoneService(t); d.findZoneFound = false
	z, err := svc.Create("missing.com", d.credID, d.userID)
	if err == nil && z.Status != models.DNSZoneStatusError { t.Fatal("expected zone-not-found error/status") }
}
func TestHostedZone_Delete_InUse_Rejected(t *testing.T) {
	svc, d := newTestHostedZoneService(t); d.recordsForZone = 1
	if err := svc.Delete(d.zoneID); !errors.Is(err, ErrDNSHostedZoneInUse) { t.Fatalf("want ErrDNSHostedZoneInUse, got %v", err) }
}
```
- [ ] **Step 2: Run to verify it fails** → FAIL.
- [ ] **Step 3: Implement** — `Create`: validate the credential exists + provider supported; `DecryptedCredentials(credID)` → `prov.NewClient(creds)` → `FindZone(name)`; found → store `ProviderZoneID` + `ready`; not found → persist `error` status with a message (or return an error — choose: persist row with `error` status so the user sees it on the list, and return the created zone). `Delete`: `recordRepo.CountByZone(id) > 0` → `ErrDNSHostedZoneInUse`; else repo delete. Define the sentinels.
- [ ] **Step 4: Run tests** — PASS (covers Review Focus #5).
- [ ] **Step 5: Commit** — `feat(dns): hosted-zone service with validation + in-use guard`.

### Task 9: `DNSHostedZone` handler + routes + OpenAPI

**Files:** Create `internal/handlers/dns_hosted_zone_handler.go`; Modify `internal/handlers/service_interfaces.go`, `cmd/server/main.go`; OpenAPI `docs/openapi/paths/dns-zones.yaml` + `schemas/dns-zone.yaml` + root refs; Test `internal/handlers/dns_hosted_zone_handler_test.go`.

**Interfaces:** owner-only `/dns/zones` (GET list, POST create `{name, providerCredentialId}`, GET `/:id`, DELETE `/:id`), mirroring the `/dns/credentials` handler + `RequireRole("owner")` group. Map `ErrDNSHostedZoneInUse` → 409, not-found → 404.

- [ ] **Step 1** — write the handler test (gin + mock service): POST 201, DELETE 409 on in-use, GET list, 403 without owner.
- [ ] **Step 2** — implement the handler mirroring `dns_credential_handler.go`; response DTO excludes `provider_zone_id` is already `json:"-"`.
- [ ] **Step 3** — register the `/dns/zones` owner group in `main.go` next to `/dns/credentials` (unconditional — DB + outbound HTTP only, NOT cluster-gated); add the `Dependencies` field; construct `dnsHostedZoneService` + `dnsHostedZoneHandler` (needs the repos + `dnsCredentialService` — all available early/unconditional).
- [ ] **Step 4** — add OpenAPI paths/schema; `make openapi` + `make openapi-check`.
- [ ] **Step 5** — `go test ./internal/handlers/ -run HostedZone -v`; commit `feat(dns): hosted-zone API endpoints`.

---

## Phase C — Record service rewrite + wiring

### Task 10: Record repository + model enum cleanup (consumed by Task 11)

Folded into Task 6/7 (model has `HostedZoneID`; repo has `CountByZone`). This task number is intentionally merged — proceed to Task 11. *(If reading out of order: ensure `DomainDNSRecord.HostedZoneID` exists and `DomainDNSRecordRepository` has Create/GetByDomainID/Update/DeleteByDomainID/CountByZone before Task 11.)*

### Task 11: Rewrite `DNSRecordService` (direct-provider reconcile + deferred retry)

**Files:** Modify `internal/services/dns_record_service.go`; Modify `internal/services/dns_record_service_test.go`.

**Interfaces:**
- Consumes: `DomainDNSRecordRepositoryInterface`, `DomainRepositoryInterface.GetByID`, `DNSHostedZoneService`/repo (`GetByID`), `DNSCredentialService.DecryptedCredentials`, `dnsprovider.Get(...).NewClient(...)`, `resolveGatewayAddress`/`recordTypeForAddress` (gateway_address.go, unchanged), `CertInfraApplier` for the Gateway-status read (unchanged).
- Produces: `DNSRecordInput{ HostedZoneID *uuid.UUID; RecordType models.DNSRecordType; TTL *int; Proxied bool }`; `Enable`/`Get`/`Update`/`Delete`/`Refresh` (same names); `var ErrInvalidRecordType`, `ErrDNSRecordExists`, `ErrNoHostedZone`, `ErrHostedZoneMismatch`, `ErrApexCNAME`, `ErrRecordClobber`. Remove `ErrNoActiveDNSCredential`/`ErrCredentialNotActive`/`resolveActiveCredential`/`endpointMatches`.

- [ ] **Step 1: Write the failing tests** (fake `DNSClient` + fake gateway resolver). Cover the Review Focus:
```go
func TestEnable_RequiresHostedZone(t *testing.T) { /* no hostedZoneId -> ErrNoHostedZone */ }
func TestEnable_NoGatewayAddress_PendingNoWrite(t *testing.T) { /* IP unresolved -> pending, client.Upsert never called (Review Focus #1) */ }
func TestEnable_ForeignRecord_ClobberError(t *testing.T) { /* GetRecord found + resolved_target=="" -> error, no Upsert (Review Focus #2) */ }
func TestEnable_ApexCNAME_Error(t *testing.T) { /* hostname==zone.Name && target is hostname -> error (Review Focus #3) */ }
func TestEnable_WithAddress_UpsertsReady(t *testing.T) { /* IP ready -> Upsert called, status ready, resolved_target set */ }
func TestDelete_ProviderFails_KeepsRow(t *testing.T) { /* client.Delete returns non-NotFound -> Delete returns err, row kept (Review Focus #4) */ }
func TestDelete_NotFound_DeletesRow(t *testing.T) { /* client.Delete returns provider-notfound -> row deleted */ }
```
- [ ] **Step 2: Run to verify it fails** → FAIL.
- [ ] **Step 3: Implement** — the reconcile core:
```go
func (s *DNSRecordService) reconcile(rec *models.DomainDNSRecord) {
	setErr := func(msg string) { rec.Status = models.DNSRecordStatusError; rec.StatusMessage = msg; _ = s.repo.Update(rec) }
	domain, err := s.domainRepo.GetByID(rec.DomainID); if err != nil { setErr("domain not found"); return }
	zone, err := s.zoneRepo.GetByID(rec.HostedZoneID); if err != nil { setErr("hosted zone not found"); return }
	// resolve gateway address
	gw, err := s.cp.Get(context.Background(), kubernetes.GatewayGVR, domain.K8sGatewayName, true)
	if err != nil { rec.Status = models.DNSRecordStatusPending; rec.StatusMessage = "waiting for the gateway"; _ = s.repo.Update(rec); return }
	addr, ok := resolveGatewayAddress(gw)
	if !ok { rec.Status = models.DNSRecordStatusPending; rec.StatusMessage = "waiting for the gateway load-balancer address"; _ = s.repo.Update(rec); return }
	rt, err := recordTypeForAddress(addr, rec.RecordType); if err != nil { setErr(err.Error()); return }
	// apex + CNAME guard
	if rt == models.DNSRecordTypeCNAME && domain.Hostname == zone.Name { setErr("CNAME at a zone apex isn't supported; use an IP gateway or a subdomain"); return }
	providerType, creds, err := s.creds.DecryptedCredentials(zone.ProviderCredentialID); if err != nil { setErr("credential error"); return }
	prov, ok2 := dnsprovider.Get(providerType); if !ok2 { setErr("unsupported provider"); return }
	client, err := prov.NewClient(creds); if err != nil { setErr(err.Error()); return }
	ctx := context.Background()
	// clobber check: first write only
	if rec.ResolvedTarget == "" {
		if _, found, gerr := client.GetRecord(ctx, zone.ProviderZoneID, domain.Hostname, string(rt)); gerr != nil { setErr(gerr.Error()); return } else if found {
			setErr("a record already exists for " + domain.Hostname + " not managed by FastGateway"); return
		}
	}
	proxied := rec.Proxied && providerType == "cloudflare"
	ttl := rec.TTL; if proxied { ttl = nil } // cloudflare forces auto when proxied
	if err := client.UpsertRecord(ctx, zone.ProviderZoneID, dnsprovider.Record{Name: domain.Hostname, Type: string(rt), Target: addr.Value, TTL: ttl, Proxied: proxied}); err != nil { setErr(err.Error()); return }
	rec.ResolvedTarget = addr.Value; rec.Status = models.DNSRecordStatusReady; rec.StatusMessage = "record written"; _ = s.repo.Update(rec)
}
```
`Enable`: require `in.HostedZoneID` (→ `ErrNoHostedZone`); validate record type; create the row (status pending) with `HostedZoneID`; run `reconcile` with a **5s bounded wait** on the gateway address (poll inside enable ~5s); if still pending afterward, launch the deferred-retry goroutine:
```go
func (s *DNSRecordService) deferredRetry(domainID uuid.UUID) {
	go func() {
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			time.Sleep(10 * time.Second)
			rec, err := s.repo.GetByDomainID(domainID); if err != nil { return }
			if rec.Status != models.DNSRecordStatusPending { return }
			s.reconcile(rec)
			if rec.Status == models.DNSRecordStatusReady || rec.Status == models.DNSRecordStatusError { return }
		}
	}()
}
```
`Get`/`Refresh`: reconcile **only when** `status==pending` OR (resolved gateway IP != `resolved_target`) — otherwise return the cached row without a provider call. `Update`: re-upsert (validate type). `Delete`: resolve the zone + client; `err := client.DeleteRecord(...)`; if `err != nil && !isProviderNotFound(err)` → return err + keep row; else delete the row. (Define `isProviderNotFound` per-provider or treat each client's "absent" as nil — simplest: each `DeleteRecord` returns nil when the record is absent, so `Delete` just returns the error as-is and keeps the row on a real error.)
Add the `zoneRepo`/`creds` deps + nil-panic constructor; remove the active-credential deps.

- [ ] **Step 4: Run tests** — `go test ./internal/services/ -run 'TestEnable|TestDelete|TestUpdate|DNSRecord' -v` PASS; now `go build ./...` + `go test ./...` + `make mocks-check` green (Task 5's build break is resolved here).
- [ ] **Step 5: Commit** — `feat(dns): rewrite record service for direct-provider writes + deferred retry`.

### Task 12: Record handler + domain auto-create + wiring + OpenAPI

**Files:** Modify `internal/handlers/dns_record_handler.go`, `internal/services/domain_service.go` (CreateDomainInput.DNS), `cmd/server/main.go`, OpenAPI `schemas/dns-record.yaml` + `schemas/domain.yaml`; Tests in the handler + domain service test files.

- [ ] **Step 1** — handler request becomes `{ hostedZoneId *string, recordType string, ttl *int, proxied bool }`; map to `DNSRecordInput`; error mapping: `ErrNoHostedZone`/`ErrInvalidRecordType`/apex/clobber → 400, provider errors → 502, `gorm.ErrRecordNotFound` → 404, `ErrDNSRecordExists` → 409. Update the handler test for the new field + codes.
- [ ] **Step 2** — `CreateDomainInput.DNS` becomes `{ Enabled bool; HostedZoneID string; RecordType string; TTL *int; Proxied bool }`; the Create-tail best-effort enable passes `HostedZoneID`. Update the domain-create test.
- [ ] **Step 3** — `main.go`: construct the record service with `zoneRepo`/`creds`/`domainRepo`/`controlPlane`; `SetDNSRecords(dnsRecordService)` on the domain service; keep the record routes; remove the active-credential routes. (The record service still needs the in-cluster `controlPlane` for Gateway status — keep it inside the in-cluster nil-guard block as before.)
- [ ] **Step 4** — OpenAPI: record schema `hostedZoneId` replaces `providerCredentialId`; domain-create `dns` block updated; `make openapi` + `make openapi-check`.
- [ ] **Step 5** — `go test ./... && make openapi-check`; commit `feat(dns): record API + domain auto-create use hosted zones`.

### Task 13: Credential service + form: Route53 region; credential-in-use-by-zone guard

**Files:** Modify `internal/services/dns_credential_service.go`, its test.

- [ ] **Step 1** — `DNSCredentialService.Delete`: keep the ACME-issuer check; replace the record check with `hostedZoneRepo.CountByCredential(id) > 0 → ErrDNSCredentialInUse`. Inject the hosted-zone repo. Add a test: deleting a credential used by a zone → `ErrDNSCredentialInUse` (409 in handler).
- [ ] **Step 2** — ensure `route53` provider `Validate` does not require `region` (it's optional; the client defaults it). No code change if Task 1 already left `region` out of `RequiredFields`. Add a test asserting a route53 credential with a `region` key validates and round-trips (encryption covers arbitrary keys).
- [ ] **Step 3** — `go test ./internal/services/ -run DNSCredential`; `make mocks`/`mocks-check`; commit `feat(dns): credential in-use-by-zone guard + route53 region passthrough`.

---

## Phase D — Helm + docs

### Task 14: Helm — remove the external-dns RBAC + value

**Repo:** helm-chart. **Files:** `fastgateway/templates/rbac.yaml`, `fastgateway/values.yaml`, READMEs.
- [ ] Remove the `{{- if .Values.dns.enabled }}` DNSEndpoint block from `rbac.yaml` and the `dns:` value from `values.yaml` (the backend needs no new k8s perms now). `helm lint` + `helm template` show no `dnsendpoints`. `make readme`. Commit WITH the Claude trailer.

### Task 15: Docs — rewrite for direct-provider + hosted zones

**Repo:** site. **Files:** `docs/concepts/dns-management.md`, `docs/getting-started/installation.md`.
- [ ] Rewrite the DNS page for the new flow (register credential → register hosted zone → enable DNS per domain; three providers; no external-dns). Remove the external-dns prerequisite from installation. `npm run build` succeeds. Commit WITH the Claude trailer.

---

## Phase E — Frontend (repo: frontend-v2, new branch `feat/dns-direct-provider`)

### Task 16: Frontend types + api (zones + record input + remove active-credential)

**Files:** Create `src/lib/api/dns-zones.ts`; Modify `src/types/index.ts`, `src/lib/api/dns-records.ts` (input), `src/lib/api/dns-credentials.ts` (remove active-credential methods); Tests `dns-zones.test.ts`.
- [ ] Add `DNSHostedZone` type + `dnsZonesApi` (list/create/get/remove on `/dns/zones`); change `DNSRecordInput` to `{ hostedZoneId?: string; recordType?; ttl?; proxied? }`; remove `getActiveCredential`/`setActiveCredential`; add colocated tests (mirror `dns-records.test.ts`). tsc/eslint/jest.

### Task 17: Frontend — Hosted Zones admin page + credential region field

**Files:** Create `src/app/(authenticated)/dns-zones/page.tsx` (list + create modal: zone name + credential `<Select>`); Modify `src/app/(authenticated)/dns-credentials/page.tsx` (add optional `region` to the route53 field map); sidebar nav.
- [ ] Mirror the dns-credentials admin page structure. tsc/eslint/build.

### Task 18: Frontend — domain DNS section (view + edit) uses hosted-zone picker

**Files:** Modify `.../domains/[domainId]/page.tsx`, `.../settings/page.tsx`.
- [ ] Replace the credential picker with a **hosted-zone picker** (`dnsZonesApi.list()` filtered to zones whose `name` is a suffix of `domain.hostname`, default longest-suffix, overridable); show provider/zone on the read-only view; **`proxied` only for Cloudflare zones**; **TTL greyed out when proxied**; status badge `pending/ready/error`. tsc/eslint/build.

### Task 19: Frontend — create wizard hosted-zone picker

**Files:** Modify `.../domains/create/page.tsx`.
- [ ] DNS block: hosted-zone picker (suffix-filtered on the typed hostname, longest default); payload `dns:{ enabled, hostedZoneId, recordType, ttl, proxied }` when enabled. tsc/eslint/build.

---

## Self-Review

**1. Spec coverage:** §1 flow → Tasks 6–9 (zones), 11–12 (records), 16–19 (UI). §3 write path → Tasks 1–4, 11. §4 hosted zones → 6–9, 17. §5 credentials → 1 (region), 13. §6 reconcile → 11. §7 data model/squash → 6. §8 removed/added → 5, 14. §9 frontend → 16–19. §10 errors → 8, 11, 12, 13. §11 testing → every task. §12 futures → out of scope. No gaps.

**2. Placeholder scan:** No "TBD"/"handle errors"/"similar to Task N". SDK method names carry a "pin the version + align to its godoc" step (Tasks 2–4) because the exact v0/SDK signatures must be verified at the pinned version — the behavior is pinned by the tests, which is the real contract. Task 5's intentional build-break-until-Task-11 is called out explicitly (do 5→6→7→11 back-to-back; green at 11).

**3. Type consistency:** `HostedZoneID` (model, Task 6) = `hosted_zone_id` (migration) = `DNSRecordInput.HostedZoneID` (Task 11) = `hostedZoneId` (handler/OpenAPI/frontend). `DNSClient`/`Record` (Task 1) consumed verbatim in Tasks 2–4, 8, 11. `CountByZone` (Task 7) consumed in Task 8. `ProviderZoneID` on the zone (Task 6) consumed in Task 11's reconcile. Status enum `pending/ready/error` consistent; `syncing` removed.

**4. Review Focus:** all five pinned — #1 (no-IP→pending/no-write) Task 11 `TestEnable_NoGatewayAddress_PendingNoWrite`; #2 (clobber) Task 11 `TestEnable_ForeignRecord_ClobberError`; #3 (apex+CNAME) Task 11 `TestEnable_ApexCNAME_Error`; #4 (delete-keeps-row) Task 11 `TestDelete_ProviderFails_KeepsRow`; #5 (in-use guards) Task 8 `TestHostedZone_Delete_InUse_Rejected` + Task 13 credential-in-use test.
