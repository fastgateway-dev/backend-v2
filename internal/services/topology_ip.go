package services

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// NormalizeTopologyCIDR returns a canonical CIDR string. Plain IPs become /32 (v4) or /128 (v6).
// Returns an error for empty or unparseable inputs.
func NormalizeTopologyCIDR(in string) (string, error) {
	s := strings.TrimSpace(in)
	if s == "" {
		return "", errors.New("empty CIDR")
	}
	if strings.Contains(s, "/") {
		if _, _, err := net.ParseCIDR(s); err != nil {
			return "", fmt.Errorf("invalid CIDR %q: %w", in, err)
		}
		return s, nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return "", fmt.Errorf("invalid IP %q", in)
	}
	if ip.To4() != nil {
		return s + "/32", nil
	}
	return s + "/128", nil
}

// TopologyIPDedupKey returns a deterministic key for deduping topology IP rows
// within a single source dimension. Same source+ref+cidr → same key.
func TopologyIPDedupKey(source, refID, cidr string) string {
	return source + "|" + refID + "|" + cidr
}
