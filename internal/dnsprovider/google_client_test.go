package dnsprovider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/option"
)

func newGoogleTestClient(t *testing.T, srv *httptest.Server) DNSClient {
	t.Helper()
	svc, err := dns.NewService(context.Background(), option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	return google{}.newClientWithService(svc, "proj1")
}

func TestGoogleClient_FindZoneAndUpsert(t *testing.T) {
	var sawCreate dns.Change
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/managedZones") && r.Method == http.MethodGet:
			w.Write([]byte(`{"managedZones":[{"name":"example-com","dnsName":"example.com."}]}`))
		case strings.Contains(r.URL.Path, "/rrsets") && r.Method == http.MethodGet:
			w.Write([]byte(`{"rrsets":[]}`))
		case strings.Contains(r.URL.Path, "/changes") && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &sawCreate); err != nil {
				t.Errorf("unmarshal change: %v", err)
			}
			w.Write([]byte(`{"id":"1","status":"done"}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	c := newGoogleTestClient(t, srv)

	zid, found, err := c.FindZone(context.Background(), "example.com")
	if err != nil || !found || zid != "example-com" {
		t.Fatalf("FindZone=%q,%v,%v", zid, found, err)
	}

	if err := c.UpsertRecord(context.Background(), zid, Record{Name: "app.example.com", Type: "A", Target: "203.0.113.5"}); err != nil {
		t.Fatalf("UpsertRecord: %v", err)
	}
	if len(sawCreate.Additions) != 1 {
		t.Fatalf("expected 1 addition, got %d", len(sawCreate.Additions))
	}
	add := sawCreate.Additions[0]
	if add.Name != "app.example.com." || add.Type != "A" || len(add.Rrdatas) != 1 || add.Rrdatas[0] != "203.0.113.5" {
		t.Fatalf("addition=%+v", add)
	}
	if len(sawCreate.Deletions) != 0 {
		t.Fatalf("expected no deletions when record absent, got %d", len(sawCreate.Deletions))
	}
}

func TestGoogleClient_FindZone_SuffixMatchAndNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"managedZones":[{"name":"example-com","dnsName":"example.com."}]}`))
	}))
	defer srv.Close()

	c := newGoogleTestClient(t, srv)

	zid, found, err := c.FindZone(context.Background(), "app.example.com")
	if err != nil || !found || zid != "example-com" {
		t.Fatalf("FindZone(suffix)=%q,%v,%v", zid, found, err)
	}

	_, found, err = c.FindZone(context.Background(), "other.org")
	if err != nil || found {
		t.Fatalf("FindZone(nomatch) found=%v err=%v, want found=false", found, err)
	}
}

func TestGoogleClient_GetRecord_FoundAndNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Get("name") == "app.example.com.":
			w.Write([]byte(`{"rrsets":[{"name":"app.example.com.","type":"A","ttl":300,"rrdatas":["203.0.113.5"]}]}`))
		default:
			w.Write([]byte(`{"rrsets":[]}`))
		}
	}))
	defer srv.Close()

	c := newGoogleTestClient(t, srv)

	rec, found, err := c.GetRecord(context.Background(), "example-com", "app.example.com", "A")
	if err != nil || !found {
		t.Fatalf("GetRecord found=%v err=%v", found, err)
	}
	if rec.Target != "203.0.113.5" || rec.TTL == nil || *rec.TTL != 300 {
		t.Fatalf("GetRecord rec=%+v", rec)
	}

	_, found, err = c.GetRecord(context.Background(), "example-com", "missing.example.com", "A")
	if err != nil || found {
		t.Fatalf("GetRecord(missing) found=%v err=%v, want false", found, err)
	}
}

