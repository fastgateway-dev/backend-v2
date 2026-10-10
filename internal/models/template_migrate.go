package models

// MigrateTemplateListeners maps the pre-listener template fields onto the
// listener list, preserving listener NAMES ("http"/"https") so generated
// Gateways stay byte-identical. A stream-enabled template gets the full valid
// TCP/UDP range so no existing stream route port becomes invalid.
func MigrateTemplateListeners(tlsMode string, httpPort, httpsPort int, tlsPolicy string, enableStream bool) Listeners {
	tlsm := TLSListenerTerminate
	if tlsPolicy == "passthrough" {
		tlsm = TLSListenerPassthrough
	}
	http := TemplateListener{Name: "http", Protocol: ListenerHTTP, Port: httpPort}
	https := TemplateListener{Name: "https", Protocol: ListenerHTTPS, Port: httpsPort, TLSMode: tlsm}

	var ls Listeners
	switch tlsMode {
	case "no_tls":
		ls = Listeners{http}
	case "tls_only":
		ls = Listeners{https}
	case "both":
		ls = Listeners{http, https}
	default:
		ls = Listeners{https} // mirror gateway.go default (secret-driven); safe fallback
	}
	if enableStream {
		ls = append(ls, TemplateListener{Name: "tcpudp", Protocol: ListenerTCP, PortRangeMin: 1, PortRangeMax: 65535})
	}
	return ls
}

// MigrateDomainBoundListeners returns the listener names an existing domain
// binds, derived from its old TLSMode.
func MigrateDomainBoundListeners(tlsMode string) []string {
	switch tlsMode {
	case "no_tls":
		return []string{"http"}
	case "both":
		return []string{"http", "https"}
	default: // tls_only and unknown
		return []string{"https"}
	}
}
