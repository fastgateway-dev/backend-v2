package models

import "testing"

func TestDNSHostedZone_TableName(t *testing.T) {
	if got := (DNSHostedZone{}).TableName(); got != "dns_hosted_zones" {
		t.Fatalf("TableName() = %q, want dns_hosted_zones", got)
	}
}

func TestDNSZoneStatusConstants(t *testing.T) {
	for _, s := range []DNSZoneStatus{DNSZoneStatusReady, DNSZoneStatusError, DNSZoneStatusPending} {
		if s == "" {
			t.Fatal("empty DNSZoneStatus constant")
		}
	}
}
