package dnsprovider

import "errors"

func init() { register(route53{}) }

type route53 struct{}

func (route53) Type() string             { return "route53" }
func (route53) RequiredFields() []string { return []string{"accessKeyId", "secretAccessKey"} }
func (r route53) Validate(cred map[string]string) error {
	return requireFields(cred, r.RequiredFields())
}
func (route53) NewClient(cred map[string]string) (DNSClient, error) {
	return nil, errors.New("not implemented")
}
