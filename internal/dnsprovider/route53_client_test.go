package dnsprovider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const route53ListHostedZonesXML = `<?xml version="1.0"?>
<ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <HostedZones>
    <HostedZone>
      <Id>/hostedzone/Z111111QQQQQQQ</Id>
      <Name>example.com.</Name>
      <CallerReference>ref1</CallerReference>
    </HostedZone>
  </HostedZones>
  <IsTruncated>false</IsTruncated>
  <MaxItems>100</MaxItems>
</ListHostedZonesResponse>`

const route53ChangeResponseXML = `<?xml version="1.0"?>
<ChangeResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ChangeInfo>
    <Id>/change/C1111111QQQQQQ</Id>
    <Status>PENDING</Status>
    <SubmittedAt>2024-01-01T00:00:00.000Z</SubmittedAt>
  </ChangeInfo>
</ChangeResourceRecordSetsResponse>`

func route53RRSetXML(found bool) string {
	if !found {
		return `<?xml version="1.0"?>
<ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ResourceRecordSets></ResourceRecordSets>
  <IsTruncated>false</IsTruncated>
  <MaxItems>1</MaxItems>
</ListResourceRecordSetsResponse>`
	}
	return `<?xml version="1.0"?>
<ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ResourceRecordSets>
    <ResourceRecordSet>
      <Name>app.example.com.</Name>
      <Type>A</Type>
      <TTL>300</TTL>
      <ResourceRecords>
        <ResourceRecord>
          <Value>203.0.113.5</Value>
        </ResourceRecord>
      </ResourceRecords>
    </ResourceRecordSet>
  </ResourceRecordSets>
  <IsTruncated>false</IsTruncated>
  <MaxItems>1</MaxItems>
</ListResourceRecordSetsResponse>`
}

func route53TestCreds() map[string]string {
	return map[string]string{"accessKeyId": "AKIATEST", "secretAccessKey": "secret"}
}

func TestRoute53Client_FindZoneAndUpsert(t *testing.T) {
	var sawUpsertAction string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/hostedzone") && r.Method == http.MethodGet:
			w.Write([]byte(route53ListHostedZonesXML))
		case strings.Contains(r.URL.Path, "/rrset") && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "<Action>UPSERT</Action>") {
				sawUpsertAction = "UPSERT"
			}
			w.Write([]byte(route53ChangeResponseXML))
		default:
			w.Write([]byte(route53ChangeResponseXML))
		}
	}))
	defer srv.Close()

	c, err := route53{}.newClientWithBaseURL(route53TestCreds(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	zid, found, err := c.FindZone(context.Background(), "example.com")
	if err != nil || !found || zid != "Z111111QQQQQQQ" {
		t.Fatalf("FindZone=%q,%v,%v", zid, found, err)
	}

	if err := c.UpsertRecord(context.Background(), zid, Record{Name: "app.example.com", Type: "A", Target: "203.0.113.5"}); err != nil {
		t.Fatalf("UpsertRecord: %v", err)
	}
	if sawUpsertAction != "UPSERT" {
		t.Fatal("expected UpsertRecord to issue ChangeResourceRecordSets with Action=UPSERT")
	}
}

func TestRoute53Client_FindZone_SuffixMatchAndNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(route53ListHostedZonesXML))
	}))
	defer srv.Close()

	c, err := route53{}.newClientWithBaseURL(route53TestCreds(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	zid, found, err := c.FindZone(context.Background(), "app.example.com")
	if err != nil || !found || zid != "Z111111QQQQQQQ" {
		t.Fatalf("FindZone(suffix)=%q,%v,%v", zid, found, err)
	}

	_, found, err = c.FindZone(context.Background(), "other.org")
	if err != nil || found {
		t.Fatalf("FindZone(nomatch) found=%v err=%v, want found=false", found, err)
	}
}

func TestRoute53Client_GetRecord_FoundAndNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") == "app.example.com" {
			w.Write([]byte(route53RRSetXML(true)))
			return
		}
		w.Write([]byte(route53RRSetXML(false)))
	}))
	defer srv.Close()

	c, err := route53{}.newClientWithBaseURL(route53TestCreds(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	rec, found, err := c.GetRecord(context.Background(), "zone123", "app.example.com", "A")
	if err != nil || !found {
		t.Fatalf("GetRecord found=%v err=%v", found, err)
	}
	if rec.Target != "203.0.113.5" || rec.TTL == nil || *rec.TTL != 300 {
		t.Fatalf("GetRecord rec=%+v", rec)
	}

	_, found, err = c.GetRecord(context.Background(), "zone123", "missing.example.com", "A")
	if err != nil || found {
		t.Fatalf("GetRecord(missing) found=%v err=%v, want false", found, err)
	}
}

func TestRoute53Client_DeleteRecord_AbsentIsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(route53RRSetXML(false)))
	}))
	defer srv.Close()

	c, err := route53{}.newClientWithBaseURL(route53TestCreds(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteRecord(context.Background(), "zone123", "missing.example.com", "A"); err != nil {
		t.Fatalf("DeleteRecord(absent) = %v, want nil", err)
	}
}

func TestRoute53Client_DeleteRecord_DeletesWhenPresentWithExactValues(t *testing.T) {
	var sawDeleteAction bool
	var sawExactValue bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/rrset") && r.Method == http.MethodGet:
			w.Write([]byte(route53RRSetXML(true)))
		case strings.Contains(r.URL.Path, "/rrset") && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			s := string(body)
			if strings.Contains(s, "<Action>DELETE</Action>") {
				sawDeleteAction = true
			}
			if strings.Contains(s, "<Value>203.0.113.5</Value>") && strings.Contains(s, "<TTL>300</TTL>") {
				sawExactValue = true
			}
			w.Write([]byte(route53ChangeResponseXML))
		default:
			w.Write([]byte(route53ChangeResponseXML))
		}
	}))
	defer srv.Close()

	c, err := route53{}.newClientWithBaseURL(route53TestCreds(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteRecord(context.Background(), "zone123", "app.example.com", "A"); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if !sawDeleteAction {
		t.Fatal("expected DeleteRecord to issue ChangeResourceRecordSets with Action=DELETE")
	}
	if !sawExactValue {
		t.Fatal("expected DeleteRecord to send the exact existing record values (Value/TTL)")
	}
}

func TestRoute53Client_RecordExistsForName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") == "app.example.com" {
			w.Write([]byte(route53RRSetXML(true)))
			return
		}
		w.Write([]byte(route53RRSetXML(false)))
	}))
	defer srv.Close()
	c, err := route53{}.newClientWithBaseURL(route53TestCreds(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	exists, err := c.RecordExistsForName(context.Background(), "zone123", "app.example.com")
	if err != nil || !exists {
		t.Fatalf("RecordExistsForName=%v,%v want true,nil", exists, err)
	}
	exists, err = c.RecordExistsForName(context.Background(), "zone123", "missing.example.com")
	if err != nil || exists {
		t.Fatalf("RecordExistsForName(missing)=%v,%v want false,nil", exists, err)
	}
}
