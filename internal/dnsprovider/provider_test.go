package dnsprovider

import (
	"reflect"
	"testing"
)

func TestSupportedProviders_ThreeProviders(t *testing.T) {
	want := map[string]bool{"cloudflare": true, "route53": true, "google": true}
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

func TestRoute53_RegionOptional(t *testing.T) {
	p, _ := Get("route53")
	// region is NOT required (defaults later), only the keys are
	if err := p.Validate(map[string]string{"accessKeyId": "AK", "secretAccessKey": "SK"}); err != nil {
		t.Fatalf("route53 without region should validate: %v", err)
	}
}
