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
	if err != nil {
		t.Fatal(err)
	}
	zid, found, err := c.FindZone(context.Background(), "example.com")
	if err != nil || !found || zid != "zone123" {
		t.Fatalf("FindZone=%q,%v,%v", zid, found, err)
	}
	if err := c.UpsertRecord(context.Background(), "zone123", Record{Name: "app.example.com", Type: "A", Target: "203.0.113.5"}); err != nil {
		t.Fatalf("UpsertRecord: %v", err)
	}
}

func TestCloudflareClient_FindZone_SuffixMatchAndNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"result":[{"id":"zone123","name":"example.com"}],"result_info":{"page":1,"total_pages":1}}`))
	}))
	defer srv.Close()

	c, err := cloudflare{}.newClientWithBaseURL(map[string]string{"apiToken": "tok"}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	zid, found, err := c.FindZone(context.Background(), "app.example.com")
	if err != nil || !found || zid != "zone123" {
		t.Fatalf("FindZone(suffix)=%q,%v,%v", zid, found, err)
	}

	_, found, err = c.FindZone(context.Background(), "other.org")
	if err != nil || found {
		t.Fatalf("FindZone(nomatch) found=%v err=%v, want found=false", found, err)
	}
}

func TestCloudflareClient_GetRecord_FoundAndNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "dns_records") && r.URL.Query().Get("name") == "app.example.com":
			w.Write([]byte(`{"success":true,"result":[{"id":"rec1","name":"app.example.com","type":"A","content":"203.0.113.5","ttl":300}],"result_info":{"page":1,"total_pages":1}}`))
		case strings.Contains(r.URL.Path, "dns_records"):
			w.Write([]byte(`{"success":true,"result":[],"result_info":{"page":1,"total_pages":1}}`))
		default:
			w.Write([]byte(`{"success":true,"result":{}}`))
		}
	}))
	defer srv.Close()

	c, err := cloudflare{}.newClientWithBaseURL(map[string]string{"apiToken": "tok"}, srv.URL)
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

func TestCloudflareClient_UpsertRecord_UpdatesWhenPresent(t *testing.T) {
	var sawUpdate bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodGet:
			w.Write([]byte(`{"success":true,"result":[{"id":"rec1","name":"app.example.com","type":"A","content":"203.0.113.5"}],"result_info":{"page":1,"total_pages":1}}`))
		case strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodPatch:
			sawUpdate = true
			w.Write([]byte(`{"success":true,"result":{"id":"rec1"}}`))
		default:
			w.Write([]byte(`{"success":true,"result":{}}`))
		}
	}))
	defer srv.Close()

	c, err := cloudflare{}.newClientWithBaseURL(map[string]string{"apiToken": "tok"}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertRecord(context.Background(), "zone123", Record{Name: "app.example.com", Type: "A", Target: "203.0.113.9"}); err != nil {
		t.Fatalf("UpsertRecord: %v", err)
	}
	if !sawUpdate {
		t.Fatal("expected UpsertRecord to call update (PATCH) when record already exists")
	}
}

func TestCloudflareClient_DeleteRecord_AbsentIsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"result":[],"result_info":{"page":1,"total_pages":1}}`))
	}))
	defer srv.Close()

	c, err := cloudflare{}.newClientWithBaseURL(map[string]string{"apiToken": "tok"}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteRecord(context.Background(), "zone123", "missing.example.com", "A"); err != nil {
		t.Fatalf("DeleteRecord(absent) = %v, want nil", err)
	}
}

func TestCloudflareClient_DeleteRecord_DeletesWhenPresent(t *testing.T) {
	var sawDelete bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodGet:
			w.Write([]byte(`{"success":true,"result":[{"id":"rec1","name":"app.example.com","type":"A","content":"203.0.113.5"}],"result_info":{"page":1,"total_pages":1}}`))
		case strings.Contains(r.URL.Path, "dns_records") && r.Method == http.MethodDelete:
			sawDelete = true
			w.Write([]byte(`{"success":true,"result":{"id":"rec1"}}`))
		default:
			w.Write([]byte(`{"success":true,"result":{}}`))
		}
	}))
	defer srv.Close()

	c, err := cloudflare{}.newClientWithBaseURL(map[string]string{"apiToken": "tok"}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteRecord(context.Background(), "zone123", "app.example.com", "A"); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if !sawDelete {
		t.Fatal("expected DeleteRecord to call DELETE when record exists")
	}
}

func TestCloudflareClient_RecordExistsForName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
