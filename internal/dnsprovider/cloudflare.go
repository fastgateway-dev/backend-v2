package dnsprovider

func init() { register(cloudflare{}) }

type cloudflare struct{}

func (cloudflare) Type() string             { return "cloudflare" }
func (cloudflare) RequiredFields() []string { return []string{"apiToken"} }
func (c cloudflare) Validate(cred map[string]string) error {
	return requireFields(cred, c.RequiredFields())
}
func (cloudflare) RenderSecret(cred map[string]string) map[string][]byte {
	return map[string][]byte{"apiToken": []byte(cred["apiToken"])}
}
func (cloudflare) ExternalDNSFlag() string { return "cloudflare" }
