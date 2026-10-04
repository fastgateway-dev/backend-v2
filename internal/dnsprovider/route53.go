package dnsprovider

func init() { register(route53{}) }

type route53 struct{}

func (route53) Type() string             { return "route53" }
func (route53) RequiredFields() []string { return []string{"accessKeyId", "secretAccessKey"} }
func (r route53) Validate(cred map[string]string) error {
	return requireFields(cred, r.RequiredFields())
}
func (route53) RenderSecret(cred map[string]string) map[string][]byte {
	return map[string][]byte{
		"AWS_ACCESS_KEY_ID":     []byte(cred["accessKeyId"]),
		"AWS_SECRET_ACCESS_KEY": []byte(cred["secretAccessKey"]),
	}
}
func (route53) ExternalDNSFlag() string { return "aws" }
