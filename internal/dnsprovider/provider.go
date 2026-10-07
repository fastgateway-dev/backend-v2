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
	// RecordExistsForName reports whether any A, AAAA, or CNAME record exists at
	// name in the zone, regardless of type. Used by the create-time collision
	// check, where the eventual record type (auto -> A/AAAA/CNAME) is not yet known.
	RecordExistsForName(ctx context.Context, providerZoneID, name string) (bool, error)
}

type Record struct {
	Name    string
	Type    string // A | AAAA | CNAME
	Target  string
	TTL     *int
	Proxied bool
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
