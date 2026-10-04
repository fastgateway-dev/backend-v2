// Package dnsprovider isolates everything provider-specific about DNS:
// credential field validation, the external-dns Secret payload, and the
// external-dns --provider flag. The DNSEndpoint CR and record lifecycle are
// provider-agnostic and live elsewhere.
package dnsprovider

import (
	"fmt"
	"sort"
)

// SecretName is the fixed Secret in fastgateway-system that external-dns reads.
const SecretName = "fgw-externaldns-credentials"

type DNSProvider interface {
	Type() string
	RequiredFields() []string
	Validate(cred map[string]string) error
	RenderSecret(cred map[string]string) map[string][]byte
	ExternalDNSFlag() string
}

var registry = map[string]DNSProvider{}

func register(p DNSProvider) { registry[p.Type()] = p }

func Get(providerType string) (DNSProvider, bool) {
	p, ok := registry[providerType]
	return p, ok
}

func Supported() map[string]bool {
	out := make(map[string]bool, len(registry))
	for k := range registry {
		out[k] = true
	}
	return out
}

// requireFields returns an error naming every missing/empty required field.
func requireFields(cred map[string]string, fields []string) error {
	var missing []string
	for _, f := range fields {
		if cred[f] == "" {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required credential field(s): %v", missing)
	}
	return nil
}
