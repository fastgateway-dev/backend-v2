package models

import (
	"reflect"
	"testing"
)

func TestMigrateTemplateListeners(t *testing.T) {
	// both + terminate + domain-enabled + stream-enabled
	got := MigrateTemplateListeners("both", 80, 443, "terminate", true, true)
	want := Listeners{
		{Name: "http", Protocol: ListenerHTTP, Port: 80},
		{Name: "https", Protocol: ListenerHTTPS, Port: 443, TLSMode: TLSListenerTerminate},
		{Name: "tcpudp", Protocol: ListenerTCP, PortRangeMin: 1, PortRangeMax: 65535},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("both+stream:\n got %+v\nwant %+v", got, want)
	}
	// tls_only + passthrough, domain-enabled, no stream
	got = MigrateTemplateListeners("tls_only", 80, 443, "passthrough", true, false)
	want = Listeners{{Name: "https", Protocol: ListenerHTTPS, Port: 443, TLSMode: TLSListenerPassthrough}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tls_only+passthrough:\n got %+v\nwant %+v", got, want)
	}
	// no_tls, domain-enabled
	got = MigrateTemplateListeners("no_tls", 8080, 443, "terminate", true, false)
	want = Listeners{{Name: "http", Protocol: ListenerHTTP, Port: 8080}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("no_tls:\n got %+v\nwant %+v", got, want)
	}
	// I1 regression guard: a STREAM-ONLY template (enable_domain=false,
	// enable_stream=true) still carried tls_mode at its NOT NULL default
	// ('tls_only'). The hostname listener must be gated on enable_domain, so
	// such a row backfills to the TCP/UDP range ONLY -- never a phantom
	// HTTPS:443 that would wrongly make it domain-eligible in the picker.
	got = MigrateTemplateListeners("tls_only", 80, 443, "terminate", false, true)
	want = Listeners{{Name: "tcpudp", Protocol: ListenerTCP, PortRangeMin: 1, PortRangeMax: 65535}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stream-only:\n got %+v\nwant %+v", got, want)
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
