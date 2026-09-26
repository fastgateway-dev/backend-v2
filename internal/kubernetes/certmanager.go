package kubernetes

import (
	"encoding/base64"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/fastgateway-dev/backend-v2/internal/models"
)

func managedByLabels() map[string]interface{} {
	return map[string]interface{}{"app.kubernetes.io/managed-by": "fastgateway"}
}

// SelfSignedIssuer builds a namespaced cert-manager Issuer backed by the
// selfSigned issuer type. Used as the root issuer for the internal CA chain.
func SelfSignedIssuer(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "Issuer",
		"metadata": map[string]interface{}{"name": name, "namespace": namespace, "labels": managedByLabels()},
		"spec":     map[string]interface{}{"selfSigned": map[string]interface{}{}},
	}}
}

// CACertConfig configures the internal CA Certificate built by CACertificate.
type CACertConfig struct {
	Name, Namespace, CommonName, SecretName, SelfSignedIssuerName, KeyAlgorithm string
	KeySize, DurationDays                                                       int
}

// CACertificate builds a cert-manager Certificate with isCA: true, issued by
// a selfSigned Issuer, whose resulting Secret backs a CA Issuer.
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
				"name": cfg.SelfSignedIssuerName, "kind": "Issuer", "group": "cert-manager.io",
			},
		},
	}}
}

// LeafCertConfig configures the leaf Certificate built by LeafCertificate.
type LeafCertConfig struct {
	Name, Namespace, SecretName, IssuerName, CommonName, KeyAlgorithm string
	DNSNames, URISANs                                                 []string
	KeySize, DurationDays                                             int
	Usage                                                             models.ManagedCertUsage
}

// LeafCertificate builds a cert-manager Certificate for a leaf certificate,
// issued by an Issuer (typically the CA Issuer or ACME Issuer).
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
			"name": cfg.IssuerName, "kind": "Issuer", "group": "cert-manager.io",
		},
	}
	if cfg.CommonName != "" {
		spec["commonName"] = cfg.CommonName
	}
	if len(cfg.URISANs) > 0 {
		uris := make([]interface{}, 0, len(cfg.URISANs))
		for _, u := range cfg.URISANs {
			uris = append(uris, u)
		}
		spec["uris"] = uris
	}
	switch cfg.Usage {
	case models.ManagedCertUsageClient:
		spec["usages"] = []interface{}{"client auth", "digital signature", "key encipherment"}
	default:
		spec["usages"] = []interface{}{"server auth", "digital signature", "key encipherment"}
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "Certificate",
		"metadata": map[string]interface{}{"name": cfg.Name, "namespace": cfg.Namespace, "labels": managedByLabels()},
		"spec":     spec,
	}}
}

// CertificateRequestConfig configures the CertificateRequest built by
// CertificateRequestObject.
type CertificateRequestConfig struct {
	Name         string
	Namespace    string
	IssuerName   string
	Request      []byte // PEM-encoded CSR bytes
	DurationDays int
}

// CertificateRequestObject builds a cert-manager CertificateRequest for
// CSR-mode issuance, where the caller generates the key and CSR and
// cert-manager only signs it. Used as the client-auth-only counterpart to
// LeafCertificate for callers that keep their own private key.
func CertificateRequestObject(config CertificateRequestConfig) *unstructured.Unstructured {
	spec := map[string]interface{}{
		"request": base64.StdEncoding.EncodeToString(config.Request),
		"issuerRef": map[string]interface{}{
			"name": config.IssuerName, "kind": "Issuer", "group": "cert-manager.io",
		},
		"usages": []interface{}{"client auth", "digital signature", "key encipherment"},
	}
	if config.DurationDays > 0 {
		spec["duration"] = hoursDuration(config.DurationDays)
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "CertificateRequest",
		"metadata": map[string]interface{}{"name": config.Name, "namespace": config.Namespace, "labels": managedByLabels()},
		"spec":     spec,
	}}
}

// CAIssuer builds a namespaced cert-manager Issuer of type "ca", signing
// with the key material in caSecretName (produced by CACertificate).
func CAIssuer(name, namespace, caSecretName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1", "kind": "Issuer",
		"metadata": map[string]interface{}{"name": name, "namespace": namespace, "labels": managedByLabels()},
		"spec":     map[string]interface{}{"ca": map[string]interface{}{"secretName": caSecretName}},
	}}
}

// ACMEIssuerConfig configures the ACME Issuer built by ACMEIssuer.
type ACMEIssuerConfig struct {
	Name, Namespace, Server, Email, AccountSecretName, EABKeyID, EABSecretName, ProviderType, SolverSecretName string
}

// ACMEIssuer builds a namespaced cert-manager Issuer of type "acme" with a
// single DNS-01 solver. EAB fields are optional; when EABKeyID is set, an
// externalAccountBinding is added (used by some ACME providers such as ZeroSSL).
func ACMEIssuer(cfg ACMEIssuerConfig) *unstructured.Unstructured {
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
		"apiVersion": "cert-manager.io/v1", "kind": "Issuer",
		"metadata": map[string]interface{}{"name": cfg.Name, "namespace": cfg.Namespace, "labels": managedByLabels()},
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
// referenced by an ACME Issuer's dns01.cloudflare.apiTokenSecretRef.
func CloudflareSolverSecret(name, namespace, apiToken string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1", "kind": "Secret",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace, "labels": managedByLabels()},
		"type":       "Opaque",
		"stringData": map[string]interface{}{"apiToken": apiToken},
	}}
}

// EABSecret builds the Secret holding an ACME External Account Binding HMAC
// key, referenced by an ACME Issuer's
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
