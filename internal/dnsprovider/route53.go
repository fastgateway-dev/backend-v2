package dnsprovider

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	r53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

func init() { register(route53{}) }

type route53 struct{}

func (route53) Type() string             { return "route53" }
func (route53) RequiredFields() []string { return []string{"accessKeyId", "secretAccessKey"} }
func (r route53) Validate(cred map[string]string) error {
	return requireFields(cred, r.RequiredFields())
}

func (route53) NewClient(cred map[string]string) (DNSClient, error) {
	return newRoute53Client(cred, "")
}

// newClientWithBaseURL is a test seam: it builds the same client but points
// the underlying SDK at an arbitrary base URL (e.g. an httptest.Server),
// letting tests drive real SDK calls without a live AWS account.
func (route53) newClientWithBaseURL(cred map[string]string, baseURL string) (DNSClient, error) {
	return newRoute53Client(cred, baseURL)
}

func newRoute53Client(cred map[string]string, baseURL string) (DNSClient, error) {
	region := cred["region"]
	if region == "" {
		region = "us-east-1"
	}
	creds := credentials.NewStaticCredentialsProvider(cred["accessKeyId"], cred["secretAccessKey"], "")

	opts := r53.Options{
		Region:      region,
		Credentials: creds,
	}
	if baseURL != "" {
		opts.BaseEndpoint = aws.String(baseURL)
	}
	return &route53Client{api: r53.New(opts)}, nil
}

type route53Client struct{ api *r53.Client }

// FindZone returns the provider zone ID for the longest-suffix-matching
// hosted zone name (e.g. zone "example.com" matches queries for
// "example.com" and "app.example.com"). Route53 zone names carry a trailing
// dot and the hosted zone ID carries a "/hostedzone/" prefix; both are
// stripped before comparing/returning.
func (c *route53Client) FindZone(ctx context.Context, zoneName string) (string, bool, error) {
	var marker *string
	var bestName, bestID string
	for {
		out, err := c.api.ListHostedZones(ctx, &r53.ListHostedZonesInput{Marker: marker})
		if err != nil {
			return "", false, err
		}
		for _, z := range out.HostedZones {
			name := strings.TrimSuffix(aws.ToString(z.Name), ".")
			if (zoneName == name || strings.HasSuffix(zoneName, "."+name)) && len(name) > len(bestName) {
				bestName = name
				bestID = strings.TrimPrefix(aws.ToString(z.Id), "/hostedzone/")
			}
		}
		if !out.IsTruncated || out.NextMarker == nil || *out.NextMarker == "" {
			break
		}
		marker = out.NextMarker
	}
	if bestID == "" {
		return "", false, nil
	}
	return bestID, true, nil
}

func (c *route53Client) GetRecord(ctx context.Context, providerZoneID, name, recordType string) (Record, bool, error) {
	out, err := c.api.ListResourceRecordSets(ctx, &r53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(providerZoneID),
		StartRecordName: aws.String(name),
		StartRecordType: r53types.RRType(recordType),
		MaxItems:        aws.Int32(1),
	})
	if err != nil {
		return Record{}, false, err
	}
	if len(out.ResourceRecordSets) == 0 {
		return Record{}, false, nil
	}
	rrs := out.ResourceRecordSets[0]
	rname := strings.TrimSuffix(aws.ToString(rrs.Name), ".")
	if rname != name || string(rrs.Type) != recordType {
		return Record{}, false, nil
	}
	return toRoute53Record(rrs), true, nil
}

func (c *route53Client) UpsertRecord(ctx context.Context, providerZoneID string, r Record) error {
	_, err := c.api.ChangeResourceRecordSets(ctx, &r53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(providerZoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{
				{
					Action:            r53types.ChangeActionUpsert,
					ResourceRecordSet: recordToRRS(r),
				},
			},
		},
	})
	return err
}

// DeleteRecord first reads the exact RRSet (Route53 requires the delete
// request to match the existing values precisely) and issues a DELETE with
// those values. An absent record is treated as success.
func (c *route53Client) DeleteRecord(ctx context.Context, providerZoneID, name, recordType string) error {
	rec, found, err := c.GetRecord(ctx, providerZoneID, name, recordType)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	_, err = c.api.ChangeResourceRecordSets(ctx, &r53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(providerZoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{
				{
					Action:            r53types.ChangeActionDelete,
					ResourceRecordSet: recordToRRS(rec),
				},
			},
		},
	})
	return err
}

func recordToRRS(r Record) *r53types.ResourceRecordSet {
	ttl := int64(300)
	if r.TTL != nil {
		ttl = int64(*r.TTL)
	}
	return &r53types.ResourceRecordSet{
		Name: aws.String(r.Name),
		Type: r53types.RRType(r.Type),
		TTL:  aws.Int64(ttl),
		ResourceRecords: []r53types.ResourceRecord{
			{Value: aws.String(r.Target)},
		},
	}
}

func toRoute53Record(rrs r53types.ResourceRecordSet) Record {
	r := Record{
		Name: strings.TrimSuffix(aws.ToString(rrs.Name), "."),
		Type: string(rrs.Type),
	}
	if len(rrs.ResourceRecords) > 0 {
		r.Target = aws.ToString(rrs.ResourceRecords[0].Value)
	}
	if rrs.TTL != nil {
		ttl := int(*rrs.TTL)
		r.TTL = &ttl
	}
	// Route53 has no concept of Proxied; leave it at its zero value (false).
	return r
}
