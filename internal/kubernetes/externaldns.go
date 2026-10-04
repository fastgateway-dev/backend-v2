package kubernetes

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// ExternalDNSSecretName is the fixed Secret external-dns reads for its provider
// credentials. Must match internal/dnsprovider.SecretName.
const ExternalDNSSecretName = "fgw-externaldns-credentials"

type DNSEndpointConfig struct {
	Name              string
	Hostname          string
	RecordType        string // A | AAAA | CNAME
	Targets           []string
	TTL               *int
	CloudflareProxied bool
}

// DNSEndpoint builds an externaldns.k8s.io/v1alpha1 DNSEndpoint in
// fastgateway-system for a single hostname->targets record.
func DNSEndpoint(cfg DNSEndpointConfig) *unstructured.Unstructured {
	targets := make([]interface{}, len(cfg.Targets))
	for i, t := range cfg.Targets {
		targets[i] = t
	}
	endpoint := map[string]interface{}{
		"dnsName":    cfg.Hostname,
		"recordType": cfg.RecordType,
		"targets":    targets,
	}
	if cfg.TTL != nil {
		endpoint["recordTTL"] = int64(*cfg.TTL)
	}
	meta := map[string]interface{}{
		"name":      cfg.Name,
		"namespace": FastGatewayNamespace,
		"labels":    managedByLabels(),
	}
	if cfg.CloudflareProxied {
		meta["annotations"] = map[string]interface{}{
			"external-dns.alpha.kubernetes.io/cloudflare-proxied": "true",
		}
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "externaldns.k8s.io/v1alpha1",
		"kind":       "DNSEndpoint",
		"metadata":   meta,
		"spec":       map[string]interface{}{"endpoints": []interface{}{endpoint}},
	}}
}

// ExternalDNSSecret builds the opaque Secret external-dns reads for credentials.
func ExternalDNSSecret(name string, data map[string][]byte) *unstructured.Unstructured {
	strData := make(map[string]interface{}, len(data))
	for k, v := range data {
		strData[k] = string(v)
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"type":       "Opaque",
		"metadata": map[string]interface{}{
			"name": name, "namespace": FastGatewayNamespace, "labels": managedByLabels(),
		},
		"stringData": strData,
	}}
}