func TestGoogleClient_UpsertRecord_IncludesDeletionWhenPresent(t *testing.T) {
	var sawCreate dns.Change
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/rrsets") && r.Method == http.MethodGet:
			w.Write([]byte(`{"rrsets":[{"name":"app.example.com.","type":"A","ttl":300,"rrdatas":["203.0.113.5"]}]}`))
		case strings.Contains(r.URL.Path, "/changes") && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &sawCreate); err != nil {
				t.Errorf("unmarshal change: %v", err)
			}
			w.Write([]byte(`{"id":"1","status":"done"}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	c := newGoogleTestClient(t, srv)
	if err := c.UpsertRecord(context.Background(), "example-com", Record{Name: "app.example.com", Type: "A", Target: "203.0.113.9"}); err != nil {
		t.Fatalf("UpsertRecord: %v", err)
	}
	if len(sawCreate.Deletions) != 1 || sawCreate.Deletions[0].Rrdatas[0] != "203.0.113.5" {
		t.Fatalf("expected deletion of existing rrset, got %+v", sawCreate.Deletions)
	}
	if len(sawCreate.Additions) != 1 || sawCreate.Additions[0].Rrdatas[0] != "203.0.113.9" {
		t.Fatalf("expected addition of new rrset, got %+v", sawCreate.Additions)
	}
}

// TestGoogleClient_UpsertRecord_CNAMETargetIsFQDN pins final-review Fix I2:
// Google Cloud DNS requires CNAME rrdata to be a dot-terminated FQDN and
// rejects a relative value, so UpsertRecord must append the trailing dot to a
// CNAME target that lacks one. An already-dotted target must not be
// double-dotted, and A/AAAA (IP) targets must be left exactly as given.
func TestGoogleClient_UpsertRecord_CNAMETargetIsFQDN(t *testing.T) {
	cases := []struct {
		name       string
		recordType string
		target     string
		wantRrdata string
	}{
		{"relative CNAME gets a trailing dot", "CNAME", "lb.example.com", "lb.example.com."},
		{"already-dotted CNAME is not double-dotted", "CNAME", "lb.example.com.", "lb.example.com."},
		{"A target is left untouched", "A", "203.0.113.5", "203.0.113.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sawCreate dns.Change
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.Contains(r.URL.Path, "/rrsets") && r.Method == http.MethodGet:
					w.Write([]byte(`{"rrsets":[]}`))
				case strings.Contains(r.URL.Path, "/changes") && r.Method == http.MethodPost:
					body, _ := io.ReadAll(r.Body)
					if err := json.Unmarshal(body, &sawCreate); err != nil {
						t.Errorf("unmarshal change: %v", err)
					}
					w.Write([]byte(`{"id":"1","status":"done"}`))
				default:
					w.Write([]byte(`{}`))
				}
			}))
			defer srv.Close()

			c := newGoogleTestClient(t, srv)
			if err := c.UpsertRecord(context.Background(), "example-com", Record{
				Name: "app.example.com", Type: tc.recordType, Target: tc.target,
			}); err != nil {
				t.Fatalf("UpsertRecord: %v", err)
			}
			if len(sawCreate.Additions) != 1 {
				t.Fatalf("expected 1 addition, got %d", len(sawCreate.Additions))
			}
			add := sawCreate.Additions[0]
			if len(add.Rrdatas) != 1 || add.Rrdatas[0] != tc.wantRrdata {
				t.Fatalf("rrdata = %+v, want [%q]", add.Rrdatas, tc.wantRrdata)
			}
		})
	}
}

func TestGoogleClient_DeleteRecord_AbsentIsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"rrsets":[]}`))
	}))
	defer srv.Close()

	c := newGoogleTestClient(t, srv)
	if err := c.DeleteRecord(context.Background(), "example-com", "missing.example.com", "A"); err != nil {
		t.Fatalf("DeleteRecord(absent) = %v, want nil", err)
	}
}

func TestGoogleClient_DeleteRecord_DeletesWhenPresent(t *testing.T) {
	var sawCreate dns.Change
	var sawCreateCall bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/rrsets") && r.Method == http.MethodGet:
			w.Write([]byte(`{"rrsets":[{"name":"app.example.com.","type":"A","ttl":300,"rrdatas":["203.0.113.5"]}]}`))
		case strings.Contains(r.URL.Path, "/changes") && r.Method == http.MethodPost:
			sawCreateCall = true
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &sawCreate); err != nil {
				t.Errorf("unmarshal change: %v", err)
			}
			w.Write([]byte(`{"id":"1","status":"done"}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	c := newGoogleTestClient(t, srv)
	if err := c.DeleteRecord(context.Background(), "example-com", "app.example.com", "A"); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if !sawCreateCall {
		t.Fatal("expected DeleteRecord to call Changes.Create")
	}
	if len(sawCreate.Additions) != 0 {
		t.Fatalf("expected no additions on delete, got %d", len(sawCreate.Additions))
	}
	if len(sawCreate.Deletions) != 1 || sawCreate.Deletions[0].Rrdatas[0] != "203.0.113.5" {
		t.Fatalf("expected deletion of existing rrset, got %+v", sawCreate.Deletions)
	}
}
