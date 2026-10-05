package dnsprovider

import "errors"

func init() { register(google{}) }

type google struct{}

func (google) Type() string             { return "google" }
func (google) RequiredFields() []string { return []string{"serviceAccountKey", "project"} }
func (g google) Validate(cred map[string]string) error {
	return requireFields(cred, g.RequiredFields())
}
func (google) NewClient(cred map[string]string) (DNSClient, error) {
	return nil, errors.New("not implemented")
}
