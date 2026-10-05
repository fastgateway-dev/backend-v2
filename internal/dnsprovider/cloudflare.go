package dnsprovider

import "errors"

func init() { register(cloudflare{}) }

type cloudflare struct{}

func (cloudflare) Type() string             { return "cloudflare" }
func (cloudflare) RequiredFields() []string { return []string{"apiToken"} }
func (c cloudflare) Validate(cred map[string]string) error {
	return requireFields(cred, c.RequiredFields())
}
func (cloudflare) NewClient(cred map[string]string) (DNSClient, error) {
	return nil, errors.New("not implemented")
}
