package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

type ListenerProtocol string

const (
	ListenerHTTP  ListenerProtocol = "HTTP"
	ListenerHTTPS ListenerProtocol = "HTTPS"
	ListenerTLS   ListenerProtocol = "TLS"
	ListenerTCP   ListenerProtocol = "TCP"
	ListenerUDP   ListenerProtocol = "UDP"
)

type ListenerTLSMode string

const (
	TLSListenerTerminate   ListenerTLSMode = "Terminate"
	TLSListenerPassthrough ListenerTLSMode = "Passthrough"
)

// TemplateListener is one listener a Gateway Template exposes. Hostname-routed
// families (HTTP/HTTPS/TLS) use Port; the port-routed family (TCP/UDP) uses
// PortRangeMin/Max. Name is the Gateway API sectionName and is referenced by
// Domain.BoundListeners.
type TemplateListener struct {
	Name         string           `json:"name"`
	Protocol     ListenerProtocol `json:"protocol"`
	Port         int              `json:"port,omitempty"`
	TLSMode      ListenerTLSMode  `json:"tlsMode,omitempty"`
	PortRangeMin int              `json:"portRangeMin,omitempty"`
	PortRangeMax int              `json:"portRangeMax,omitempty"`
}

// Listeners is a JSONB-stored slice (mirrors internal/models/telemetry.go's
// Value/Scan pattern for slice-bearing JSONB columns).
type Listeners []TemplateListener

func (l Listeners) Value() (driver.Value, error) { return json.Marshal(l) }

func (l *Listeners) Scan(src interface{}) error {
	if src == nil {
		*l = nil
		return nil
	}
	b, ok := src.([]byte)
	if !ok {
		if s, ok := src.(string); ok {
			b = []byte(s)
		} else {
			return errors.New("Listeners.Scan: unsupported source type")
		}
	}
	return json.Unmarshal(b, l)
}

func (l Listeners) HostnameRouted() []TemplateListener {
	var out []TemplateListener
	for _, x := range l {
		switch x.Protocol {
		case ListenerHTTP, ListenerHTTPS, ListenerTLS:
			out = append(out, x)
		}
	}
	return out
}

// StreamRange returns the single shared TCP/UDP range, if present.
func (l Listeners) StreamRange() (min, max int, ok bool) {
	for _, x := range l {
		if x.Protocol == ListenerTCP || x.Protocol == ListenerUDP {
			return x.PortRangeMin, x.PortRangeMax, true
		}
	}
	return 0, 0, false
}
