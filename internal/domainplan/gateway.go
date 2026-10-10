// Package domainplan builds Kubernetes manifest configuration for domains.
//
// It is the domain-level sibling of internal/routeplan and carries the same
// contract: no database access, no Kubernetes client. Inputs are models
// values, outputs are kubernetes.*Config values.
//
// Before Phase 2F these four builders were private methods on DomainService
// -- a second manifest-assembly path that bypassed the pure layer entirely
// and that none of the 72 route goldens covered. Phase 2D found one of them
// to be the third EnvoyExtensionPolicy assembler in the codebase.
package domainplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
)

// BuildGatewayConfig builds a kubernetes.GatewayConfig from a domain, resolving
// the domain's bound listeners against its template and including template
// annotations.
//
// Template and template annotations are resolved by the caller and passed in
// rather than looked up here: domainplan performs no I/O. Pass nil annotations
// when the domain has no template, or when the lookup failed -- see the note
// on error handling at the call sites.
func BuildGatewayConfig(domain *models.Domain, template *models.DomainTemplate, templateAnnotations models.Annotations) *kubernetes.GatewayConfig {
	tlsSecretName := domain.TLSSecretName
	tlsSecretNamespace := domain.TLSSecretNamespace
	if domain.ManagedCertificateID != nil {
		// A managed cert wins over any legacy BYO secret. The Phase-3a
		// distribution controller pushes the leaf to cert-<id> in
		// fastgateway-system for every ready cert, so the name is
		// deterministic and needs no cert lookup here.
		tlsSecretName = "cert-" + domain.ManagedCertificateID.String()
		tlsSecretNamespace = kubernetes.FastGatewayNamespace
	}

	config := &kubernetes.GatewayConfig{
		Name:               domain.K8sGatewayName,
		Namespace:          domain.Namespace,
		GatewayClassName:   domain.K8sGatewayClass,
		Hostname:           domain.Hostname,
		TLSSecretName:      tlsSecretName,
		TLSSecretNamespace: tlsSecretNamespace,
		HostnameListeners:  resolveHostnameListeners(domain.BoundListeners, template),
	}
	// Include annotations from domain template
	if domain.DomainTemplateID != nil {
		config.Annotations = templateAnnotations
	}
	return config
}

// resolveHostnameListeners resolves bound listener names against the
// template's listeners.
//
// Output follows the TEMPLATE's listener order, not the order of bound, so a
// migrated "both" domain yields [http, https] regardless of how its binding
// was stored. Only hostname-routed listeners (HTTP/HTTPS/TLS) are resolved;
// port-routed TCP/UDP listeners belong to the stream Gateway. Name, protocol,
// port and TLS mode are copied from the matched template listener verbatim --
// the protocol is never coerced (a non-HTTPS listener must not silently
// become HTTP here).
//
// A Gateway with zero listeners is invalid, so when the domain is bound to
// listeners but none of the names resolve (a stale binding after the template
// was edited) the result falls back to every hostname-routed template
// listener instead of being empty. It is empty only when there is nothing to
// resolve: no bound listeners, or no template / no hostname-routed listeners.
func resolveHostnameListeners(bound []string, template *models.DomainTemplate) []kubernetes.HostnameListener {
	if len(bound) == 0 || template == nil {
		return nil
	}
	want := make(map[string]struct{}, len(bound))
	for _, name := range bound {
		want[name] = struct{}{}
	}

	hostnameRouted := template.Listeners.HostnameRouted()
	var out []kubernetes.HostnameListener
	for _, l := range hostnameRouted {
		if _, ok := want[l.Name]; ok {
			out = append(out, toHostnameListener(l))
		}
	}
	if len(out) == 0 {
		for _, l := range hostnameRouted {
			out = append(out, toHostnameListener(l))
		}
	}
	return out
}

func toHostnameListener(l models.TemplateListener) kubernetes.HostnameListener {
	return kubernetes.HostnameListener{
		Name:     l.Name,
		Protocol: string(l.Protocol),
		Port:     l.Port,
		TLSMode:  string(l.TLSMode),
	}
}
