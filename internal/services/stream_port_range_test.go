package services

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

func TestTemplatePortRange(t *testing.T) {
	tmpl := &models.DomainTemplate{Listeners: models.Listeners{
		{Name: "tcpudp", Protocol: models.ListenerUDP, PortRangeMin: 9000, PortRangeMax: 9100},
	}}
	min, max, ok := templatePortRange(tmpl)
	if !ok || min != 9000 || max != 9100 {
		t.Fatalf("range: %d-%d ok=%v", min, max, ok)
	}
	for _, p := range []int{9000, 9042, 9100} {
		if err := checkPortInRange(p, tmpl); err != nil {
			t.Fatalf("in-range %d rejected: %v", p, err)
		}
	}
	for _, p := range []int{8999, 8125, 9101} {
		if err := checkPortInRange(p, tmpl); !errors.Is(err, ErrPortOutOfRange) {
			t.Fatalf("out-of-range %d: want ErrPortOutOfRange, got %v", p, err)
		}
	}
	// template with no range (domain-only) => stream route rejected
	noRange := &models.DomainTemplate{Listeners: models.Listeners{{Name: "https", Protocol: models.ListenerHTTPS, Port: 443}}}
	if err := checkPortInRange(9042, noRange); !errors.Is(err, ErrPortOutOfRange) {
		t.Fatalf("no-range template: want ErrPortOutOfRange, got %v", err)
	}
}
