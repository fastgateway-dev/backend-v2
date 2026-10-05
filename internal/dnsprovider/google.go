package dnsprovider

import (
	"context"
	"strings"

	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/option"
)

func init() { register(google{}) }

type google struct{}

func (google) Type() string             { return "google" }
func (google) RequiredFields() []string { return []string{"serviceAccountKey", "project"} }
func (g google) Validate(cred map[string]string) error {
	return requireFields(cred, g.RequiredFields())
}

func (google) NewClient(cred map[string]string) (DNSClient, error) {
	svc, err := dns.NewService(context.Background(), option.WithCredentialsJSON([]byte(cred["serviceAccountKey"])))
	if err != nil {
		return nil, err
	}
	return &googleClient{svc: svc, project: cred["project"]}, nil
}

// newClientWithService is a test seam: it builds the same client around an
// already-constructed *dns.Service (e.g. one pointed at an httptest.Server
// via option.WithEndpoint + option.WithoutAuthentication), letting tests
// drive real SDK calls without a live service account.
func (google) newClientWithService(svc *dns.Service, project string) DNSClient {
	return &googleClient{svc: svc, project: project}
}

type googleClient struct {
	svc     *dns.Service
	project string
}

// FindZone returns the provider zone ID for the longest-suffix-matching
// managed zone name. Google's ManagedZone.DnsName carries a trailing dot
// (e.g. "example.com."); it is stripped before comparing. The returned
// providerZoneID is the managed zone's Name (not its numeric Id), since
// that's what GetRecord/UpsertRecord/DeleteRecord require as managedZone.
func (c *googleClient) FindZone(ctx context.Context, zoneName string) (string, bool, error) {
	var bestDnsName, bestZoneName string
	pageToken := ""
	for {
		call := c.svc.ManagedZones.List(c.project).Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		out, err := call.Do()
		if err != nil {
			return "", false, err
		}
		for _, z := range out.ManagedZones {
			dnsName := strings.TrimSuffix(z.DnsName, ".")
			if (zoneName == dnsName || strings.HasSuffix(zoneName, "."+dnsName)) && len(dnsName) > len(bestDnsName) {
				bestDnsName = dnsName
				bestZoneName = z.Name
			}
		}
		if out.NextPageToken == "" {
			break
		}
		pageToken = out.NextPageToken
	}
	if bestZoneName == "" {
		return "", false, nil
	}
	return bestZoneName, true, nil
}

func (c *googleClient) GetRecord(ctx context.Context, providerZoneID, name, recordType string) (Record, bool, error) {
	fqdn := name + "."
	out, err := c.svc.ResourceRecordSets.List(c.project, providerZoneID).Name(fqdn).Type(recordType).Context(ctx).Do()
	if err != nil {
		return Record{}, false, err
	}
	if len(out.Rrsets) == 0 {
		return Record{}, false, nil
	}
	return toGoogleRecord(out.Rrsets[0]), true, nil
}

// UpsertRecord creates or replaces the record via an atomic Changes.Create:
// the existing rrset (if any) is listed as a deletion alongside the new
// rrset as an addition. Google ignores r.Proxied; it has no such concept.
func (c *googleClient) UpsertRecord(ctx context.Context, providerZoneID string, r Record) error {
	fqdn := r.Name + "."
	existing, err := c.svc.ResourceRecordSets.List(c.project, providerZoneID).Name(fqdn).Type(r.Type).Context(ctx).Do()
	if err != nil {
		return err
	}

	ttl := int64(300)
	if r.TTL != nil {
		ttl = int64(*r.TTL)
	}
	// Google Cloud DNS requires CNAME rrdata to be a fully-qualified,
	// dot-terminated domain name and rejects a relative value. A/AAAA targets
	// are IP literals and must be left exactly as given.
	target := r.Target
	if r.Type == "CNAME" && !strings.HasSuffix(target, ".") {
		target += "."
	}
	newRRSet := &dns.ResourceRecordSet{
		Name:    fqdn,
		Type:    r.Type,
		Ttl:     ttl,
		Rrdatas: []string{target},
	}

	change := &dns.Change{Additions: []*dns.ResourceRecordSet{newRRSet}}
	if len(existing.Rrsets) > 0 {
		change.Deletions = []*dns.ResourceRecordSet{existing.Rrsets[0]}
	}

	_, err = c.svc.Changes.Create(c.project, providerZoneID, change).Context(ctx).Do()
	return err
}

// DeleteRecord reads the exact existing rrset (Google requires the deletion
// to match existing data exactly) and issues a Changes.Create with it as the
// only deletion. An absent record is treated as success.
func (c *googleClient) DeleteRecord(ctx context.Context, providerZoneID, name, recordType string) error {
	fqdn := name + "."
	existing, err := c.svc.ResourceRecordSets.List(c.project, providerZoneID).Name(fqdn).Type(recordType).Context(ctx).Do()
	if err != nil {
		return err
	}
	if len(existing.Rrsets) == 0 {
		return nil
	}

	change := &dns.Change{Deletions: []*dns.ResourceRecordSet{existing.Rrsets[0]}}
	_, err = c.svc.Changes.Create(c.project, providerZoneID, change).Context(ctx).Do()
	return err
}

func toGoogleRecord(rrs *dns.ResourceRecordSet) Record {
	r := Record{
		Name: strings.TrimSuffix(rrs.Name, "."),
		Type: rrs.Type,
	}
	if len(rrs.Rrdatas) > 0 {
		r.Target = rrs.Rrdatas[0]
	}
	if rrs.Ttl != 0 {
		ttl := int(rrs.Ttl)
		r.TTL = &ttl
	}
	// Google has no concept of Proxied; leave it at its zero value (false).
	return r
}
