# DNS Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a FastGateway domain own one managed DNS record that points its hostname at the gateway's external address, created automatically (opt-in) at domain creation, written to the DNS provider by external-dns, across the four major providers.

**Architecture:** Mirror managed certificates. A first-class `domain_dns_records` row (1:1 with a domain) is reconciled to a `DNSEndpoint` custom resource (`externaldns.k8s.io/v1alpha1`) in `fastgateway-system`; external-dns (operator-installed) writes the real record. The target is the domain's Gateway `status.addresses`, resolved lazily (on apply/read) because there is no backend reconciler. Provider specifics (credential fields, the external-dns Secret, the `--provider` flag) live behind one `DNSProvider` interface. The existing `DNSProviderCredential` model + `DNSCredentialService` are reused; one owner-chosen "active DNS credential" anchors which provider external-dns runs.

**Tech Stack:** Go 1.x + Gin + GORM + `client-go` dynamic client (backend-v2); Next.js + TypeScript + Tailwind (frontend-v2); Helm (helm-chart); Docusaurus (fastgateway-site).

**Spec:** `docs/superpowers/specs/2026-10-04-dns-management-design.md` (backend-v2). Read it alongside this plan.

**Repos (absolute roots):**
- backend-v2: `/Users/zufardhiyaulhaq/Documents/personal/github/backend-v2`
- frontend-v2: `/Users/zufardhiyaulhaq/Documents/personal/github/frontend-v2`
- helm-chart: `/Users/zufardhiyaulhaq/Documents/personal/github/fastgateway.dev/helm-chart`
- site: `/Users/zufardhiyaulhaq/Documents/personal/github/fastgateway-site/fastgateway.dev`

## Global Constraints

- **One managed record per domain**, hostname = the domain's hostname (never stored). Record type auto-chosen: IPv4→`A`, IPv6→`AAAA`, hostname→`CNAME`. (spec §3.4)
- **Providers (v1):** `cloudflare`, `route53`, `google`, `azure`. One active provider per cluster. (spec §2, §4)
- **Delegation only:** FastGateway never calls a DNS provider API. It creates `DNSEndpoint` CRs; external-dns writes records. (spec §3.2)
- **external-dns Secret name (fixed):** `fgw-externaldns-credentials` in namespace `fastgateway-system`. (spec §4.2)
- **DNSEndpoint naming:** `dns-<recordID>`, namespace `fastgateway-system`, group/version/resource `externaldns.k8s.io` / `v1alpha1` / `dnsendpoints`. (spec §3.2)
- **Status enum:** `pending` | `syncing` | `ready` | `error`. Readiness rule (v1, deterministic, no external-dns writeback dependency): `pending` = enabled but no resolved target yet; `syncing` = DNSEndpoint applied but its targets don't yet match desired; `ready` = DNSEndpoint present with targets equal to desired; `error` = validation/apply failure. (spec §3.5, §8 — the stricter provider-verification readiness is an explicit future enhancement, spec §12.4)
- **Active DNS credential** is an owner-set system setting keyed `dns.active_credential_id`; a record's `providerCredentialId` defaults to it and in v1 must equal it. (spec §4.3, §6)
- **Cluster-gated:** record features require the in-cluster dynamic client; follow the managed-cert nil-guard (`if deps.DNSRecordHandler != nil`). Credential CRUD stays unconditional (DB-only). (digest §8)
- **Secrets never serialized:** `DNSCredentialData` keeps `json:"-"`; credential values individually encrypted via `crypto.Encrypt(v, cfg.EncryptionKey)`. (digest §1, §5)
- **Permissions:** record endpoints require `canManageDomains`; credential + active-credential endpoints require role `owner`. (spec §6)
- **OpenAPI:** every API-surface change ends with `make openapi` + commit `cmd/server/openapi.yaml` (CI runs `make openapi-check`). (digest §10)
- **Frontend gates:** `npx tsc --noEmit` must stay at 0 errors; `npx eslint .` has no `--max-warnings`, pre-existing warnings accepted, add no new errors. Domain field is `managedCertificateId` (lowercase d). (frontend digest §8, §2)
- **Commit trailers:** backend-v2 and frontend-v2 commits OMIT the Claude trailer; helm-chart and site commits INCLUDE `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`. Verify per repo with `git log` before committing.

## Review Focus

Input classes/failure modes the spec implies that no single task's happy-path tests fully exercise — each gets its test pinned to the owning task (noted inline):

1. **Gateway address never appears / is empty** (`status.addresses` absent) — record must stay `pending` and must NOT create a target-less or empty-target DNSEndpoint. (Task 5 + Task 8 tests)
2. **Forced `record_type` conflicts with resolved target** (e.g. `CNAME` forced but address is an IP) — must yield `error` status with a clear message, not a broken DNSEndpoint. (Task 8 test)
3. **Provider/credential mismatch** — `providerCredentialId` ≠ active credential, or no active credential set — must 400 with a message, never silently write with the wrong provider. (Task 8 + Task 9 tests)
4. **Domain deletion with a record present** — the DNSEndpoint must be deleted (so external-dns removes the record) before/with the DB cascade; a leaked DNSEndpoint would orphan a live record. (Task 8 delete test + Task 10)
5. **Active-credential Secret render per provider** — Route53/Google/Azure render different Secret keys/files than Cloudflare; a wrong key silently breaks external-dns for that provider. (Task 2 RenderSecret tests, one per provider)

---

## Task 1: Migration + `DomainDNSRecord` model

**Files:**
- Create: `migrations/000046_add_domain_dns_records.up.sql`, `migrations/000046_add_domain_dns_records.down.sql` (verify 000046 is the next free number: `ls migrations | tail`)
- Create: `internal/models/domain_dns_record.go`
- Test: `internal/models/domain_dns_record_test.go`

**Interfaces:**
- Produces: `models.DomainDNSRecord` struct; `models.DNSRecordStatus` enum (`DNSRecordStatusPending|Syncing|Ready|Error`); `models.DNSRecordType` (`auto|A|AAAA|CNAME`); `DomainDNSRecord.TableName() = "domain_dns_records"`.

- [ ] **Step 1: Write the migration**

`migrations/000046_add_domain_dns_records.up.sql`:
```sql
CREATE TABLE domain_dns_records (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id UUID NOT NULL UNIQUE REFERENCES domains(id) ON DELETE CASCADE,
    provider_credential_id UUID NOT NULL REFERENCES dns_provider_credentials(id),
    record_type VARCHAR(16) NOT NULL DEFAULT 'auto',
    ttl INTEGER,
    proxied BOOLEAN NOT NULL DEFAULT FALSE,
    resolved_target TEXT,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    status_message TEXT,
    endpoint_name TEXT,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_domain_dns_records_credential ON domain_dns_records(provider_credential_id);
```
`migrations/000046_add_domain_dns_records.down.sql`:
```sql
DROP TABLE IF EXISTS domain_dns_records;
```

- [ ] **Step 2: Write the model**

`internal/models/domain_dns_record.go`:
```go
package models

import (
	"time"

	"github.com/google/uuid"
)

type DNSRecordStatus string

const (
	DNSRecordStatusPending DNSRecordStatus = "pending"
	DNSRecordStatusSyncing DNSRecordStatus = "syncing"
	DNSRecordStatusReady   DNSRecordStatus = "ready"
	DNSRecordStatusError   DNSRecordStatus = "error"
)

type DNSRecordType string

const (
	DNSRecordTypeAuto  DNSRecordType = "auto"
	DNSRecordTypeA     DNSRecordType = "A"
	DNSRecordTypeAAAA  DNSRecordType = "AAAA"
	DNSRecordTypeCNAME DNSRecordType = "CNAME"
)

// DomainDNSRecord is the one FastGateway-managed DNS record for a domain.
// Hostname is always the domain's hostname (not stored). ResolvedTarget is a
// display cache of the last gateway address we resolved.
type DomainDNSRecord struct {
	ID                   uuid.UUID       `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	DomainID             uuid.UUID       `gorm:"type:uuid;not null;uniqueIndex" json:"domainId"`
	ProviderCredentialID uuid.UUID       `gorm:"type:uuid;not null;index" json:"providerCredentialId"`
	RecordType           DNSRecordType   `gorm:"column:record_type;not null;default:'auto'" json:"recordType"`
	TTL                  *int            `gorm:"column:ttl" json:"ttl,omitempty"`
	Proxied              bool            `gorm:"not null;default:false" json:"proxied"`
	ResolvedTarget       string          `gorm:"column:resolved_target" json:"resolvedTarget,omitempty"`
	Status               DNSRecordStatus `gorm:"not null;default:'pending'" json:"status"`
	StatusMessage        string          `gorm:"column:status_message" json:"statusMessage,omitempty"`
	EndpointName         string          `gorm:"column:endpoint_name" json:"endpointName,omitempty"`
	CreatedBy            uuid.UUID       `gorm:"type:uuid;not null" json:"createdBy"`
	CreatedAt            time.Time       `gorm:"not null;default:now()" json:"createdAt"`
	UpdatedAt            time.Time       `gorm:"not null;default:now()" json:"updatedAt"`
}

func (DomainDNSRecord) TableName() string { return "domain_dns_records" }
```

- [ ] **Step 3: Write the test**

`internal/models/domain_dns_record_test.go`:
```go
package models

import "testing"

func TestDomainDNSRecord_TableName(t *testing.T) {
	if got := (DomainDNSRecord{}).TableName(); got != "domain_dns_records" {
		t.Fatalf("TableName() = %q, want domain_dns_records", got)
	}
}

