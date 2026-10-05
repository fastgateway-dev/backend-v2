package models

import "testing"

func TestDomainDNSRecord_TableName(t *testing.T) {
	if got := (DomainDNSRecord{}).TableName(); got != "domain_dns_records" {
		t.Fatalf("TableName() = %q, want domain_dns_records", got)
	}
}

func TestDNSRecordStatusConstants(t *testing.T) {
	for _, s := range []DNSRecordStatus{DNSRecordStatusPending, DNSRecordStatusReady, DNSRecordStatusError} {
		if s == "" {
			t.Fatal("empty DNSRecordStatus constant")
		}
	}
}
