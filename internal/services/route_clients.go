package services

// EffectiveIPEntry represents a single IP CIDR in the effective IP allowlist
type EffectiveIPEntry struct {
	CIDR        string `json:"cidr"`
	ClientID    string `json:"clientId"`
	ClientName  string `json:"clientName"`
	Description string `json:"description,omitempty"`
}
