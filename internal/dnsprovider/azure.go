package dnsprovider

import "encoding/json"

func init() { register(azure{}) }

type azure struct{}

func (azure) Type() string { return "azure" }
func (azure) RequiredFields() []string {
	return []string{"tenantId", "subscriptionId", "resourceGroup", "clientId", "clientSecret"}
}
func (a azure) Validate(cred map[string]string) error {
	return requireFields(cred, a.RequiredFields())
}
func (azure) RenderSecret(cred map[string]string) map[string][]byte {
	cfg, _ := json.Marshal(map[string]string{
		"tenantId":        cred["tenantId"],
		"subscriptionId":  cred["subscriptionId"],
		"resourceGroup":   cred["resourceGroup"],
		"aadClientId":     cred["clientId"],
		"aadClientSecret": cred["clientSecret"],
	})
	return map[string][]byte{"azure.json": cfg}
}
func (azure) ExternalDNSFlag() string { return "azure" }
