package services

import (
	"fmt"
	"net"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type GatewayAddress struct {
	Value string
	Kind  string // "ip" | "hostname"
}

// resolveGatewayAddress reads the first usable address from a Gateway's
// status.addresses. ok=false means no address is assigned yet.
func resolveGatewayAddress(obj *unstructured.Unstructured) (GatewayAddress, bool) {
	addrs, found, err := unstructured.NestedSlice(obj.Object, "status", "addresses")
	if err != nil || !found || len(addrs) == 0 {
		return GatewayAddress{}, false
	}
	for _, a := range addrs {
		m, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		val, _ := m["value"].(string)
		if val == "" {
			continue
		}
		kind := "hostname"
		if net.ParseIP(val) != nil {
			kind = "ip"
		}
		return GatewayAddress{Value: val, Kind: kind}, true
	}
	return GatewayAddress{}, false
}

// recordTypeForAddress resolves the effective record type, honoring a forced
// type and erroring on an impossible combination.
func recordTypeForAddress(addr GatewayAddress, forced models.DNSRecordType) (models.DNSRecordType, error) {
	auto := models.DNSRecordTypeCNAME
	if addr.Kind == "ip" {
		if ip := net.ParseIP(addr.Value); ip != nil && ip.To4() == nil {
			auto = models.DNSRecordTypeAAAA
		} else {
			auto = models.DNSRecordTypeA
		}
	}
	if forced == "" || forced == models.DNSRecordTypeAuto {
		return auto, nil
	}
	// Validate the forced type against the address kind.
	switch forced {
	case models.DNSRecordTypeA:
		if addr.Kind != "ip" {
			return "", fmt.Errorf("record type A requires an IP target, but the gateway address %q is a hostname", addr.Value)
		}
		if ip := net.ParseIP(addr.Value); ip == nil || ip.To4() == nil {
			return "", fmt.Errorf("record type A requires an IPv4 target, but the gateway address %q is IPv6", addr.Value)
		}
	case models.DNSRecordTypeAAAA:
		if addr.Kind != "ip" {
			return "", fmt.Errorf("record type AAAA requires an IP target, but the gateway address %q is a hostname", addr.Value)
		}
		if ip := net.ParseIP(addr.Value); ip == nil || ip.To4() != nil {
			return "", fmt.Errorf("record type AAAA requires an IPv6 target, but the gateway address %q is IPv4", addr.Value)
		}
	case models.DNSRecordTypeCNAME:
		if addr.Kind != "hostname" {
			return "", fmt.Errorf("record type CNAME requires a hostname target, but the gateway address %q is an IP", addr.Value)
		}
	default:
		// Defense-in-depth (final review Fix A): DNSRecordService validates
		// RecordType before it ever reaches here, but no unexpected value
		// must be allowed to flow into the DNSEndpoint CR even if a future
		// caller skips that validation.
		return "", fmt.Errorf("unsupported record type %q (allowed: auto, A, AAAA, CNAME)", forced)
	}
	return forced, nil
}
