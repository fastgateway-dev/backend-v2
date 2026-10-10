package models

// MigrateTemplateListeners maps the pre-listener template fields onto the
// listener list, preserving listener NAMES ("http"/"https") so generated
// Gateways stay byte-identical. A stream-enabled template gets the full valid
// TCP/UDP range so no existing stream route port becomes invalid.
//
// The hostname (HTTP/HTTPS) listeners are gated on enableDomain: a stream-only
// template (enable_domain=false, enable_stream=true) still carried tls_mode at
// its NOT NULL default, which is meaningless for a stream template -- emitting
// a hostname listener from it would fabricate a phantom HTTP/HTTPS listener and
// wrongly make the template domain-eligible. Since the old model rejected
// both-false, every row has at least one of enableDomain/enableStream, so the
// result is never empty.
func MigrateTemplateListeners(tlsMode string, httpPort, httpsPort int, tlsPolicy string, enableDomain, enableStream bool) Listeners {
	var ls Listeners
	if enableDomain {
		tlsm := TLSListenerTerminate
		if tlsPolicy == "passthrough" {
			tlsm = TLSListenerPassthrough
		}
		http := TemplateListener{Name: "http", Protocol: ListenerHTTP, Port: httpPort}
		https := TemplateListener{Name: "https", Protocol: ListenerHTTPS, Port: httpsPort, TLSMode: tlsm}
		switch tlsMode {
		case "no_tls":
			ls = Listeners{http}
		case "both":
			ls = Listeners{http, https}
		default: // tls_only + unknown: mirror gateway.go default (secret-driven)
			ls = Listeners{https}
		}
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
