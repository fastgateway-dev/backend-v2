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
