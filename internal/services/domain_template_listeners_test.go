package services

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

func TestValidateTemplateListeners(t *testing.T) {
	ok := models.Listeners{
		{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
		{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate},
		{Name: "tcpudp", Protocol: models.ListenerTCP, PortRangeMin: 9000, PortRangeMax: 9100},
	}
	if err := ValidateTemplateListeners(ok); err != nil {
		t.Fatalf("valid listeners rejected: %v", err)
	}
	// empty -> ErrNoListener
	if err := ValidateTemplateListeners(nil); !errors.Is(err, ErrNoListener) {
		t.Fatalf("empty: want ErrNoListener, got %v", err)
	}
	// port conflict: HTTP 443 + HTTPS 443
	conflict := models.Listeners{
		{Name: "http", Protocol: models.ListenerHTTP, Port: 443},
		{Name: "https", Protocol: models.ListenerHTTPS, Port: 443},
	}
	if err := ValidateTemplateListeners(conflict); !errors.Is(err, ErrListenerPortConflict) {
		t.Fatalf("conflict: want ErrListenerPortConflict, got %v", err)
	}
	// range overlaps a fixed port
	overlap := models.Listeners{
		{Name: "https", Protocol: models.ListenerHTTPS, Port: 9050},
		{Name: "tcpudp", Protocol: models.ListenerTCP, PortRangeMin: 9000, PortRangeMax: 9100},
	}
	if err := ValidateTemplateListeners(overlap); !errors.Is(err, ErrListenerPortConflict) {
		t.Fatalf("overlap: want ErrListenerPortConflict, got %v", err)
	}
	// reserved port
	reserved := models.Listeners{{Name: "http", Protocol: models.ListenerHTTP, Port: 19000}}
	if err := ValidateTemplateListeners(reserved); !errors.Is(err, ErrReservedPort) {
		t.Fatalf("reserved: want ErrReservedPort, got %v", err)
	}
	// TLS passthrough deferred
	tls := models.Listeners{{Name: "tls", Protocol: models.ListenerTLS, Port: 8443, TLSMode: models.TLSListenerPassthrough}}
	if err := ValidateTemplateListeners(tls); !errors.Is(err, ErrTLSPassthroughNotSupported) {
		t.Fatalf("tls: want ErrTLSPassthroughNotSupported, got %v", err)
	}
}

func TestDomainTemplateService_Create_RejectsInvalidListeners(t *testing.T) {
	svc := NewDomainTemplateService(nil, nil, nil, nil, nil)

	_, err := svc.Create(uuid.New(), &CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "ClusterIP",
	}, uuid.New())
	if !errors.Is(err, ErrNoListener) {
		t.Fatalf("no listeners: want ErrNoListener, got %v", err)
	}

	_, err = svc.Create(uuid.New(), &CreateDomainTemplateInput{
		Name:         "my-template",
		ExposureType: "ClusterIP",
		Listeners:    []models.TemplateListener{{Name: "tls", Protocol: models.ListenerTLS, Port: 8443, TLSMode: models.TLSListenerPassthrough}},
	}, uuid.New())
	if !errors.Is(err, ErrTLSPassthroughNotSupported) {
		t.Fatalf("tls passthrough: want ErrTLSPassthroughNotSupported, got %v", err)
	}
}
