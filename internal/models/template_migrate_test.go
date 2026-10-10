package models

import (
	"reflect"
	"testing"
)

func TestMigrateTemplateListeners(t *testing.T) {
	// both + terminate + stream-enabled
	got := MigrateTemplateListeners("both", 80, 443, "terminate", true)
	want := Listeners{
		{Name: "http", Protocol: ListenerHTTP, Port: 80},
		{Name: "https", Protocol: ListenerHTTPS, Port: 443, TLSMode: TLSListenerTerminate},
		{Name: "tcpudp", Protocol: ListenerTCP, PortRangeMin: 1, PortRangeMax: 65535},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("both+stream:\n got %+v\nwant %+v", got, want)
	}
	// tls_only + passthrough, no stream
	got = MigrateTemplateListeners("tls_only", 80, 443, "passthrough", false)
	want = Listeners{{Name: "https", Protocol: ListenerHTTPS, Port: 443, TLSMode: TLSListenerPassthrough}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tls_only+passthrough:\n got %+v\nwant %+v", got, want)
	}
	// no_tls
	got = MigrateTemplateListeners("no_tls", 8080, 443, "terminate", false)
	want = Listeners{{Name: "http", Protocol: ListenerHTTP, Port: 8080}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("no_tls:\n got %+v\nwant %+v", got, want)
	}
}

func TestMigrateDomainBoundListeners(t *testing.T) {
	cases := map[string][]string{
		"both":     {"http", "https"},
		"tls_only": {"https"},
		"no_tls":   {"http"},
	}
	for mode, want := range cases {
		if got := MigrateDomainBoundListeners(mode); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %v want %v", mode, got, want)
		}
	}
}
