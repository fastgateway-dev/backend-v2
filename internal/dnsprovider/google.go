package dnsprovider

func init() { register(google{}) }

type google struct{}

func (google) Type() string             { return "google" }
func (google) RequiredFields() []string { return []string{"serviceAccountKey", "project"} }
func (g google) Validate(cred map[string]string) error {
	return requireFields(cred, g.RequiredFields())
}
func (google) RenderSecret(cred map[string]string) map[string][]byte {
	return map[string][]byte{"credentials.json": []byte(cred["serviceAccountKey"])}
}
func (google) ExternalDNSFlag() string { return "google" }
