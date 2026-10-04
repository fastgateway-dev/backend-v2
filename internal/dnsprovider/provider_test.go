package dnsprovider

import (
	"reflect"
	"testing"
)

func TestSupportedProviders(t *testing.T) {
	want := map[string]bool{"cloudflare": true, "route53": true, "google": true, "azure": true}
	if got := Supported(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Supported() = %v, want %v", got, want)
	}
}

func TestValidate_MissingFields(t *testing.T) {
	p, _ := Get("cloudflare")
	if err := p.Validate(map[string]string{}); err == nil {
		t.Fatal("expected error for missing apiToken")
	}
	if err := p.Validate(map[string]string{"apiToken": "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderSecret_PerProvider(t *testing.T) {
	cf, _ := Get("cloudflare")
	if got := cf.RenderSecret(map[string]string{"apiToken": "tok"}); string(got["apiToken"]) != "tok" {
		t.Fatalf("cloudflare secret key apiToken = %q", got["apiToken"])
	}
	r53, _ := Get("route53")
	s := r53.RenderSecret(map[string]string{"accessKeyId": "AK", "secretAccessKey": "SK"})
	if string(s["AWS_ACCESS_KEY_ID"]) != "AK" || string(s["AWS_SECRET_ACCESS_KEY"]) != "SK" {
		t.Fatalf("route53 secret = %v", s)
	}
	g, _ := Get("google")
	if got := g.RenderSecret(map[string]string{"serviceAccountKey": `{"x":1}`, "project": "p"}); string(got["credentials.json"]) != `{"x":1}` {
		t.Fatalf("google secret credentials.json = %q", got["credentials.json"])
	}
	az, _ := Get("azure")
	if got := az.RenderSecret(map[string]string{"tenantId": "t", "subscriptionId": "s", "resourceGroup": "rg", "clientId": "c", "clientSecret": "sec"}); len(got["azure.json"]) == 0 {
		t.Fatal("azure secret azure.json empty")
	}
}

func TestExternalDNSFlag(t *testing.T) {
	cases := map[string]string{"cloudflare": "cloudflare", "route53": "aws", "google": "google", "azure": "azure"}
	for ptype, flag := range cases {
		p, _ := Get(ptype)
		if p.ExternalDNSFlag() != flag {
			t.Fatalf("%s ExternalDNSFlag() = %q, want %q", ptype, p.ExternalDNSFlag(), flag)
		}
	}
}
