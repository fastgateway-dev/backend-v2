package kubernetes

import (
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func managedByLabels() map[string]interface{} {
	return map[string]interface{}{"app.kubernetes.io/managed-by": "fastgateway"}
}

// SelfSignedClusterIssuer builds a cert-manager ClusterIssuer backed by the
// selfSigned issuer type. Used as the root issuer for the internal CA chain.
func SelfSignedClusterIssuer(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer",
		"metadata": map[string]interface{}{"name": name, "labels": managedByLabels()},
		"spec":     map[string]interface{}{"selfSigned": map[string]interface{}{}},
	}}
}

// CACertConfig configures the internal CA Certificate built by CACertificate.
type CACertConfig struct {
	Name, Namespace, CommonName, SecretName, SelfSignedIssuerName, KeyAlgorithm string
	KeySize, DurationDays                                                       int
}

// CACertificate builds a cert-manager Certificate with isCA: true, issued by
// a selfSigned ClusterIssuer, whose resulting Secret backs a CA ClusterIssuer.
func CACertificate(cfg CACertConfig) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "Certificate",
		"metadata": map[string]interface{}{"name": cfg.Name, "namespace": cfg.Namespace, "labels": managedByLabels()},
		"spec": map[string]interface{}{
			"isCA":       true,
			"commonName": cfg.CommonName,
			"secretName": cfg.SecretName,
			"duration":   hoursDuration(cfg.DurationDays),
			"privateKey": map[string]interface{}{"algorithm": cfg.KeyAlgorithm, "size": int64(cfg.KeySize)},
			"issuerRef": map[string]interface{}{
				"name": cfg.SelfSignedIssuerName, "kind": "ClusterIssuer", "group": "cert-manager.io",
			},
		},
	}}
}

// LeafCertConfig configures the leaf Certificate built by LeafCertificate.
type LeafCertConfig struct {
	Name, Namespace, SecretName, IssuerClusterIssuerName, CommonName, KeyAlgorithm string
	DNSNames                                                                       []string
	KeySize, DurationDays                                                          int
}

// LeafCertificate builds a cert-manager Certificate for a leaf certificate,
// issued by a ClusterIssuer (typically the CA ClusterIssuer or ACME ClusterIssuer).
func LeafCertificate(cfg LeafCertConfig) *unstructured.Unstructured {
	dnsNames := make([]interface{}, 0, len(cfg.DNSNames))
	for _, n := range cfg.DNSNames {
		dnsNames = append(dnsNames, n)
	}
	spec := map[string]interface{}{
		"secretName": cfg.SecretName,
		"dnsNames":   dnsNames,
		"duration":   hoursDuration(cfg.DurationDays),
		"privateKey": map[string]interface{}{"algorithm": cfg.KeyAlgorithm, "size": int64(cfg.KeySize)},
		"issuerRef": map[string]interface{}{
			"name": cfg.IssuerClusterIssuerName, "kind": "ClusterIssuer", "group": "cert-manager.io",
		},
	}
	if cfg.CommonName != "" {
		spec["commonName"] = cfg.CommonName
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "Certificate",
		"metadata": map[string]interface{}{"name": cfg.Name, "namespace": cfg.Namespace, "labels": managedByLabels()},
		"spec":     spec,
	}}
}

// CAClusterIssuer builds a cert-manager ClusterIssuer of type "ca", signing
// with the key material in caSecretName (produced by CACertificate).
func CAClusterIssuer(name, caSecretName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer",
		"metadata": map[string]interface{}{"name": name, "labels": managedByLabels()},
		"spec":     map[string]interface{}{"ca": map[string]interface{}{"secretName": caSecretName}},
	}}
}

// ACMEIssuerConfig configures the ACME ClusterIssuer built by ACMEClusterIssuer.
type ACMEIssuerConfig struct {
	Name, Server, Email, AccountSecretName, EABKeyID, EABSecretName, ProviderType, SolverSecretName string
}

// ACMEClusterIssuer builds a cert-manager ClusterIssuer of type "acme" with a
// single DNS-01 solver. EAB fields are optional; when EABKeyID is set, an
// externalAccountBinding is added (used by some ACME providers such as ZeroSSL).
func ACMEClusterIssuer(cfg ACMEIssuerConfig) *unstructured.Unstructured {
	acme := map[string]interface{}{
		"server": cfg.Server, "email": cfg.Email,
		"privateKeySecretRef": map[string]interface{}{"name": cfg.AccountSecretName},
		"solvers":             []interface{}{dns01Solver(cfg.ProviderType, cfg.SolverSecretName)},
	}
	if cfg.EABKeyID != "" {
		acme["externalAccountBinding"] = map[string]interface{}{
			"keyID":        cfg.EABKeyID,
			"keySecretRef": map[string]interface{}{"name": cfg.EABSecretName, "key": "hmacKey"},
		}
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer",
		"metadata": map[string]interface{}{"name": cfg.Name, "labels": managedByLabels()},
		"spec":     map[string]interface{}{"acme": acme},
	}}
}

// dns01Solver currently supports cloudflare; extend the switch for more providers.
func dns01Solver(providerType, solverSecretName string) map[string]interface{} {
	switch providerType {
	case "cloudflare":
		return map[string]interface{}{"dns01": map[string]interface{}{
			"cloudflare": map[string]interface{}{
				"apiTokenSecretRef": map[string]interface{}{"name": solverSecretName, "key": "apiToken"},
			},
		}}
	default:
		return map[string]interface{}{"dns01": map[string]interface{}{}}
	}
}

// CloudflareSolverSecret builds the Secret holding the Cloudflare API token
// referenced by an ACME ClusterIssuer's dns01.cloudflare.apiTokenSecretRef.
func CloudflareSolverSecret(name, namespace, apiToken string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1", "kind": "Secret",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace, "labels": managedByLabels()},
		"type":       "Opaque",
		"stringData": map[string]interface{}{"apiToken": apiToken},
	}}
}

// EABSecret builds the Secret holding an ACME External Account Binding HMAC
// key, referenced by an ACME ClusterIssuer's
// acme.externalAccountBinding.keySecretRef (used by providers such as
// ZeroSSL that require EAB).
func EABSecret(name, namespace, hmacKey string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1", "kind": "Secret",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace, "labels": managedByLabels()},
		"type":       "Opaque",
		"stringData": map[string]interface{}{"hmacKey": hmacKey},
	}}
}

// hoursDuration renders a day count as a Go duration string, which is what
// cert-manager's Certificate.spec.duration expects (e.g. "87600h").
func hoursDuration(days int) string {
	return strconv.Itoa(days*24) + "h"
}
