package services

import (
	"errors"
	"testing"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

func TestValidateBoundListeners(t *testing.T) {
	tmpl := &models.DomainTemplate{Listeners: models.Listeners{
		{Name: "http", Protocol: models.ListenerHTTP, Port: 80},
		{Name: "https", Protocol: models.ListenerHTTPS, Port: 443, TLSMode: models.TLSListenerTerminate},
		{Name: "tcpudp", Protocol: models.ListenerTCP, PortRangeMin: 9000, PortRangeMax: 9100},
	}}
	if err := ValidateBoundListeners(tmpl, []string{"http", "https"}); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	if err := ValidateBoundListeners(tmpl, []string{"nope"}); !errors.Is(err, ErrUnknownBoundListener) {
		t.Fatalf("unknown: want ErrUnknownBoundListener, got %v", err)
	}
	// cannot bind a TCP/UDP (non-hostname) listener to a domain
	if err := ValidateBoundListeners(tmpl, []string{"tcpudp"}); !errors.Is(err, ErrUnknownBoundListener) {
		t.Fatalf("stream listener bound to domain: want error, got %v", err)
	}
	if err := ValidateBoundListeners(tmpl, nil); !errors.Is(err, ErrNoBoundListener) {
		t.Fatalf("empty: want ErrNoBoundListener, got %v", err)
	}
	if !domainNeedsTLSSecret(tmpl, []string{"https"}) || domainNeedsTLSSecret(tmpl, []string{"http"}) {
		t.Fatal("TLS-secret-needed detection wrong")
	}
	passthrough := &models.DomainTemplate{Listeners: models.Listeners{
		{Name: "tls", Protocol: models.ListenerTLS, Port: 8443, TLSMode: models.TLSListenerPassthrough},
	}}
	if domainNeedsTLSSecret(passthrough, []string{"tls"}) {
		t.Fatal("passthrough listener must not require a TLS secret")
	}
}
