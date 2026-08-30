package services

// VersionPair is a tested (Envoy Gateway, Gateway API) version combination.
type VersionPair struct {
	EnvoyGateway string `json:"envoyGateway"` // "1.7.0" (no leading v)
	GatewayAPI   string `json:"gatewayAPI"`   // "1.4.1"
}

// SupportedVersionPairs are the (EG, GatewayAPI) combinations FastGateway has
// been explicitly tested against. Add new pairs as new versions are validated.
var SupportedVersionPairs = []VersionPair{
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
func ClassifyVersionPair(eg, gw string) VersionStatus {
	if eg == "" || gw == "" {
		return VersionStatusUnknown
	}
	for _, p := range SupportedVersionPairs {
		if p.EnvoyGateway == eg && p.GatewayAPI == gw {
			return VersionStatusSupported
		}
	}
	return VersionStatusUntested
}
