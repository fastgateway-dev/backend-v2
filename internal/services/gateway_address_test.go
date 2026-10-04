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

// TestRecordTypeForAddress_ForcedFamilyMismatch is the regression test for
// final review Fix 3: forcing A on an IPv6 gateway address (or AAAA on an
// IPv4 one) used to be silently accepted because the switch only checked
// addr.Kind == "ip", not which IP family. Both must now error.
func TestRecordTypeForAddress_ForcedFamilyMismatch(t *testing.T) {
	v4 := GatewayAddress{Value: "203.0.113.5", Kind: "ip"}
	v6 := GatewayAddress{Value: "2001:db8::1", Kind: "ip"}

	if _, err := recordTypeForAddress(v6, models.DNSRecordTypeA); err == nil {
		t.Fatal("expected error forcing A on an IPv6 address")
	}
	if _, err := recordTypeForAddress(v4, models.DNSRecordTypeAAAA); err == nil {
		t.Fatal("expected error forcing AAAA on an IPv4 address")
	}
	if ty, err := recordTypeForAddress(v4, models.DNSRecordTypeA); err != nil || ty != models.DNSRecordTypeA {
		t.Fatalf("forced A on IPv4 should be ok, got ty=%v err=%v", ty, err)
	}
	if ty, err := recordTypeForAddress(v6, models.DNSRecordTypeAAAA); err != nil || ty != models.DNSRecordTypeAAAA {
		t.Fatalf("forced AAAA on IPv6 should be ok, got ty=%v err=%v", ty, err)
	}
}

// TestRecordTypeForAddress_UnknownForcedType_Errors is the regression test
// for final review Fix A (defense-in-depth): before this, an unrecognized
// forced type (e.g. "TXT", "foo") fell through the switch without matching
// any case and was returned verbatim via `return forced, nil`, letting it
// flow straight into the DNSEndpoint CR. It must now error instead.
func TestRecordTypeForAddress_UnknownForcedType_Errors(t *testing.T) {
	ip := GatewayAddress{Value: "203.0.113.5", Kind: "ip"}
	if _, err := recordTypeForAddress(ip, models.DNSRecordType("TXT")); err == nil {
		t.Fatal("expected error for unknown forced record type TXT")
	}
	if _, err := recordTypeForAddress(ip, models.DNSRecordType("foo")); err == nil {
		t.Fatal("expected error for unknown forced record type foo")
	}
	if _, err := recordTypeForAddress(ip, models.DNSRecordType("a")); err == nil {
		t.Fatal("expected error for lowercase forced record type a (case-sensitive)")
	}
}