func TestDNSRecordStatusConstants(t *testing.T) {
	for _, s := range []DNSRecordStatus{DNSRecordStatusPending, DNSRecordStatusSyncing, DNSRecordStatusReady, DNSRecordStatusError} {
		if s == "" {
			t.Fatal("empty DNSRecordStatus constant")
		}
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/zufardhiyaulhaq/Documents/personal/github/backend-v2 && go test ./internal/models/ -run 'TestDomainDNSRecord|TestDNSRecordStatus' -v`
Expected: PASS. Also `go build ./...` succeeds.

- [ ] **Step 5: Commit**

```bash
git add migrations/000046_add_domain_dns_records.*.sql internal/models/domain_dns_record.go internal/models/domain_dns_record_test.go
git commit -m "feat(dns): add domain_dns_records migration and model"
```

---

## Task 2: Provider abstraction package (`internal/dnsprovider`)

**Files:**
- Create: `internal/dnsprovider/provider.go` (interface + registry)
- Create: `internal/dnsprovider/cloudflare.go`, `route53.go`, `google.go`, `azure.go`
- Test: `internal/dnsprovider/provider_test.go`

**Interfaces:**
- Produces:
  - `type DNSProvider interface { Type() string; RequiredFields() []string; Validate(map[string]string) error; RenderSecret(map[string]string) map[string][]byte; ExternalDNSFlag() string }`
  - `func Get(providerType string) (DNSProvider, bool)`
  - `func Supported() map[string]bool`
  - `const SecretName = "fgw-externaldns-credentials"`

- [ ] **Step 1: Write the failing tests**

`internal/dnsprovider/provider_test.go`:
```go
package dnsprovider

import (
	"reflect"
	"testing"
)

func TestSupportedProviders(t *testing.T) {
	want := map[string]bool{"cloudflare": true, "route53": true, "google": true, "azure": true}
	if got := Supported(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Supported() = %v, want %v", got, want)
	}
}

func TestValidate_MissingFields(t *testing.T) {
	p, _ := Get("cloudflare")
	if err := p.Validate(map[string]string{}); err == nil {
		t.Fatal("expected error for missing apiToken")
	}
	if err := p.Validate(map[string]string{"apiToken": "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderSecret_PerProvider(t *testing.T) {
	cf, _ := Get("cloudflare")
	if got := cf.RenderSecret(map[string]string{"apiToken": "tok"}); string(got["apiToken"]) != "tok" {
		t.Fatalf("cloudflare secret key apiToken = %q", got["apiToken"])
	}
	r53, _ := Get("route53")
	s := r53.RenderSecret(map[string]string{"accessKeyId": "AK", "secretAccessKey": "SK"})
	if string(s["AWS_ACCESS_KEY_ID"]) != "AK" || string(s["AWS_SECRET_ACCESS_KEY"]) != "SK" {
		t.Fatalf("route53 secret = %v", s)
	}
	g, _ := Get("google")
	if got := g.RenderSecret(map[string]string{"serviceAccountKey": `{"x":1}`, "project": "p"}); string(got["credentials.json"]) != `{"x":1}` {
		t.Fatalf("google secret credentials.json = %q", got["credentials.json"])
	}
	az, _ := Get("azure")
	if got := az.RenderSecret(map[string]string{"tenantId": "t", "subscriptionId": "s", "resourceGroup": "rg", "clientId": "c", "clientSecret": "sec"}); len(got["azure.json"]) == 0 {
		t.Fatal("azure secret azure.json empty")
	}
}

func TestExternalDNSFlag(t *testing.T) {
	cases := map[string]string{"cloudflare": "cloudflare", "route53": "aws", "google": "google", "azure": "azure"}
	for ptype, flag := range cases {
		p, _ := Get(ptype)
		if p.ExternalDNSFlag() != flag {
			t.Fatalf("%s ExternalDNSFlag() = %q, want %q", ptype, p.ExternalDNSFlag(), flag)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/zufardhiyaulhaq/Documents/personal/github/backend-v2 && go test ./internal/dnsprovider/ 2>&1 | head`
Expected: FAIL — package/identifiers undefined.

- [ ] **Step 3: Implement the interface + registry**

`internal/dnsprovider/provider.go`:
```go
// Package dnsprovider isolates everything provider-specific about DNS:
// credential field validation, the external-dns Secret payload, and the
// external-dns --provider flag. The DNSEndpoint CR and record lifecycle are
// provider-agnostic and live elsewhere.
package dnsprovider

import (
	"fmt"
	"sort"
)

// SecretName is the fixed Secret in fastgateway-system that external-dns reads.
const SecretName = "fgw-externaldns-credentials"

type DNSProvider interface {
	Type() string
	RequiredFields() []string
	Validate(cred map[string]string) error
	RenderSecret(cred map[string]string) map[string][]byte
	ExternalDNSFlag() string
}

var registry = map[string]DNSProvider{}

func register(p DNSProvider) { registry[p.Type()] = p }

func Get(providerType string) (DNSProvider, bool) {
	p, ok := registry[providerType]
	return p, ok
}

func Supported() map[string]bool {
	out := make(map[string]bool, len(registry))
	for k := range registry {
		out[k] = true
	}
	return out
}

// requireFields returns an error naming every missing/empty required field.
func requireFields(cred map[string]string, fields []string) error {
	var missing []string
	for _, f := range fields {
		if cred[f] == "" {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required credential field(s): %v", missing)
	}
	return nil
}
```

`internal/dnsprovider/cloudflare.go`:
```go
package dnsprovider

func init() { register(cloudflare{}) }

type cloudflare struct{}

func (cloudflare) Type() string             { return "cloudflare" }
func (cloudflare) RequiredFields() []string { return []string{"apiToken"} }
func (c cloudflare) Validate(cred map[string]string) error {
	return requireFields(cred, c.RequiredFields())
}
func (cloudflare) RenderSecret(cred map[string]string) map[string][]byte {
	return map[string][]byte{"apiToken": []byte(cred["apiToken"])}
}
func (cloudflare) ExternalDNSFlag() string { return "cloudflare" }
```

`internal/dnsprovider/route53.go`:
```go
package dnsprovider

func init() { register(route53{}) }

type route53 struct{}

func (route53) Type() string             { return "route53" }
func (route53) RequiredFields() []string { return []string{"accessKeyId", "secretAccessKey"} }
func (r route53) Validate(cred map[string]string) error {
	return requireFields(cred, r.RequiredFields())
}
func (route53) RenderSecret(cred map[string]string) map[string][]byte {
	return map[string][]byte{
		"AWS_ACCESS_KEY_ID":     []byte(cred["accessKeyId"]),
		"AWS_SECRET_ACCESS_KEY": []byte(cred["secretAccessKey"]),
	}
}
func (route53) ExternalDNSFlag() string { return "aws" }
```

`internal/dnsprovider/google.go`:
```go
package dnsprovider

func init() { register(google{}) }

type google struct{}

func (google) Type() string             { return "google" }
func (google) RequiredFields() []string { return []string{"serviceAccountKey", "project"} }
func (g google) Validate(cred map[string]string) error {
	return requireFields(cred, g.RequiredFields())
}
func (google) RenderSecret(cred map[string]string) map[string][]byte {
	return map[string][]byte{"credentials.json": []byte(cred["serviceAccountKey"])}
}
func (google) ExternalDNSFlag() string { return "google" }
```

`internal/dnsprovider/azure.go`:
```go
package dnsprovider

import "encoding/json"

func init() { register(azure{}) }

type azure struct{}

func (azure) Type() string { return "azure" }
func (azure) RequiredFields() []string {
	return []string{"tenantId", "subscriptionId", "resourceGroup", "clientId", "clientSecret"}
}
func (a azure) Validate(cred map[string]string) error {
	return requireFields(cred, a.RequiredFields())
}
func (azure) RenderSecret(cred map[string]string) map[string][]byte {
	cfg, _ := json.Marshal(map[string]string{
		"tenantId":        cred["tenantId"],
		"subscriptionId":  cred["subscriptionId"],
		"resourceGroup":   cred["resourceGroup"],
		"aadClientId":     cred["clientId"],
		"aadClientSecret": cred["clientSecret"],
	})
	return map[string][]byte{"azure.json": cfg}
}
func (azure) ExternalDNSFlag() string { return "azure" }
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/dnsprovider/ -v`
Expected: PASS (all four tests, including the per-provider RenderSecret — Review Focus #5).

- [ ] **Step 5: Commit**

```bash
git add internal/dnsprovider/
git commit -m "feat(dns): provider abstraction for cloudflare/route53/google/azure"
```

---

## Task 3: Multi-provider credential validation

**Files:**
- Modify: `internal/services/dns_credential_service.go` (replace `supportedDNSProviders` + validation with `dnsprovider` registry)
- Test: `internal/services/dns_credential_service_test.go` (extend)

**Interfaces:**
- Consumes: `dnsprovider.Get`, `dnsprovider.Supported` (Task 2).
- Produces: credential Create/Update validate required fields per provider via the registry.

- [ ] **Step 1: Write the failing test**

Append to `internal/services/dns_credential_service_test.go`:
```go
func TestCreateDNSCredential_Route53ValidatesFields(t *testing.T) {
	svc, _ := newTestDNSCredentialService(t) // existing helper; see other tests in this file
	_, err := svc.Create(&CreateDNSCredentialInput{
		Name: "aws", ProviderType: "route53",
		Credentials: map[string]string{"accessKeyId": "AK"}, // missing secretAccessKey
	}, testUserID)
	if err == nil {
		t.Fatal("expected validation error for missing secretAccessKey")
	}
}

func TestCreateDNSCredential_UnsupportedProvider(t *testing.T) {
	svc, _ := newTestDNSCredentialService(t)
	_, err := svc.Create(&CreateDNSCredentialInput{Name: "x", ProviderType: "bind", Credentials: map[string]string{}}, testUserID)
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}
```
(If `newTestDNSCredentialService`/`testUserID` names differ, match the existing helpers already in this test file.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/services/ -run TestCreateDNSCredential -v`
Expected: FAIL (route53 currently unsupported; only cloudflare allowed).

- [ ] **Step 3: Implement**

In `internal/services/dns_credential_service.go`, delete the `var supportedDNSProviders = map[string]bool{"cloudflare": true}` line and change the Create/Update validation to use the registry:
```go
import "github.com/fastgateway-dev/backend-v2/internal/dnsprovider"

// in Create (and Update where it validates provider/credentials):
prov, ok := dnsprovider.Get(input.ProviderType)
if !ok {
	return nil, errors.New("unsupported DNS provider: " + input.ProviderType)
}
if err := prov.Validate(input.Credentials); err != nil {
	return nil, err
}
```
Keep the existing encryption loop (`crypto.Encrypt` per value) unchanged after validation.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/services/ -run 'TestCreateDNSCredential|TestDNSCredential' -v`
Expected: PASS. Existing cloudflare tests still pass.

- [ ] **Step 5: Commit**

```bash
git add internal/services/dns_credential_service.go internal/services/dns_credential_service_test.go
git commit -m "feat(dns): validate credentials per provider via dnsprovider registry"
```

---

## Task 4: `DNSEndpoint` + Secret builders + GVRs (k8s)

**Files:**
- Modify: `internal/kubernetes/gvr.go` (add `DNSEndpointGVR`, `SecretGVR` if absent)
- Create: `internal/kubernetes/externaldns.go` (builders)
- Test: `internal/kubernetes/externaldns_test.go` (golden)
- Create golden dir on first `-update-golden`: `internal/kubernetes/testdata/golden/externaldns/`

**Interfaces:**
- Produces:
  - `kubernetes.DNSEndpointGVR`, `kubernetes.SecretGVR`
  - `func DNSEndpoint(cfg DNSEndpointConfig) *unstructured.Unstructured`
  - `type DNSEndpointConfig struct { Name, Hostname, RecordType string; Targets []string; TTL *int; CloudflareProxied bool }`
  - `func ExternalDNSSecret(name string, data map[string][]byte) *unstructured.Unstructured`

- [ ] **Step 1: Add the GVRs**

In `internal/kubernetes/gvr.go`, add to the appropriate `var (...)` block:
```go
DNSEndpointGVR = schema.GroupVersionResource{Group: "externaldns.k8s.io", Version: "v1alpha1", Resource: "dnsendpoints"}
SecretGVR      = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
```
(If `SecretGVR` already exists, reuse it — `grep SecretGVR internal/kubernetes/`.)

- [ ] **Step 2: Write the failing golden test**

`internal/kubernetes/externaldns_test.go`:
```go
package kubernetes_test

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/stretchr/testify/assert"
)

func TestDNSEndpoint_A_Golden(t *testing.T) {
	ttl := 300
	obj := kubernetes.DNSEndpoint(kubernetes.DNSEndpointConfig{
		Name: "dns-11111111", Hostname: "app.example.com", RecordType: "A",
		Targets: []string{"203.0.113.10"}, TTL: &ttl,
	})
	assert.Equal(t, "DNSEndpoint", obj.Object["kind"])
	assert.Equal(t, "fastgateway-system", obj.GetNamespace())
	assertGolden(t, "externaldns", "dnsendpoint-a", obj)
}

func TestDNSEndpoint_CNAME_ProxiedGolden(t *testing.T) {
	obj := kubernetes.DNSEndpoint(kubernetes.DNSEndpointConfig{
		Name: "dns-22222222", Hostname: "app.example.com", RecordType: "CNAME",
		Targets: []string{"lb.example.net"}, CloudflareProxied: true,
	})
	assertGolden(t, "externaldns", "dnsendpoint-cname-proxied", obj)
}

func TestExternalDNSSecret_Golden(t *testing.T) {
	obj := kubernetes.ExternalDNSSecret(kubernetes.ExternalDNSSecretName, map[string][]byte{"apiToken": []byte("tok")})
	assert.Equal(t, "Secret", obj.Object["kind"])
	assertGolden(t, "externaldns", "externaldns-secret", obj)
}
```
NOTE: the existing `assertGolden` in `certmanager_test.go` takes `(t, name, obj)` and hardcodes the `certmanager` subdir. Add a subdir parameter: change its signature to `assertGolden(t *testing.T, subdir, name string, obj any)` and update the `certmanager` subdir to `"certmanager"` at each existing call site. Do this as Step 2a before the test compiles.

- [ ] **Step 2a: Generalize `assertGolden`**

In `internal/kubernetes/certmanager_test.go`, change:
```go
func assertGolden(t *testing.T, subdir, name string, obj any) {
	t.Helper()
	got, err := yaml.Marshal(obj)
	require.NoError(t, err)
	path := filepath.Join("testdata", "golden", subdir, name+".yaml")
	// ... rest unchanged ...
}
```
and update every existing call `assertGolden(t, "<name>", obj)` → `assertGolden(t, "certmanager", "<name>", obj)`.

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/kubernetes/ -run 'TestDNSEndpoint|TestExternalDNSSecret' 2>&1 | head`
Expected: FAIL — builders undefined.

- [ ] **Step 4: Implement the builders**

`internal/kubernetes/externaldns.go`:
```go
package kubernetes

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// ExternalDNSSecretName is the fixed Secret external-dns reads for its provider
// credentials. Must match internal/dnsprovider.SecretName.
const ExternalDNSSecretName = "fgw-externaldns-credentials"

type DNSEndpointConfig struct {
	Name              string
	Hostname          string
	RecordType        string // A | AAAA | CNAME
	Targets           []string
	TTL               *int
	CloudflareProxied bool
}

// DNSEndpoint builds an externaldns.k8s.io/v1alpha1 DNSEndpoint in
// fastgateway-system for a single hostname->targets record.
func DNSEndpoint(cfg DNSEndpointConfig) *unstructured.Unstructured {
	targets := make([]interface{}, len(cfg.Targets))
	for i, t := range cfg.Targets {
		targets[i] = t
	}
	endpoint := map[string]interface{}{
		"dnsName":    cfg.Hostname,
		"recordType": cfg.RecordType,
		"targets":    targets,
	}
	if cfg.TTL != nil {
		endpoint["recordTTL"] = int64(*cfg.TTL)
	}
	meta := map[string]interface{}{
		"name":      cfg.Name,
		"namespace": FastGatewayNamespace,
		"labels":    managedByLabels(),
	}
	if cfg.CloudflareProxied {
		meta["annotations"] = map[string]interface{}{
			"external-dns.alpha.kubernetes.io/cloudflare-proxied": "true",
		}
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "externaldns.k8s.io/v1alpha1",
		"kind":       "DNSEndpoint",
		"metadata":   meta,
		"spec":       map[string]interface{}{"endpoints": []interface{}{endpoint}},
	}}
}

// ExternalDNSSecret builds the opaque Secret external-dns reads for credentials.
func ExternalDNSSecret(name string, data map[string][]byte) *unstructured.Unstructured {
	strData := make(map[string]interface{}, len(data))
	for k, v := range data {
		strData[k] = string(v)
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"type":       "Opaque",
		"metadata": map[string]interface{}{
			"name": name, "namespace": FastGatewayNamespace, "labels": managedByLabels(),
		},
		"stringData": strData,
	}}
}
```
(`FastGatewayNamespace` and `managedByLabels()` already exist in this package — used by certmanager.go.)

- [ ] **Step 5: Generate goldens + run**

Run: `go test ./internal/kubernetes/ -run 'TestDNSEndpoint|TestExternalDNSSecret' -update-golden && go test ./internal/kubernetes/ -run 'TestDNSEndpoint|TestExternalDNSSecret|Golden' -v`
Expected: PASS. Inspect the three generated files under `testdata/golden/externaldns/` for correctness (A record has `recordTTL: 300`; CNAME has the `cloudflare-proxied` annotation).

- [ ] **Step 6: Commit**

```bash
git add internal/kubernetes/gvr.go internal/kubernetes/externaldns.go internal/kubernetes/externaldns_test.go internal/kubernetes/certmanager_test.go internal/kubernetes/testdata/golden/externaldns/
git commit -m "feat(dns): DNSEndpoint and external-dns Secret builders with golden fixtures"
```

---

## Task 5: Gateway address resolver

**Files:**
- Create: `internal/services/gateway_address.go`
- Test: `internal/services/gateway_address_test.go`

**Interfaces:**
- Consumes: `CertInfraApplier.Get` (digest §4, already a service dependency type), `kubernetes.GatewayGVR`.
- Produces:
  - `type GatewayAddress struct { Value string; Kind string }` where `Kind` ∈ `"ip" | "hostname"` (empty `Value` = not assigned yet)
  - `func resolveGatewayAddress(obj *unstructured.Unstructured) (GatewayAddress, bool)` — pure, testable from a fake Gateway object
  - `func recordTypeForAddress(addr GatewayAddress, forced models.DNSRecordType) (models.DNSRecordType, error)`

- [ ] **Step 1: Write the failing tests**

`internal/services/gateway_address_test.go`:
```go
package services

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func gw(addrs ...map[string]interface{}) *unstructured.Unstructured {
	list := make([]interface{}, len(addrs))
	for i, a := range addrs {
		list[i] = a
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"status": map[string]interface{}{"addresses": list},
	}}
}

func TestResolveGatewayAddress_IP(t *testing.T) {
	a, ok := resolveGatewayAddress(gw(map[string]interface{}{"type": "IPAddress", "value": "203.0.113.5"}))
	if !ok || a.Value != "203.0.113.5" || a.Kind != "ip" {
		t.Fatalf("got %+v ok=%v", a, ok)
	}
}

func TestResolveGatewayAddress_Hostname(t *testing.T) {
	a, ok := resolveGatewayAddress(gw(map[string]interface{}{"type": "Hostname", "value": "lb.example.net"}))
	if !ok || a.Kind != "hostname" {
		t.Fatalf("got %+v ok=%v", a, ok)
	}
}

func TestResolveGatewayAddress_None(t *testing.T) {
	if _, ok := resolveGatewayAddress(gw()); ok {
		t.Fatal("expected not found for empty addresses")
	}
}

func TestRecordTypeForAddress(t *testing.T) {
	ip := GatewayAddress{Value: "203.0.113.5", Kind: "ip"}
	if ty, _ := recordTypeForAddress(ip, models.DNSRecordTypeAuto); ty != models.DNSRecordTypeA {
		t.Fatalf("auto+ip = %v, want A", ty)
	}
	v6 := GatewayAddress{Value: "2001:db8::1", Kind: "ip"}
	if ty, _ := recordTypeForAddress(v6, models.DNSRecordTypeAuto); ty != models.DNSRecordTypeAAAA {
		t.Fatalf("auto+ipv6 = %v, want AAAA", ty)
	}
	host := GatewayAddress{Value: "lb.example.net", Kind: "hostname"}
	if ty, _ := recordTypeForAddress(host, models.DNSRecordTypeAuto); ty != models.DNSRecordTypeCNAME {
		t.Fatalf("auto+host = %v, want CNAME", ty)
	}
	// forced CNAME on an IP target is a conflict (Review Focus #2)
	if _, err := recordTypeForAddress(ip, models.DNSRecordTypeCNAME); err == nil {
		t.Fatal("expected conflict error for forced CNAME on IP target")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/services/ -run 'TestResolveGatewayAddress|TestRecordTypeForAddress' 2>&1 | head`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

`internal/services/gateway_address.go`:
```go
package services

import (
	"fmt"
	"net"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type GatewayAddress struct {
	Value string
	Kind  string // "ip" | "hostname"
}

// resolveGatewayAddress reads the first usable address from a Gateway's
// status.addresses. ok=false means no address is assigned yet.
func resolveGatewayAddress(obj *unstructured.Unstructured) (GatewayAddress, bool) {
	addrs, found, err := unstructured.NestedSlice(obj.Object, "status", "addresses")
	if err != nil || !found || len(addrs) == 0 {
		return GatewayAddress{}, false
	}
	for _, a := range addrs {
		m, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		val, _ := m["value"].(string)
		if val == "" {
			continue
		}
		kind := "hostname"
		if net.ParseIP(val) != nil {
			kind = "ip"
		}
		return GatewayAddress{Value: val, Kind: kind}, true
	}
	return GatewayAddress{}, false
}

// recordTypeForAddress resolves the effective record type, honoring a forced
// type and erroring on an impossible combination.
func recordTypeForAddress(addr GatewayAddress, forced models.DNSRecordType) (models.DNSRecordType, error) {
	auto := models.DNSRecordTypeCNAME
	if addr.Kind == "ip" {
		if ip := net.ParseIP(addr.Value); ip != nil && ip.To4() == nil {
			auto = models.DNSRecordTypeAAAA
		} else {
			auto = models.DNSRecordTypeA
		}
	}
	if forced == "" || forced == models.DNSRecordTypeAuto {
		return auto, nil
	}
	// Validate the forced type against the address kind.
	switch forced {
	case models.DNSRecordTypeA, models.DNSRecordTypeAAAA:
		if addr.Kind != "ip" {
			return "", fmt.Errorf("record type %s requires an IP target, but the gateway address %q is a hostname", forced, addr.Value)
		}
	case models.DNSRecordTypeCNAME:
		if addr.Kind != "hostname" {
			return "", fmt.Errorf("record type CNAME requires a hostname target, but the gateway address %q is an IP", addr.Value)
		}
	}
	return forced, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/services/ -run 'TestResolveGatewayAddress|TestRecordTypeForAddress' -v`
Expected: PASS (covers Review Focus #1 empty-address and #2 forced-type conflict).

- [ ] **Step 5: Commit**

```bash
git add internal/services/gateway_address.go internal/services/gateway_address_test.go
git commit -m "feat(dns): resolve gateway address and record type from Gateway status"
```

---

## Task 6: `DomainDNSRecord` repository + interface

**Files:**
- Create: `internal/repository/domain_dns_record_repository.go`
- Modify: `internal/repository/interfaces.go` (add interface + assertion)
- Test: existing repo tests use a sqlite/pg harness; add `internal/repository/domain_dns_record_repository_test.go` only if the repo package has a test DB helper (check `grep -l "func.*testDB\|setupTestDB" internal/repository/*_test.go`). If a helper exists, add a round-trip test; if not, skip the repo test (the service tests in Task 8 cover it via a mock).

**Interfaces:**
- Produces:
  - `type DomainDNSRecordRepository struct{ db *gorm.DB }`, `func NewDomainDNSRecordRepository(db *gorm.DB) *DomainDNSRecordRepository`
  - Methods: `Create(*models.DomainDNSRecord) error`, `GetByDomainID(uuid.UUID) (*models.DomainDNSRecord, error)`, `Update(*models.DomainDNSRecord) error`, `DeleteByDomainID(uuid.UUID) error`, `CountByCredential(uuid.UUID) (int64, error)`
  - `DomainDNSRecordRepositoryInterface` with the same methods; `var _ DomainDNSRecordRepositoryInterface = (*DomainDNSRecordRepository)(nil)`

- [ ] **Step 1: Implement the repository**

`internal/repository/domain_dns_record_repository.go`:
```go
package repository

import (
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type DomainDNSRecordRepository struct{ db *gorm.DB }

func NewDomainDNSRecordRepository(db *gorm.DB) *DomainDNSRecordRepository {
	return &DomainDNSRecordRepository{db: db}
}

func (r *DomainDNSRecordRepository) Create(rec *models.DomainDNSRecord) error {
	return r.db.Create(rec).Error
}

func (r *DomainDNSRecordRepository) GetByDomainID(domainID uuid.UUID) (*models.DomainDNSRecord, error) {
	var rec models.DomainDNSRecord
	if err := r.db.Where("domain_id = ?", domainID).First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *DomainDNSRecordRepository) Update(rec *models.DomainDNSRecord) error {
	return r.db.Save(rec).Error
}

func (r *DomainDNSRecordRepository) DeleteByDomainID(domainID uuid.UUID) error {
	return r.db.Where("domain_id = ?", domainID).Delete(&models.DomainDNSRecord{}).Error
}

func (r *DomainDNSRecordRepository) CountByCredential(credID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.Model(&models.DomainDNSRecord{}).Where("provider_credential_id = ?", credID).Count(&n).Error
	return n, err
}
```

- [ ] **Step 2: Add the interface + assertion**

In `internal/repository/interfaces.go`:
```go
// DomainDNSRecordRepositoryInterface defines domain DNS record repository operations
type DomainDNSRecordRepositoryInterface interface {
	Create(rec *models.DomainDNSRecord) error
	GetByDomainID(domainID uuid.UUID) (*models.DomainDNSRecord, error)
	Update(rec *models.DomainDNSRecord) error
	DeleteByDomainID(domainID uuid.UUID) error
	CountByCredential(credID uuid.UUID) (int64, error)
}
```
and at the bottom with the other assertions:
```go
var _ DomainDNSRecordRepositoryInterface = (*DomainDNSRecordRepository)(nil)
```

- [ ] **Step 3: Build + (optional) repo test**

Run: `go build ./... && go vet ./internal/repository/`
Expected: compiles. If a repo test DB helper exists, add a create→get→update→delete round-trip test and run it.

- [ ] **Step 4: Commit**

```bash
git add internal/repository/domain_dns_record_repository.go internal/repository/interfaces.go internal/repository/domain_dns_record_repository_test.go 2>/dev/null; git add -A internal/repository/
git commit -m "feat(dns): domain DNS record repository"
```

---

## Task 7: Active DNS credential system setting + external-dns Secret rendering

**Files:**
- Read first: `grep -rl "system_settings\|SystemSetting\|GetSetting\|SetSetting" internal/services internal/repository internal/models` to find the existing settings mechanism (spec §4.3; OpenAPI `paths/system-settings.yaml` confirms one exists).
- Create: `internal/services/dns_infra_service.go`
- Test: `internal/services/dns_infra_service_test.go`

**Interfaces:**
- Consumes: the existing system-settings get/set (follow the file you found above), `DNSCredentialService.DecryptedCredentials(id) (providerType string, creds map[string]string, error)` (digest §5), `dnsprovider.Get`, `CertInfraApplier.ApplyNamespaced`, `kubernetes.ExternalDNSSecret`, `kubernetes.SecretGVR`, `dnsprovider.SecretName`.
- Produces:
  - `const SettingActiveDNSCredential = "dns.active_credential_id"`
  - `func (s *DNSInfraService) GetActiveCredentialID() (uuid.UUID, bool, error)`
  - `func (s *DNSInfraService) SetActiveCredential(id uuid.UUID) error` — validates the credential exists, stores the setting, renders+applies the external-dns Secret
  - `func (s *DNSInfraService) activeProvider() (dnsprovider.DNSProvider, uuid.UUID, error)`

- [ ] **Step 1: Write the failing test**

`internal/services/dns_infra_service_test.go`:
```go
package services

import "testing"

func TestSetActiveCredential_RendersSecret(t *testing.T) {
	svc, deps := newTestDNSInfraService(t) // helper you write in this file: wires fakes for creds, settings, applier
	credID := deps.seedCloudflareCredential(t, "tok")

	if err := svc.SetActiveCredential(credID); err != nil {
		t.Fatalf("SetActiveCredential: %v", err)
	}
	// the external-dns Secret was applied with the cloudflare apiToken
	applied := deps.applier.lastApplied(SecretGVRResource())
	if applied == nil {
		t.Fatal("no Secret applied")
	}
	// and the setting persisted
	got, ok, _ := svc.GetActiveCredentialID()
	if !ok || got != credID {
		t.Fatalf("GetActiveCredentialID = %v,%v want %v", got, ok, credID)
	}
}

func TestSetActiveCredential_UnknownCredential(t *testing.T) {
	svc, _ := newTestDNSInfraService(t)
	if err := svc.SetActiveCredential(randomUUID()); err == nil {
		t.Fatal("expected error for unknown credential")
	}
}
```
Write `newTestDNSInfraService` + the fake applier (record last-applied unstructured per GVR) + `seedCloudflareCredential` in this test file, following the fake/mocks style already used in `internal/services/*_test.go` (check an existing service test for the fake applier shape; `managed_certificate_service_test.go` has a control-plane fake to copy).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/services/ -run TestSetActiveCredential 2>&1 | head`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

`internal/services/dns_infra_service.go` (adapt the settings get/set calls to the real settings API you located in Step 0):
```go
package services

import (
	"context"
	"errors"

	"github.com/fastgateway-dev/backend-v2/internal/dnsprovider"
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/google/uuid"
)

const SettingActiveDNSCredential = "dns.active_credential_id"

type DNSInfraService struct {
	creds        DNSCredentialReader        // interface exposing DecryptedCredentials + exists check
	settings     SystemSettingStore          // interface over the existing settings service (Get/Set string)
	controlPlane CertInfraApplier            // reused role (digest §4)
}

type DNSInfraServiceDeps struct {
	Creds        DNSCredentialReader
	Settings     SystemSettingStore
	ControlPlane CertInfraApplier
}

func NewDNSInfraService(deps DNSInfraServiceDeps) *DNSInfraService {
	if deps.Creds == nil || deps.Settings == nil || deps.ControlPlane == nil {
		panic("services.NewDNSInfraService: missing required dependency")
	}
	return &DNSInfraService{creds: deps.Creds, settings: deps.Settings, controlPlane: deps.ControlPlane}
}

func (s *DNSInfraService) GetActiveCredentialID() (uuid.UUID, bool, error) {
	raw, ok, err := s.settings.Get(SettingActiveDNSCredential)
	if err != nil || !ok || raw == "" {
		return uuid.Nil, false, err
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

func (s *DNSInfraService) SetActiveCredential(id uuid.UUID) error {
	providerType, creds, err := s.creds.DecryptedCredentials(id)
	if err != nil {
		return errors.New("credential not found or undecryptable")
	}
	prov, ok := dnsprovider.Get(providerType)
	if !ok {
		return errors.New("unsupported DNS provider: " + providerType)
	}
	secret := kubernetes.ExternalDNSSecret(kubernetes.ExternalDNSSecretName, prov.RenderSecret(creds))
	if err := s.controlPlane.ApplyNamespaced(context.Background(), kubernetes.SecretGVR, secret); err != nil {
		return err
	}
	return s.settings.Set(SettingActiveDNSCredential, id.String())
}
```
Define the small adapter interfaces (`DNSCredentialReader` with `DecryptedCredentials(uuid.UUID) (string, map[string]string, error)`; `SystemSettingStore` with `Get(key) (string, bool, error)` / `Set(key, value) error`) in this file, and in `cmd/server/main.go` satisfy them with the real `DNSCredentialService` and the real settings service (Task 9 wiring).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/services/ -run TestSetActiveCredential -v`
Expected: PASS (covers Review Focus #5 render via the active-credential path).

- [ ] **Step 5: Commit**

```bash
git add internal/services/dns_infra_service.go internal/services/dns_infra_service_test.go
git commit -m "feat(dns): active DNS credential setting and external-dns secret rendering"
```

---

## Task 8: DNS record service

**Files:**
- Create: `internal/services/dns_record_service.go`
- Test: `internal/services/dns_record_service_test.go`

**Interfaces:**
- Consumes: `DomainDNSRecordRepositoryInterface` (Task 6), `DomainRepositoryInterface.GetByID`, `DNSInfraService.GetActiveCredentialID` (Task 7), `resolveGatewayAddress`/`recordTypeForAddress` (Task 5), `CertInfraApplier` (Get/ApplyNamespaced/Delete), `kubernetes.DNSEndpoint`/`DNSEndpointGVR`/`GatewayGVR`.
- Produces:
  - `type DNSRecordInput struct { ProviderCredentialID *uuid.UUID; RecordType models.DNSRecordType; TTL *int; Proxied bool }`
  - `func (s *DNSRecordService) Enable(domainID, createdBy uuid.UUID, in DNSRecordInput) (*models.DomainDNSRecord, error)`
  - `func (s *DNSRecordService) Get(domainID uuid.UUID) (*models.DomainDNSRecord, error)` — resolves + reconciles + returns live status
  - `func (s *DNSRecordService) Update(domainID uuid.UUID, in DNSRecordInput) (*models.DomainDNSRecord, error)`
  - `func (s *DNSRecordService) Delete(domainID uuid.UUID) error`
  - `func (s *DNSRecordService) Refresh(domainID uuid.UUID) (*models.DomainDNSRecord, error)`
  - `var ErrNoActiveDNSCredential`, `var ErrCredentialNotActive`
  - gateway name for a domain derived as it is elsewhere (reuse `domainplan`/domain fields — check how `applyGateway` names the Gateway; use the same name).

- [ ] **Step 1: Write the failing tests**

`internal/services/dns_record_service_test.go` (core behaviors; write fakes for repo/domain/applier/infra following existing service-test fakes):
```go
package services

import (
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

func TestEnable_NoActiveCredential_Errors(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.activeCredOK = false
	_, err := svc.Enable(d.domainID, d.userID, DNSRecordInput{})
	if err == nil {
		t.Fatal("expected ErrNoActiveDNSCredential")
	}
}

func TestEnable_CredentialMismatch_Errors(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	other := randomUUID()
	_, err := svc.Enable(d.domainID, d.userID, DNSRecordInput{ProviderCredentialID: &other})
	if err == nil {
		t.Fatal("expected mismatch error (Review Focus #3)")
	}
}

func TestEnable_NoGatewayAddress_Pending(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.gatewayAddresses = nil // no address yet
	rec, err := svc.Enable(d.domainID, d.userID, DNSRecordInput{})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if rec.Status != models.DNSRecordStatusPending {
		t.Fatalf("status = %s, want pending (Review Focus #1)", rec.Status)
	}
	if d.applier.appliedCount(DNSEndpointGVRResource()) != 0 {
		t.Fatal("must not create a target-less DNSEndpoint")
	}
}

func TestEnable_WithAddress_SyncingThenReady(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.setGatewayIP("203.0.113.9")
	rec, err := svc.Enable(d.domainID, d.userID, DNSRecordInput{})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	// DNSEndpoint applied with the A target; first pass reports syncing
	if rec.Status != models.DNSRecordStatusSyncing && rec.Status != models.DNSRecordStatusReady {
		t.Fatalf("status = %s", rec.Status)
	}
	// make the fake return the applied endpoint on Get; a subsequent Get => ready
	d.applier.echoApplied = true
	got, _ := svc.Get(d.domainID)
	if got.Status != models.DNSRecordStatusReady {
		t.Fatalf("status after reconcile = %s, want ready", got.Status)
	}
	if got.ResolvedTarget != "203.0.113.9" {
		t.Fatalf("resolvedTarget = %q", got.ResolvedTarget)
	}
}

func TestEnable_ForcedCNAMEOnIP_Error(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.setGatewayIP("203.0.113.9")
	rec, _ := svc.Enable(d.domainID, d.userID, DNSRecordInput{RecordType: models.DNSRecordTypeCNAME})
	if rec.Status != models.DNSRecordStatusError {
		t.Fatalf("status = %s, want error (Review Focus #2)", rec.Status)
	}
}

func TestDelete_RemovesDNSEndpoint(t *testing.T) {
	svc, d := newTestDNSRecordService(t)
	d.setGatewayIP("203.0.113.9")
	_, _ = svc.Enable(d.domainID, d.userID, DNSRecordInput{})
	if err := svc.Delete(d.domainID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if d.applier.deletedCount(DNSEndpointGVRResource()) != 1 {
		t.Fatal("DNSEndpoint must be deleted (Review Focus #4)")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/services/ -run 'TestEnable|TestDelete_RemovesDNSEndpoint' 2>&1 | head`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement the service**

`internal/services/dns_record_service.go` — the core reconcile logic:
```go
package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/fastgateway-dev/backend-v2/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrNoActiveDNSCredential = errors.New("no active DNS credential is configured")
	ErrCredentialNotActive   = errors.New("providerCredentialId must equal the active DNS credential")
)

type DNSRecordService struct {
	repo       repository.DomainDNSRecordRepositoryInterface
	domainRepo repository.DomainRepositoryInterface
	infra      *DNSInfraService
	cp         CertInfraApplier
}

type DNSRecordServiceDeps struct {
	Repo       repository.DomainDNSRecordRepositoryInterface
	DomainRepo repository.DomainRepositoryInterface
	Infra      *DNSInfraService
	ControlPlane CertInfraApplier
}

func NewDNSRecordService(deps DNSRecordServiceDeps) *DNSRecordService {
	if deps.Repo == nil || deps.DomainRepo == nil || deps.Infra == nil || deps.ControlPlane == nil {
		panic("services.NewDNSRecordService: missing required dependency")
	}
	return &DNSRecordService{repo: deps.Repo, domainRepo: deps.DomainRepo, infra: deps.Infra, cp: deps.ControlPlane}
}

type DNSRecordInput struct {
	ProviderCredentialID *uuid.UUID
	RecordType           models.DNSRecordType
	TTL                  *int
	Proxied              bool
}

func (s *DNSRecordService) resolveActiveCredential(in DNSRecordInput) (uuid.UUID, error) {
	active, ok, err := s.infra.GetActiveCredentialID()
	if err != nil {
		return uuid.Nil, err
	}
	if !ok {
		return uuid.Nil, ErrNoActiveDNSCredential
	}
	if in.ProviderCredentialID != nil && *in.ProviderCredentialID != active {
		return uuid.Nil, ErrCredentialNotActive
	}
	return active, nil
}

func (s *DNSRecordService) Enable(domainID, createdBy uuid.UUID, in DNSRecordInput) (*models.DomainDNSRecord, error) {
	if _, err := s.repo.GetByDomainID(domainID); err == nil {
		return nil, errors.New("DNS record already exists for this domain")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	cred, err := s.resolveActiveCredential(in)
	if err != nil {
		return nil, err
	}
	rt := in.RecordType
	if rt == "" {
		rt = models.DNSRecordTypeAuto
	}
	rec := &models.DomainDNSRecord{
		DomainID: domainID, ProviderCredentialID: cred, RecordType: rt,
		TTL: in.TTL, Proxied: in.Proxied, Status: models.DNSRecordStatusPending, CreatedBy: createdBy,
	}
	rec.EndpointName = "dns-" + shortID(domainID) // see shortID note below
	if err := s.repo.Create(rec); err != nil {
		return nil, err
	}
	s.reconcile(rec) // best-effort; sets status + resolved_target, persists
	return rec, nil
}

func (s *DNSRecordService) Get(domainID uuid.UUID) (*models.DomainDNSRecord, error) {
	rec, err := s.repo.GetByDomainID(domainID)
	if err != nil {
		return nil, err
	}
	s.reconcile(rec)
	return rec, nil
}

func (s *DNSRecordService) Refresh(domainID uuid.UUID) (*models.DomainDNSRecord, error) {
	return s.Get(domainID)
}

func (s *DNSRecordService) Update(domainID uuid.UUID, in DNSRecordInput) (*models.DomainDNSRecord, error) {
	rec, err := s.repo.GetByDomainID(domainID)
	if err != nil {
		return nil, err
	}
	if _, err := s.resolveActiveCredential(in); err != nil {
		return nil, err
	}
	if in.RecordType != "" {
		rec.RecordType = in.RecordType
	}
	rec.TTL = in.TTL
	rec.Proxied = in.Proxied
	if err := s.repo.Update(rec); err != nil {
		return nil, err
	}
	s.reconcile(rec)
	return rec, nil
}

func (s *DNSRecordService) Delete(domainID uuid.UUID) error {
	rec, err := s.repo.GetByDomainID(domainID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	// Delete the DNSEndpoint first so external-dns removes the record.
	_ = s.cp.Delete(context.Background(), kubernetes.DNSEndpointGVR, rec.EndpointName, true)
	return s.repo.DeleteByDomainID(domainID)
}

// reconcile resolves the gateway address, builds/applies or clears the
// DNSEndpoint, and sets status + resolved_target. Never returns an error; it
// records failure in the record's status (status-on-read, best-effort).
func (s *DNSRecordService) reconcile(rec *models.DomainDNSRecord) {
	setErr := func(msg string) {
		rec.Status = models.DNSRecordStatusError
		rec.StatusMessage = msg
		_ = s.repo.Update(rec)
	}
	domain, err := s.domainRepo.GetByID(rec.DomainID)
	if err != nil {
		setErr("domain not found")
		return
	}
	gw, err := s.cp.Get(context.Background(), kubernetes.GatewayGVR, gatewayNameForDomain(domain), true)
	if err != nil {
		rec.Status = models.DNSRecordStatusPending
		rec.StatusMessage = "waiting for the gateway to be created"
		_ = s.repo.Update(rec)
		return
	}
	addr, ok := resolveGatewayAddress(gw)
	if !ok {
		rec.Status = models.DNSRecordStatusPending
		rec.StatusMessage = "waiting for the gateway load-balancer address"
		_ = s.repo.Update(rec)
		return
	}
	rt, err := recordTypeForAddress(addr, rec.RecordType)
	if err != nil {
		setErr(err.Error())
		return
	}
	ep := kubernetes.DNSEndpoint(kubernetes.DNSEndpointConfig{
		Name: rec.EndpointName, Hostname: domain.Hostname, RecordType: string(rt),
		Targets: []string{addr.Value}, TTL: rec.TTL, CloudflareProxied: rec.Proxied,
	})
	if err := s.cp.ApplyNamespaced(context.Background(), kubernetes.DNSEndpointGVR, ep); err != nil {
		setErr(fmt.Sprintf("failed to apply DNSEndpoint: %v", err))
		return
	}
	rec.ResolvedTarget = addr.Value
	// Readiness rule: ready when the live DNSEndpoint carries the desired target.
	if s.endpointMatches(rec.EndpointName, domain.Hostname, addr.Value) {
		rec.Status = models.DNSRecordStatusReady
		rec.StatusMessage = "record submitted to external-dns"
	} else {
		rec.Status = models.DNSRecordStatusSyncing
		rec.StatusMessage = "applying record via external-dns"
	}
	_ = s.repo.Update(rec)
}

func (s *DNSRecordService) endpointMatches(name, hostname, target string) bool {
	obj, err := s.cp.Get(context.Background(), kubernetes.DNSEndpointGVR, name, true)
	if err != nil {
		return false
	}
	eps, found, _ := unstructuredNestedSlice(obj, "spec", "endpoints")
	if !found {
		return false
	}
	for _, e := range eps {
		m, _ := e.(map[string]interface{})
		dn, _ := m["dnsName"].(string)
		if dn != hostname {
			continue
		}
		targets, _, _ := unstructuredNestedSliceFrom(m, "targets")
		for _, tgt := range targets {
			if ts, _ := tgt.(string); ts == target {
				return true
			}
		}
	}
	return false
}
```
Notes for the implementer:
- `shortID(domainID)`: use the domain record's own `rec.ID` (available after `Create`) instead — change `EndpointName` assignment to after `repo.Create` using `rec.ID.String()` so the name is `"dns-"+rec.ID.String()` per the Global Constraint. Adjust the Enable ordering accordingly (create, then set EndpointName, then Update once in reconcile).
- `gatewayNameForDomain(domain)`: reuse the exact Gateway name `applyGateway`/`domainplan.BuildGatewayConfig` uses — find it (`grep -n "Name" internal/domainplan/*.go` / how CreateGateway names the Gateway) and call the same helper or replicate the naming. Do NOT invent a new name.
- `unstructuredNestedSlice`/`unstructuredNestedSliceFrom`: thin wrappers over `k8s.io/apimachinery/.../unstructured` `NestedSlice`; define locally or inline `unstructured.NestedSlice(obj.Object, path...)`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/services/ -run 'TestEnable|TestDelete_RemovesDNSEndpoint|TestUpdate' -v`
Expected: PASS (covers Review Focus #1,#2,#3,#4).

- [ ] **Step 5: Commit**

```bash
git add internal/services/dns_record_service.go internal/services/dns_record_service_test.go
git commit -m "feat(dns): domain DNS record service with lazy gateway resolution"
```

---

## Task 9: HTTP handlers, routes, permissions, OpenAPI

**Files:**
- Create: `internal/handlers/dns_record_handler.go`
- Create: `internal/handlers/dns_active_credential_handler.go` (owner: GET/PUT active credential)
- Modify: `internal/handlers/service_interfaces.go` (add handler-facing service interfaces)
- Modify: `cmd/server/main.go` (construct services/handlers; register routes; nil-guard)
- Create: `docs/openapi/paths/dns-records.yaml`, `docs/openapi/schemas/dns-record.yaml`; Modify `docs/openapi/openapi.yaml` (add `$ref`s); Modify `docs/openapi/paths/dns-credentials.yaml` (add the active-credential path or a new `paths/dns-settings.yaml`)
- Test: `internal/handlers/dns_record_handler_test.go`

**Interfaces:**
- Consumes: `DNSRecordService` (Task 8), `DNSInfraService` (Task 7), `middleware.PermissionChecker.CanManageDomains(projectID, user)` (confirm the exact method name — `grep CanManage internal/middleware/`), `AuditServiceInterface`.
- Produces: REST endpoints from spec §6.

- [ ] **Step 1: Write the failing handler test**

`internal/handlers/dns_record_handler_test.go` — follow the gin + mock-service test style already in `managed_certificate_handler_test.go` (set up a router, inject a mock service + a permission checker that returns true, assert status codes). Cover: GET 404 when absent, POST 403 without `canManageDomains`, POST 400 on credential mismatch (mock service returns `ErrCredentialNotActive` → handler maps to 400), DELETE 204.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/handlers/ -run TestDNSRecordHandler 2>&1 | head`
Expected: FAIL.

- [ ] **Step 3: Implement handler + DTO**

`internal/handlers/dns_record_handler.go` — mirror `managed_certificate_handler.go`: parse `projectId`/`domainId`, permission check, `ShouldBindJSON`, call the service, map errors (`ErrNoActiveDNSCredential`/`ErrCredentialNotActive` → 400, `gorm.ErrRecordNotFound` → 404), audit-log, return a `dnsRecordResponse` DTO (expose all `DomainDNSRecord` json fields; nothing is sensitive here). Request body:
```go
type dnsRecordRequest struct {
	ProviderCredentialID *string `json:"providerCredentialId"`
	RecordType           string  `json:"recordType"`
	TTL                  *int    `json:"ttl"`
	Proxied              bool    `json:"proxied"`
}
```

`internal/handlers/dns_active_credential_handler.go` — owner-only GET/PUT; PUT body `{ "credentialId": "<uuid>" }` → `DNSInfraService.SetActiveCredential`; GET returns `{ "credentialId": "<uuid>|null" }`.

- [ ] **Step 4: Register routes + wire DI in main.go**

In `cmd/server/main.go`, in the in-cluster block (next to the managed-cert wiring), construct:
```go
dnsInfraService := services.NewDNSInfraService(services.DNSInfraServiceDeps{
	Creds: dnsCredentialService, Settings: systemSettingsService, ControlPlane: controlPlane,
})
dnsRecordService := services.NewDNSRecordService(services.DNSRecordServiceDeps{
	Repo: domainDNSRecordRepo, DomainRepo: domainRepo, Infra: dnsInfraService, ControlPlane: controlPlane,
})
dnsRecordHandler = handlers.NewDNSRecordHandler(dnsRecordService, permChecker, auditService)
dnsActiveCredHandler = handlers.NewDNSActiveCredentialHandler(dnsInfraService)
```
(`domainDNSRecordRepo := repository.NewDomainDNSRecordRepository(db)` near the other repos; `systemSettingsService` = the real settings service you located in Task 7.)

Add handler fields to the `Dependencies` struct and register routes:
```go
// owner-only active credential, next to /dns/credentials
dnsSettings := protected.Group("/dns/settings")
dnsSettings.Use(deps.AuthMiddleware.RequireRole("owner"))
{
	dnsSettings.GET("/active-credential", deps.DNSActiveCredentialHandler.Get)
	dnsSettings.PUT("/active-credential", deps.DNSActiveCredentialHandler.Set)
}
// project-scoped record, nested under the existing domains group, nil-guarded
if deps.DNSRecordHandler != nil {
	dnsRec := domains.Group("/:domainId/dns-record")
	dnsRec.Use(deps.PermChecker.RequireProjectAccess())
	{
		dnsRec.GET("", deps.DNSRecordHandler.Get)
		dnsRec.POST("", deps.DNSRecordHandler.Enable)
		dnsRec.PUT("", deps.DNSRecordHandler.Update)
		dnsRec.DELETE("", deps.DNSRecordHandler.Delete)
		dnsRec.POST("/refresh", deps.DNSRecordHandler.Refresh)
	}
}
```
(Confirm the existing `domains` group variable name and that `:domainId` is its param — reuse exactly what the domain routes use.)

- [ ] **Step 5: OpenAPI**

Add `docs/openapi/schemas/dns-record.yaml` (DomainDNSRecord + request) and `docs/openapi/paths/dns-records.yaml` (the five record paths + the two active-credential paths), following the exact shape in `paths/dns-credentials.yaml` (digest §10). Add their `$ref` entries to `docs/openapi/openapi.yaml`. Then:
```bash
make openapi
```

- [ ] **Step 6: Run tests + openapi check**

Run: `go test ./internal/handlers/ -run TestDNSRecordHandler -v && go build ./... && make openapi-check`
Expected: PASS; `make openapi-check` clean (no diff).

- [ ] **Step 7: Commit**

```bash
git add internal/handlers/dns_record_handler.go internal/handlers/dns_active_credential_handler.go internal/handlers/service_interfaces.go internal/handlers/dns_record_handler_test.go cmd/server/main.go docs/openapi cmd/server/openapi.yaml
git commit -m "feat(dns): DNS record + active-credential API endpoints"
```

---

## Task 10: Auto-create at domain creation

**Files:**
- Modify: `internal/services/domain_service.go` (`CreateDomainInput` + `Create` tail)
- Modify: `cmd/server/main.go` (inject `DNSRecordService` into `DomainService`, nil-safe)
- Test: `internal/services/domain_service_test.go` (extend)

**Interfaces:**
- Consumes: `DNSRecordService.Enable` (Task 8).
- Produces: `CreateDomainInput.DNS *DomainDNSInput`; best-effort record creation after Gateway create.

- [ ] **Step 1: Write the failing test**

Append to `internal/services/domain_service_test.go` (reuse `newTestDomainService` — digest/summary notes it returns the domain repo + mocks; add a DNS record service mock):
```go
func TestCreateDomain_WithDNS_EnablesRecord(t *testing.T) {
	svc, _, _, _, k8sMock, _, _ := newTestDomainService() // existing signature
	dnsMock := &mockDNSEnabler{}
	svc.dnsRecords = dnsMock // optional dependency field
	// ... set up a TLS-less template + CreateGateway mock as other create tests do,
	// with Namespace: kubernetes.FastGatewayNamespace to skip reference grants ...
	cred := randomUUID()
	_, err := svc.Create(projectID, &CreateDomainInput{
		Name: "d", Hostname: "app.example.com", DomainTemplateID: tmplID.String(),
		Namespace: kubernetes.FastGatewayNamespace,
		DNS:       &DomainDNSInput{Enabled: true, ProviderCredentialID: cred.String()},
	}, userID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !dnsMock.enableCalled {
		t.Fatal("expected DNS record Enable to be called")
	}
}
```
Define `mockDNSEnabler` with an `Enable(domainID, createdBy uuid.UUID, in DNSRecordInput) (*models.DomainDNSRecord, error)` method recording the call.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/services/ -run TestCreateDomain_WithDNS 2>&1 | head`
Expected: FAIL — `DNS` field + `dnsRecords` dependency undefined.

- [ ] **Step 3: Implement**

In `internal/services/domain_service.go`:
```go
type DomainDNSInput struct {
	Enabled              bool   `json:"enabled"`
	ProviderCredentialID string `json:"providerCredentialId"`
	RecordType           string `json:"recordType"`
	TTL                  *int   `json:"ttl"`
	Proxied              bool   `json:"proxied"`
}

// add to CreateDomainInput:
//   DNS *DomainDNSInput `json:"dns,omitempty"`
```
Add an optional dependency to `DomainService`:
```go
type DNSRecordEnabler interface {
	Enable(domainID, createdBy uuid.UUID, in DNSRecordInput) (*models.DomainDNSRecord, error)
}
// field: dnsRecords DNSRecordEnabler  // may be nil (outside cluster)
```
In the `Create` tail, AFTER the successful Gateway-create block (where status is set Active), before `return domain, nil`:
```go
if input.DNS != nil && input.DNS.Enabled && s.dnsRecords != nil {
	in := DNSRecordInput{RecordType: models.DNSRecordType(input.DNS.RecordType), TTL: input.DNS.TTL, Proxied: input.DNS.Proxied}
	if input.DNS.ProviderCredentialID != "" {
		if id, err := uuid.Parse(input.DNS.ProviderCredentialID); err == nil {
			in.ProviderCredentialID = &id
		}
	}
	if _, err := s.dnsRecords.Enable(domain.ID, createdBy, in); err != nil {
		log.Printf("Failed to enable DNS record for domain %s: %v", domain.ID, err)
		// best-effort: the domain succeeds; the DNS record can be enabled later
	}
}
```
Wire `dnsRecords` in `cmd/server/main.go` where `DomainService` is constructed (only in the in-cluster block; leave nil otherwise). Add a setter or a deps field as the DomainService constructor pattern requires.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/services/ -run 'TestCreateDomain' -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: OpenAPI (domain create body) + commit**

Add the optional `dns` object to the domain-create request schema in `docs/openapi/schemas/*` (where CreateDomain lives), `make openapi`, then:
```bash
git add internal/services/domain_service.go cmd/server/main.go internal/services/domain_service_test.go docs/openapi cmd/server/openapi.yaml
git commit -m "feat(dns): optional auto-create DNS record at domain creation"
```

---

## Task 11: Frontend — API client, types, status badge

**Repo:** frontend-v2.

**Files:**
- Create: `src/lib/api/dns-records.ts`
- Modify: `src/types/index.ts` (add DNS record types; make credential input provider-aware)
- Create: `src/lib/utils/dns.ts` (status badge)
- Modify: `src/lib/api/dns-credentials.ts` (add active-credential get/set)

**Interfaces:**
- Produces: `dnsRecordsApi`, `DomainDNSRecord`/`DNSRecordStatus`/`DNSRecordType`/`DNSRecordInput` types, `dnsRecordStatusBadge`, provider-aware `CreateDNSCredentialInput`.

- [ ] **Step 1: Types**

In `src/types/index.ts` add:
```ts
export type DNSRecordStatus = 'pending' | 'syncing' | 'ready' | 'error';
export type DNSRecordType = 'auto' | 'A' | 'AAAA' | 'CNAME';

export interface DomainDNSRecord {
  id: string;
  domainId: string;
  providerCredentialId: string;
  recordType: DNSRecordType;
  ttl?: number;
  proxied: boolean;
  resolvedTarget?: string;
  status: DNSRecordStatus;
  statusMessage?: string;
  endpointName?: string;
  createdAt: string;
  updatedAt: string;
}

export interface DNSRecordInput {
  providerCredentialId?: string;
  recordType?: DNSRecordType;
  ttl?: number;
  proxied?: boolean;
}
```
Replace the single-provider `CreateDNSCredentialInput`/`UpdateDNSCredentialInput` with a provider-aware shape (keep backward compatible by using a free-form credentials map):
```ts
export interface CreateDNSCredentialInput { name: string; providerType: string; credentials: Record<string, string>; }
export interface UpdateDNSCredentialInput { name?: string; credentials?: Record<string, string>; }
```
Add `dns?: DNSRecordInput & { enabled: boolean }` is NOT added to `CreateDomainInput` here — the create wizard builds the `dns` object inline (Task 14); but add the optional field to the type:
```ts
// in CreateDomainInput:
//   dns?: { enabled: boolean; providerCredentialId?: string; recordType?: DNSRecordType; ttl?: number; proxied?: boolean };
```

- [ ] **Step 2: API client**

`src/lib/api/dns-records.ts`:
```ts
import apiClient from './client';
import type { DomainDNSRecord, DNSRecordInput } from '@/types';

export const dnsRecordsApi = {
  get: async (projectId: string, domainId: string): Promise<DomainDNSRecord> => {
    const r = await apiClient.get<DomainDNSRecord>(`/projects/${projectId}/domains/${domainId}/dns-record`);
    return r.data;
  },
  enable: async (projectId: string, domainId: string, data: DNSRecordInput): Promise<DomainDNSRecord> => {
    const r = await apiClient.post<DomainDNSRecord>(`/projects/${projectId}/domains/${domainId}/dns-record`, data);
    return r.data;
  },
  update: async (projectId: string, domainId: string, data: DNSRecordInput): Promise<DomainDNSRecord> => {
    const r = await apiClient.put<DomainDNSRecord>(`/projects/${projectId}/domains/${domainId}/dns-record`, data);
    return r.data;
  },
  remove: async (projectId: string, domainId: string): Promise<void> => {
    await apiClient.delete(`/projects/${projectId}/domains/${domainId}/dns-record`);
  },
  refresh: async (projectId: string, domainId: string): Promise<DomainDNSRecord> => {
    const r = await apiClient.post<DomainDNSRecord>(`/projects/${projectId}/domains/${domainId}/dns-record/refresh`, {});
    return r.data;
  },
};
```
In `src/lib/api/dns-credentials.ts` add:
```ts
getActiveCredential: async (): Promise<{ credentialId: string | null }> => {
  const r = await apiClient.get<{ credentialId: string | null }>('/dns/settings/active-credential');
  return r.data;
},
setActiveCredential: async (credentialId: string): Promise<void> => {
  await apiClient.put('/dns/settings/active-credential', { credentialId });
},
```

- [ ] **Step 3: Status badge**

`src/lib/utils/dns.ts`:
```ts
import type { DNSRecordStatus } from '@/types';

type BadgeVariant = 'default' | 'success' | 'warning' | 'error' | 'info';
export interface BadgeSpec { label: string; variant: BadgeVariant; }

export function dnsRecordStatusBadge(s: DNSRecordStatus): BadgeSpec {
  switch (s) {
    case 'ready': return { label: 'Ready', variant: 'success' };
    case 'syncing': return { label: 'Syncing', variant: 'info' };
    case 'error': return { label: 'Error', variant: 'error' };
    default: return { label: 'Pending', variant: 'warning' };
  }
}
```

- [ ] **Step 4: Verify**

Run: `cd /Users/zufardhiyaulhaq/Documents/personal/github/frontend-v2 && npx tsc --noEmit 2>&1 | grep -v "Cannot find namespace 'JSX'" | head`
Expected: no new errors.

- [ ] **Step 5: Commit**

```bash
git add src/lib/api/dns-records.ts src/lib/api/dns-credentials.ts src/lib/utils/dns.ts src/types/index.ts
git commit -m "feat(dns): frontend api client, types, and status badge"
```

---

## Task 12: Frontend — provider-aware credential form

**Repo:** frontend-v2.

**Files:**
- Modify: `src/app/(authenticated)/dns-credentials/page.tsx`

**Interfaces:**
- Consumes: `dnsCredentialsApi`, `CreateDNSCredentialInput` (provider-aware).

- [ ] **Step 1: Make the form fields switch by provider**

Replace the single-provider `providerOptions`/`apiToken` form with a per-provider field map and build the `credentials` record dynamically:
```tsx
const PROVIDERS: Record<string, { label: string; fields: { key: string; label: string; type?: string }[] }> = {
  cloudflare: { label: 'Cloudflare', fields: [{ key: 'apiToken', label: 'API Token', type: 'password' }] },
  route53:    { label: 'AWS Route53', fields: [{ key: 'accessKeyId', label: 'Access Key ID' }, { key: 'secretAccessKey', label: 'Secret Access Key', type: 'password' }] },
  google:     { label: 'Google Cloud DNS', fields: [{ key: 'serviceAccountKey', label: 'Service Account JSON', type: 'password' }, { key: 'project', label: 'Project ID' }] },
  azure:      { label: 'Azure DNS', fields: [{ key: 'tenantId', label: 'Tenant ID' }, { key: 'subscriptionId', label: 'Subscription ID' }, { key: 'resourceGroup', label: 'Resource Group' }, { key: 'clientId', label: 'Client ID' }, { key: 'clientSecret', label: 'Client Secret', type: 'password' }] },
};
```
Render a `<Select>` for `providerType` with `Object.entries(PROVIDERS).map(([v, p]) => ({ value: v, label: p.label }))`, then render one `<Input>` per `PROVIDERS[providerType].fields`, tracking values in a `Record<string, string>` state. On submit, send `{ name, providerType, credentials: fieldValues }`. On edit, show empty secret inputs with the "leave blank to keep" hint (only send changed/filled values).

- [ ] **Step 2: Verify**

Run: `npx tsc --noEmit 2>&1 | grep -v "Cannot find namespace 'JSX'" | head` and `npx eslint src/app/\(authenticated\)/dns-credentials/page.tsx`
Expected: 0 tsc errors; no new eslint errors.

- [ ] **Step 3: Commit**

```bash
git add "src/app/(authenticated)/dns-credentials/page.tsx"
git commit -m "feat(dns): provider-aware DNS credential form (cloudflare/route53/google/azure)"
```

---

## Task 13: Frontend — DNS Record section (view + edit)

**Repo:** frontend-v2.

**Files:**
- Modify: `src/app/projects/[projectId]/domains/[domainId]/page.tsx` (read-only accordion item `dns-record`)
- Modify: `src/app/projects/[projectId]/domains/[domainId]/settings/page.tsx` (editable accordion item `dns-record` + its own actions)

**Interfaces:**
- Consumes: `dnsRecordsApi`, `dnsCredentialsApi.getActiveCredential`, `dnsRecordStatusBadge`.

- [ ] **Step 1: Read-only section on the detail page**

Add `'dns-record'` to the `defaultValue` array of the Settings-tab Accordion, and add an `AccordionItem value="dns-record"` (next to `tls-certificate`), gated on `domain?.tlsMode !== 'no_tls'`, that loads the record via `dnsRecordsApi.get(...)` (catch 404 → "Not managed by FastGateway"), rendering hostname, type, `resolvedTarget`, provider, TTL, proxied, and `<Badge variant={dnsRecordStatusBadge(rec.status).variant}>{...label}</Badge>` + `statusMessage` on error. Follow the exact read-only `tls-certificate` JSX skeleton from the frontend digest §5.

- [ ] **Step 2: Editable section on the Edit Settings page**

Add `'dns-record'` to that page's Accordion `defaultValue`, and an `AccordionItem value="dns-record"` with its OWN state + handlers (not the settings-wide save), mirroring the `tls-certificate` section's independent-action pattern (digest §5):
- Load the active credential (`dnsCredentialsApi.getActiveCredential`) and the current record (`dnsRecordsApi.get`, 404-tolerant).
- Controls: record type `<Select>` (Auto/A/AAAA/CNAME), TTL `<Input type="number">`, proxied checkbox.
- Buttons: **Enable** (if no record) → `dnsRecordsApi.enable`; **Save** → `update`; **Refresh** → `refresh`; **Delete** → `remove`. Each with its own loading/error/success state like `handleUpdateCertificate`.
- If no active credential is set, show a disabled state with a hint pointing owners to DNS Credentials.

- [ ] **Step 3: Verify**

Run: `npx tsc --noEmit 2>&1 | grep -v "Cannot find namespace 'JSX'" | head` and eslint on both files.
Expected: 0 tsc errors; no new eslint errors. Build the app (`npm run build`) to confirm both routes compile.

- [ ] **Step 4: Commit**

```bash
git add "src/app/projects/[projectId]/domains/[domainId]/page.tsx" "src/app/projects/[projectId]/domains/[domainId]/settings/page.tsx"
git commit -m "feat(dns): DNS record section on domain view and edit settings"
```

---

## Task 14: Frontend — auto-create toggle in the create wizard

**Repo:** frontend-v2.

**Files:**
- Modify: `src/app/projects/[projectId]/domains/create/page.tsx`

**Interfaces:**
- Consumes: `dnsCredentialsApi.getActiveCredential`, `CreateDomainInput.dns`.

- [ ] **Step 1: Add the DNS block + payload assembly**

Gated on `needsTLS` (or any hostname domain — use `selectedTemplate && selectedTemplate.tlsMode !== 'no_tls'` consistent with the TLS block), add a DNS section with: a toggle `dnsEnabled` ("Automatically create the DNS record for this domain", default **off**); when on, a record-type `<Select>` (Auto/A/AAAA/CNAME), TTL `<Input>`, proxied checkbox, and helper text naming the hostname. Load the active credential on mount; if none is set, show the toggle disabled with a hint.

In `buildInput()`, append:
```ts
...(dnsEnabled ? { dns: { enabled: true, recordType: dnsRecordType, ttl: dnsTtl || undefined, proxied: dnsProxied } } : {}),
```
(`providerCredentialId` is omitted so the backend uses the active credential.)

- [ ] **Step 2: Verify**

Run: `npx tsc --noEmit 2>&1 | grep -v "Cannot find namespace 'JSX'" | head`; eslint the file; `npm run build`.
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add "src/app/projects/[projectId]/domains/create/page.tsx"
git commit -m "feat(dns): auto-create DNS record toggle in domain creation wizard"
```

---

## Task 15: Helm chart — RBAC gated on `dns.enabled`

**Repo:** helm-chart (`/Users/zufardhiyaulhaq/Documents/personal/github/fastgateway.dev/helm-chart`).

**Files:**
- Modify: `fastgateway/values.yaml` (add `dns: { enabled: false }`)
- Modify: `fastgateway/templates/rbac.yaml` (gated rules)
- Modify: `fastgateway/README.md` via `make readme`

**Interfaces:** cluster RBAC for the backend ServiceAccount.

- [ ] **Step 1: Add the value**

In `fastgateway/values.yaml`:
```yaml
# DNS management (external-dns integration). Enable only if external-dns is
# installed and you want FastGateway to manage domain DNS records.
dns:
  enabled: false
```

- [ ] **Step 2: Add gated RBAC**

In `fastgateway/templates/rbac.yaml`, append (inside the existing ClusterRole/Role rules, following the file's existing structure):
```yaml
{{- if .Values.dns.enabled }}
# DNS management: DNSEndpoint CRs + the external-dns credential Secret + Gateway status
- apiGroups: ["externaldns.k8s.io"]
  resources: ["dnsendpoints"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["externaldns.k8s.io"]
  resources: ["dnsendpoints/status"]
  verbs: ["get"]
{{- end }}
```
(The backend already has `secrets` verbs and `gateways` read from earlier work — confirm `gateways` includes status read; `gateways/status` get may need adding here too if not already present. Grep the file first.)

- [ ] **Step 3: Regenerate README + lint template**

Run: `cd /Users/zufardhiyaulhaq/Documents/personal/github/fastgateway.dev/helm-chart && helm template fastgateway ./fastgateway --set dns.enabled=true | grep -A2 dnsendpoints && make readme`
Expected: the DNSEndpoint rule renders when enabled; absent when `dns.enabled=false`.

- [ ] **Step 4: Commit (WITH Claude trailer — helm-chart convention)**

```bash
git add fastgateway/values.yaml fastgateway/templates/rbac.yaml fastgateway/README.md README.md
git commit -m "feat(dns): RBAC for external-dns DNSEndpoints gated on dns.enabled

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```
(Do NOT repackage/bump the chart version here — that happens in the coordinated release after the backend/frontend images ship, as in prior releases.)

---

## Task 16: Docs — external-dns prerequisite + DNS feature page

**Repo:** site (`/Users/zufardhiyaulhaq/Documents/personal/github/fastgateway-site/fastgateway.dev`).

**Files:**
- Create: `docs/<section>/dns-management.md` (place beside the certificate-management doc — `grep -rl "certificate" docs/ | head` to find the section)
- Modify: the compatibility/prerequisites doc to list external-dns as an optional prerequisite with the four providers and the exact `--source=crd` / `--policy=sync` / `--txt-owner-id=fastgateway` flags (spec §7.1).

- [ ] **Step 1: Write the doc**

Cover: what it does (one managed record per domain → gateway), enabling it (`dns.enabled=true` + install external-dns pointed at the `fgw-externaldns-credentials` Secret with `--source=crd --policy=sync --txt-owner-id=fastgateway --provider=<x>`), setting the active DNS credential (owner), the four providers and their credential fields, auto-create at domain creation, and the Pending→Syncing→Ready status meaning (Ready = submitted to external-dns).

- [ ] **Step 2: Build**

Run: `npm run build`
Expected: `[SUCCESS] Generated static files`.

- [ ] **Step 3: Commit (WITH Claude trailer — site convention)**

```bash
git add docs/
git commit -m "docs: DNS management (external-dns) guide

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage** — every spec section maps to a task:
- §3.1/3.2 write path → Tasks 4, 8. §3.3 target resolution → Tasks 5, 8. §3.4 record type → Task 5. §3.5 status → Task 8. §4 provider abstraction → Task 2. §4.1 credential schemas → Tasks 2, 3, 12. §4.2 secret rendering → Tasks 2, 7. §4.3 active credential → Tasks 7, 9. §5 data model → Task 1. §6 API + create block → Tasks 9, 10. §7 external-dns/RBAC/chart → Task 15 (+ §7.1 docs in Task 16). §9 UX → Tasks 13, 14 (credentials UI §9 last bullet → Task 12). §10 error/edge → distributed across Tasks 5, 8, 10. §11 testing → every task's tests. §12 future extensions → out of scope (correctly).
- Gap check: the `fingerprint`/export concepts of managed certs do NOT apply — intentionally omitted.

**2. Placeholder scan** — no "TBD"/"handle errors"/"similar to Task N". Two deliberate "locate the existing X then follow it" steps (Task 6 repo-test helper, Task 7 system-settings API) are grounded with the exact grep to run and the exact interface to produce — not placeholders, because the precedent exists in-repo and the produced signatures are fully specified. The Gateway-name helper (Task 8) and the `shortID→rec.ID` correction are called out explicitly rather than guessed.

**3. Type consistency** — `DNSRecordStatus`/`DNSRecordType` identical in model (Task 1), service (Task 8), and frontend (Task 11). `DNSEndpoint`/`DNSEndpointConfig`/`ExternalDNSSecret`/GVRs (Task 4) consumed verbatim in Tasks 7, 8. `DNSRecordInput` shape consistent (Task 8) and mapped in Tasks 9, 10. `dnsprovider.SecretName` == `kubernetes.ExternalDNSSecretName` == `fgw-externaldns-credentials` == values Secret (Tasks 2, 4, 7, 15). Endpoint name `dns-<recordID>` consistent (Global Constraints, Task 8 with the rec.ID correction).

**4. Review Focus** — all five pinned: #1 empty gateway address → Task 5 `TestResolveGatewayAddress_None` + Task 8 `TestEnable_NoGatewayAddress_Pending`; #2 forced-type conflict → Task 5 `TestRecordTypeForAddress` + Task 8 `TestEnable_ForcedCNAMEOnIP_Error`; #3 credential mismatch/no-active → Task 8 `TestEnable_CredentialMismatch_Errors`/`TestEnable_NoActiveCredential_Errors` + Task 9 handler 400; #4 delete removes DNSEndpoint → Task 8 `TestDelete_RemovesDNSEndpoint`; #5 per-provider secret render → Task 2 `TestRenderSecret_PerProvider` + Task 7 render path.
