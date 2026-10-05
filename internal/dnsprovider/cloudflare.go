package dnsprovider

import (
	"context"
	"strings"

	cf "github.com/cloudflare/cloudflare-go"
)

func init() { register(cloudflare{}) }

type cloudflare struct{}

func (cloudflare) Type() string             { return "cloudflare" }
func (cloudflare) RequiredFields() []string { return []string{"apiToken"} }
func (c cloudflare) Validate(cred map[string]string) error {
	return requireFields(cred, c.RequiredFields())
}

func (c cloudflare) NewClient(cred map[string]string) (DNSClient, error) {
	api, err := cf.NewWithAPIToken(cred["apiToken"])
	if err != nil {
		return nil, err
	}
	return &cloudflareClient{api: api}, nil
}

// newClientWithBaseURL is a test seam: it builds the same client but points
// the underlying SDK at an arbitrary base URL (e.g. an httptest.Server),
// letting tests drive real SDK calls without a live Cloudflare account.
func (c cloudflare) newClientWithBaseURL(cred map[string]string, baseURL string) (DNSClient, error) {
	api, err := cf.NewWithAPIToken(cred["apiToken"], cf.BaseURL(baseURL))
	if err != nil {
		return nil, err
	}
	return &cloudflareClient{api: api}, nil
}

type cloudflareClient struct{ api *cf.API }

// FindZone returns the provider zone ID for the longest-suffix-matching zone
// name (e.g. zone "example.com" matches queries for "example.com" and
// "app.example.com").
func (c *cloudflareClient) FindZone(ctx context.Context, zoneName string) (string, bool, error) {
	zones, err := c.api.ListZones(ctx)
	if err != nil {
		return "", false, err
	}
	var bestName, bestID string
	for _, z := range zones {
		if (zoneName == z.Name || strings.HasSuffix(zoneName, "."+z.Name)) && len(z.Name) > len(bestName) {
			bestName, bestID = z.Name, z.ID
		}
	}
	if bestID == "" {
		return "", false, nil
	}
	return bestID, true, nil
}

func (c *cloudflareClient) GetRecord(ctx context.Context, providerZoneID, name, recordType string) (Record, bool, error) {
	records, _, err := c.api.ListDNSRecords(ctx, cf.ZoneIdentifier(providerZoneID), cf.ListDNSRecordsParams{
		Name: name,
		Type: recordType,
	})
	if err != nil {
		return Record{}, false, err
	}
	if len(records) == 0 {
		return Record{}, false, nil
	}
	return toRecord(records[0]), true, nil
}

func (c *cloudflareClient) UpsertRecord(ctx context.Context, providerZoneID string, r Record) error {
	existing, _, err := c.api.ListDNSRecords(ctx, cf.ZoneIdentifier(providerZoneID), cf.ListDNSRecordsParams{
		Name: r.Name,
		Type: r.Type,
	})
	if err != nil {
		return err
	}

	// Cloudflare requires TTL to be "automatic" (unset/1) for proxied
	// records; only pass through a custom TTL when the record isn't
	// proxied.
	ttl := 0
	if !r.Proxied && r.TTL != nil {
		ttl = *r.TTL
	}
	proxied := r.Proxied

	if len(existing) == 0 {
		_, err := c.api.CreateDNSRecord(ctx, cf.ZoneIdentifier(providerZoneID), cf.CreateDNSRecordParams{
			Type:    r.Type,
			Name:    r.Name,
			Content: r.Target,
			TTL:     ttl,
			Proxied: &proxied,
		})
		return err
	}

	_, err = c.api.UpdateDNSRecord(ctx, cf.ZoneIdentifier(providerZoneID), cf.UpdateDNSRecordParams{
		ID:      existing[0].ID,
		Type:    r.Type,
		Name:    r.Name,
		Content: r.Target,
		TTL:     ttl,
		Proxied: &proxied,
	})
	return err
}

func (c *cloudflareClient) DeleteRecord(ctx context.Context, providerZoneID, name, recordType string) error {
	records, _, err := c.api.ListDNSRecords(ctx, cf.ZoneIdentifier(providerZoneID), cf.ListDNSRecordsParams{
		Name: name,
		Type: recordType,
	})
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	return c.api.DeleteDNSRecord(ctx, cf.ZoneIdentifier(providerZoneID), records[0].ID)
}

func toRecord(rec cf.DNSRecord) Record {
	r := Record{
		Name:   rec.Name,
		Type:   rec.Type,
		Target: rec.Content,
	}
	if rec.TTL != 0 {
		ttl := rec.TTL
		r.TTL = &ttl
	}
	if rec.Proxied != nil {
		r.Proxied = *rec.Proxied
	}
	return r
}
