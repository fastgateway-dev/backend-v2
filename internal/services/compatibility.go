package services

import "strings"

// VersionPair is a tested (Envoy Gateway, Gateway API) version combination.
type VersionPair struct {
	EnvoyGateway string `json:"envoyGateway"` // "1.7.0" (no leading v)
	GatewayAPI   string `json:"gatewayAPI"`   // "1.4.1"
}

// SupportedVersionPairs are the (EG, GatewayAPI) combinations FastGateway has
// been explicitly tested against. Add new pairs as new versions are validated.
var SupportedVersionPairs = []VersionPair{
	{EnvoyGateway: "1.9.1", GatewayAPI: "1.6.1"},
	{EnvoyGateway: "1.8.4", GatewayAPI: "1.5.1"},
	{EnvoyGateway: "1.7.0", GatewayAPI: "1.4.1"},
	{EnvoyGateway: "1.6.2", GatewayAPI: "1.4.1"},
}

// VersionStatus is the compatibility classification of a detected version pair.
type VersionStatus string

const (
	VersionStatusSupported VersionStatus = "supported"
	VersionStatusUntested  VersionStatus = "untested"
	VersionStatusUnknown   VersionStatus = "unknown"
)

// ClassifyVersionPair returns the compatibility status for a detected version pair.
// Either input being empty means detection failed and the result is Unknown.
//
// A pair is Supported when it matches a tested pair on major.minor for BOTH
// versions. The compatibility guarantee is per minor line: patch releases
// within a tested line (e.g. EG 1.9.2 on the tested 1.9.1 line) are bug and
// security fixes that the minor's e2e run validates, so they count as
// supported. A cross-line pairing that was never tested together (e.g. EG 1.8
// with GW 1.4) still reports Untested.
func ClassifyVersionPair(eg, gw string) VersionStatus {
	if eg == "" || gw == "" {
		return VersionStatusUnknown
	}
	egMinor := majorMinor(eg)
	gwMinor := majorMinor(gw)
	if egMinor == "" || gwMinor == "" {
		return VersionStatusUntested
	}
	for _, p := range SupportedVersionPairs {
		if majorMinor(p.EnvoyGateway) == egMinor && majorMinor(p.GatewayAPI) == gwMinor {
			return VersionStatusSupported
		}
	}
	return VersionStatusUntested
}

// majorMinor extracts the "major.minor" prefix from a semver-ish version
// string, tolerating a leading "v" and surrounding whitespace ("v1.9.2" →
// "1.9"). It returns "" when the string has no "major.minor" to extract.
func majorMinor(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "." + parts[1]
}
