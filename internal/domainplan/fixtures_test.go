package domainplan

import (
	"github.com/fastgateway-dev/backend-v2/internal/kubernetes"
	"github.com/fastgateway-dev/backend-v2/internal/models"
	"github.com/google/uuid"
)

// domainManifestFixture is one domain-manifest input, snapshotted through the
// builder it names. Build closes over its own inputs so each fixture is a
// self-contained description of one branch through one builder.
type domainManifestFixture struct {
	Name  string
	Build func() any
}

// fixtureDomain is the shared domain for every fixture. Fixed UUIDs keep the
// golden output stable. Callers mutate the returned value to build variants --
// each call returns a fresh copy.
func fixtureDomain() *models.Domain {
	return &models.Domain{
		ID:             uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		ProjectID:      uuid.MustParse("55555555-5555-5555-5555-555555555555"),
		Name:           "example.com",
		Hostname:       "example.com",
		Namespace:      "gateway-ns",
		K8sGatewayName: "eg",
	}
}

// fixtureTemplateID is the fixed DomainTemplateID used by template fixtures.
var fixtureTemplateID = uuid.MustParse("66666666-6666-6666-6666-666666666666")

func strPtr(s string) *string { return &s }
func i32Ptr(i int32) *int32   { return &i }
func i64Ptr(i int64) *int64   { return &i }
func intPtr(i int) *int       { return &i }

// caRef builds a CA secret ref shaped like the ones
// DomainService.collectCASecretRefs produces (Group always "", Kind "Secret").
func caRef(name string) kubernetes.SecretRefPolicyConfig {
	return kubernetes.SecretRefPolicyConfig{Group: "", Kind: "Secret", Name: name}
}

// ─── BuildGatewayConfig fixtures ─────────────────────────────────────────────

func gatewayFixtures() []domainManifestFixture {
	return []domainManifestFixture{
		// The control case: nothing but the identity fields the brief's base
		// domain carries. Every zero-valued mapping is visible here.
		{
			Name: "gateway-bare",
			Build: func() any {
				return BuildGatewayConfig(fixtureDomain(), nil)
			},
		},
		// Every field BuildGatewayConfig actually maps EXCEPT
		// TLSSecretNamespace, set to a distinct non-zero value. If a future
		// edit drops one of the ten mappings this golden is the one that
		// catches it. TLSSecretNamespace is covered instead by the
		// "gateway-tls-secret-namespace" and "gateway-deploying-domain"
		// fixtures below and by the F2 unit test
		// (TestBuildGatewayConfig_MapsTLSSecretNamespace in
		// domainplan_test.go).
		{
			Name: "gateway-all-mapped-fields",
			Build: func() any {
				d := fixtureDomain()
				d.K8sGatewayClass = "envoy-gateway-class"
				d.TLSMode = "both"
				d.HTTPPort = 8080
				d.HTTPSPort = 8443
				d.TLSSecretName = "example-com-tls"
				d.TLSPolicy = models.TLSPolicyTerminate
				return BuildGatewayConfig(d, nil)
			},
		},
		// Template annotations are attached only when DomainTemplateID is set.
		{
			Name: "gateway-with-template-annotations",
			Build: func() any {
				d := fixtureDomain()
				d.DomainTemplateID = &fixtureTemplateID
				return BuildGatewayConfig(d, map[string]string{
					"service.beta.kubernetes.io/aws-load-balancer-type":   "nlb",
					"service.beta.kubernetes.io/aws-load-balancer-scheme": "internal",
				})
			},
		},
		// F4 (cosmetic finding from Task 1): annotations supplied for a domain
		// with no template ID are silently DISCARDED by the
		// `if domain.DomainTemplateID != nil` guard. Pinned so that removing
		// the guard -- which would otherwise look like a harmless
		// simplification -- shows up as a golden diff.
		{
			Name: "gateway-annotations-without-template-id",
			Build: func() any {
				d := fixtureDomain()
				d.DomainTemplateID = nil
				return BuildGatewayConfig(d, map[string]string{
					"discarded.example.com/annotation": "never-reaches-the-gateway",
				})
			},
		},
		// F2, closed in Phase 2H. models.Domain.TLSSecretNamespace is
		// user-settable and kubernetes.GatewayConfig has a
		// TLSSecretNamespace field that kubernetes.BuildGatewayObject uses
		// to emit a cross-namespace certificateRefs[].namespace
		// (internal/kubernetes/gateway.go:63-66). BuildGatewayConfig now maps
		// it, so a domain whose TLS secret lives in another namespace gets a
		// certificateRef carrying that namespace, and Envoy Gateway resolves
		// the secret where it actually lives.
		//
		// This golden used to pin the OPPOSITE of this comment, under the
		// name "gateway-tls-secret-namespace-dropped-f2": the field was
		// silently omitted, which meant the preview builder disagreed with
		// the deploying path (domain_service.go:297), which set the field
		// directly and worked. That was F2, mis-recorded at the time as
		// "DEAD on the domain path" when it was in fact only dead on the
		// preview path. Closing F2 in Phase 2H changed this golden's content
		// and its name, dropping the defect marker.
		{
			Name: "gateway-tls-secret-namespace",
			Build: func() any {
				d := fixtureDomain()
				d.TLSMode = "tls_only"
				d.TLSSecretName = "example-com-tls"
				d.TLSSecretNamespace = "cert-manager-ns"
				d.TLSPolicy = models.TLSPolicyTerminate
				return BuildGatewayConfig(d, nil)
			},
		},
		// Phase 2H. Characterizes domain_service.go:297, the path that
		// actually deploys the Gateway. Before this phase it assembled its
		// own kubernetes.GatewayConfig literal, bypassing this builder
		// entirely, and no golden covered it -- this fixture and
		// TestDomainService_DeployGatewayConfig_MatchesDomainplanBuilder
		// (internal/services/domain_service_test.go) are its first
		// coverage. Every field the deploying call site sets is non-default
		// here, including TLSSecretNamespace (F2) and template annotations
		// (always attached: Create requires a domain template, so
		// DomainTemplateID is never nil at that call site).
		{
			Name: "gateway-deploying-domain",
			Build: func() any {
				d := fixtureDomain()
				d.K8sGatewayClass = "example-public"
				d.TLSMode = "tls_only"
				d.HTTPPort = 80
				d.HTTPSPort = 443
				d.TLSSecretName = "wildcard-tls"
				d.TLSSecretNamespace = "shared-certs"
				d.TLSPolicy = models.TLSPolicyTerminate
				d.DomainTemplateID = &fixtureTemplateID
				return BuildGatewayConfig(d, map[string]string{"a": "1"})
			},
		},
		// Phase 2H. Characterizes domain_template_service.go's example
		// Gateway (PreviewCreate), built from a synthetic *models.Domain so
		// the illustrative example cannot drift from what actually deploys.
		// The synthetic domain carries no DomainTemplateID and no
		// TLSSecretNamespace -- matching the literal it replaced, which
		// never set either.
		{
			Name: "gateway-example-domain",
			Build: func() any {
				d := &models.Domain{
					K8sGatewayName:  "example-domain",
					Namespace:       "envoy-gateway-system",
					K8sGatewayClass: "example-domain-public",
					Hostname:        "example.com",
					TLSMode:         "both",
					HTTPPort:        80,
					HTTPSPort:       443,
					TLSSecretName:   "example-tls-cert",
					TLSPolicy:       models.TLSPolicyTerminate,
				}
				return BuildGatewayConfig(d, nil)
			},
		},
	}
}

// ─── BuildClientTrafficPolicyConfig fixtures ─────────────────────────────────

func clientTrafficPolicyFixtures() []domainManifestFixture {
	return []domainManifestFixture{
		// Base shape only: name, namespace, gateway ID and target ref. Every
		// optional block absent.
		{
			Name: "ctp-empty-settings",
			Build: func() any {
				return BuildClientTrafficPolicyConfig(fixtureDomain(), &models.DomainSettingsConfig{}, nil)
			},
		},
		// Every ClientConnection sub-branch at once: TCP keepalive, PROXY
		// protocol, connection limits and buffer limit.
		{
			Name: "ctp-client-connection-full",
			Build: func() any {
				cfg := &models.DomainSettingsConfig{
					ClientConnection: &models.ClientConnectionConfig{
						TCPKeepalive: &models.TCPKeepaliveConfig{
							Probes:   i32Ptr(3),
							IdleTime: strPtr("60s"),
							Interval: strPtr("10s"),
						},
						ProxyProtocol: &models.ProxyProtocolConfig{Enabled: true},
						ConnectionLimit: &models.ConnectionLimitConfig{
							MaxConnections:           i32Ptr(1000),
							CloseDelay:               strPtr("5s"),
							MaxConnectionDuration:    strPtr("1h"),
							MaxRequestsPerConnection: i32Ptr(100),
						},
						BufferLimit: strPtr("32Ki"),
					},
				}
				return BuildClientTrafficPolicyConfig(fixtureDomain(), cfg, nil)
			},
		},
		// BufferLimit alone still allocates the Connection block; ProxyProtocol
		// present-but-disabled must NOT set EnableProxyProtocol.
		{
			Name: "ctp-client-connection-buffer-limit-only",
			Build: func() any {
				cfg := &models.DomainSettingsConfig{
					ClientConnection: &models.ClientConnectionConfig{
						ProxyProtocol: &models.ProxyProtocolConfig{Enabled: false},
						BufferLimit:   strPtr("64Ki"),
					},
				}
				return BuildClientTrafficPolicyConfig(fixtureDomain(), cfg, nil)
			},
		},
		{
			Name: "ctp-timeout-and-http3",
			Build: func() any {
				cfg := &models.DomainSettingsConfig{
					Timeout: &models.TimeoutConfig{
						HTTP: &models.HTTPTimeoutConfig{
							RequestReceivedTimeout: strPtr("30s"),
							IdleTimeout:            strPtr("60s"),
						},
					},
					HTTP3: &models.HTTP3Config{Enabled: true},
				}
				return BuildClientTrafficPolicyConfig(fixtureDomain(), cfg, nil)
			},
		},
		// "TLS configured" -- every TLSSettingsConfig field populated.
		{
			Name: "ctp-tls-configured",
			Build: func() any {
				cfg := &models.DomainSettingsConfig{
					TLS: &models.TLSSettingsConfig{
						MinVersion: strPtr("TLS1.2"),
						MaxVersion: strPtr("TLS1.3"),
						Ciphers: []string{
							"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
							"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
						},
						ECDHCurves:          []string{"X25519", "P-256"},
						SignatureAlgorithms: []string{"ecdsa_secp256r1_sha256"},
					},
				}
				return BuildClientTrafficPolicyConfig(fixtureDomain(), cfg, nil)
			},
		},
		// TLS present but IsEmpty() -- the block must be omitted entirely.
		{
			Name: "ctp-tls-present-but-empty",
			Build: func() any {
				cfg := &models.DomainSettingsConfig{
					TLS: &models.TLSSettingsConfig{MinVersion: strPtr("")},
				}
				return BuildClientTrafficPolicyConfig(fixtureDomain(), cfg, nil)
			},
		},
		{
			Name: "ctp-client-ip-detection-xff",
			Build: func() any {
				cfg := &models.DomainSettingsConfig{
					ClientIPDetection: &models.ClientIPDetectionConfig{
						XForwardedFor: &models.XForwardedForConfig{NumTrustedHops: 2},
					},
				}
				return BuildClientTrafficPolicyConfig(fixtureDomain(), cfg, nil)
			},
		},
		{
			Name: "ctp-client-ip-detection-custom-header",
			Build: func() any {
				cfg := &models.DomainSettingsConfig{
					ClientIPDetection: &models.ClientIPDetectionConfig{
						CustomHeader: &models.CustomHeaderConfig{
							Name:       "CF-Connecting-IP",
							FailClosed: true,
						},
					},
				}
				return BuildClientTrafficPolicyConfig(fixtureDomain(), cfg, nil)
			},
		},
		// mTLS, one CA, cert REQUIRED (Optional false). The XFCC headers block
		// rides along with ClientValidation.
		{
			Name: "ctp-mtls-single-ca",
			Build: func() any {
				cfg := &models.DomainSettingsConfig{
					MTLS: &models.DomainMTLSConfig{
						Enabled:  true,
						Optional: false,
						CACerts: []models.MTLSCACert{
							{ID: "ca-1", Name: "Corp Root", SecretName: "corp-root-ca", SecretKey: "ca.crt"},
						},
					},
				}
				return BuildClientTrafficPolicyConfig(fixtureDomain(), cfg, []kubernetes.SecretRefPolicyConfig{
					caRef("corp-root-ca"),
				})
			},
		},
		// mTLS, several CAs, cert OPTIONAL, plus SAN matchers and certificate
		// hashes -- the full ClientValidation shape and its ordering.
		{
			Name: "ctp-mtls-multiple-cas",
			Build: func() any {
				cfg := &models.DomainSettingsConfig{
					MTLS: &models.DomainMTLSConfig{
						Enabled:  true,
						Optional: true,
						CACerts: []models.MTLSCACert{
							{ID: "ca-1", Name: "Corp Root", SecretName: "corp-root-ca", SecretKey: "ca.crt"},
							{ID: "ca-2", Name: "Partner Root", SecretName: "partner-root-ca", SecretKey: "ca.crt"},
						},
						SANWhitelist: []models.MTLSSANEntry{
							{Type: "DNS", Value: "client.example.com"},
							{Type: "URI", Value: "spiffe://example.com/svc/api"},
						},
						HashWhitelist: []string{
							"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
						},
					},
				}
				return BuildClientTrafficPolicyConfig(fixtureDomain(), cfg, []kubernetes.SecretRefPolicyConfig{
					caRef("corp-root-ca"),
					caRef("partner-root-ca"),
					caRef("client-attachment-ca"), // contributed by a client mTLS attachment
				})
			},
		},
		// F3 -- FAIL-OPEN ON A SECURITY CONTROL, FIXED in Phase 2G.
		//
		// clienttrafficpolicy.go:86 used to guard on
		//   config.MTLS != nil && config.MTLS.Enabled && len(caSecretRefs) > 0
		// so when mTLS was ENABLED but caSecretRefs came back empty, the entire
		// ClientValidation block -- CACertificateRefs, SANMatchers,
		// CertificateHashes -- AND the XFCC Headers block were silently omitted.
		// The ClientTrafficPolicy then applied successfully with NO client
		// certificate verification at all, and nothing was logged at the point
		// of the drop.
		//
		// This was reachable in production via the producer,
		// DomainService.collectCASecretRefs (internal/services/domain_mtls.go):
		// at the time, it LOGGED AND CONTINUED on a repository error, so a domain
		// whose CAs came only from client attachments could yield an empty slice
		// on a transient DB error. Phase 2G Task 4 closed that path: it now
		// RETURNS the error instead, so a repository failure no longer reaches
		// this guard disguised as an empty ref list. The guard fixed here still
		// matters on its own, though: a domain configuration that legitimately
		// resolves to zero CA refs (no domain-level CACerts, no active client
		// mTLS attachments) reaches this same empty-refs input with no error at
		// all. ClientTrafficPolicy is GATEWAY-scoped, so the fail-open covered
		// every route behind the domain, not one route. This fixture sets
		// Optional:false -- the intent is ENFORCED client-cert auth -- and SAN +
		// hash whitelists, so the golden shows the ClientValidation and Headers
		// blocks now surviving instead of vanishing.
		//
		// Since Phase 2G, the guard no longer checks len(caSecretRefs) > 0: the
		// block is emitted with whatever refs materialised (here, none), which
		// makes Envoy reject the policy instead of silently applying with no
		// client-certificate validation. This golden now records that FIXED
		// output; it used to be byte-identical to ctp-empty-settings.yaml
		// (pinning the fail-open) and no longer is.
		{
			Name: "ctp-mtls-enabled-no-ca-refs",
			Build: func() any {
				cfg := &models.DomainSettingsConfig{
					MTLS: &models.DomainMTLSConfig{
						Enabled:  true,
						Optional: false, // client cert REQUIRED -- and yet
						SANWhitelist: []models.MTLSSANEntry{
							{Type: "DNS", Value: "client.example.com"},
						},
						HashWhitelist: []string{
							"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
						},
					},
				}
				// Empty refs: what collectCASecretRefs returns after a repo error.
				return BuildClientTrafficPolicyConfig(fixtureDomain(), cfg, nil)
			},
		},
		// The other side of the same guard: refs resolved but mTLS switched off.
		// Dropping ClientValidation here IS correct.
		{
			Name: "ctp-mtls-disabled-with-ca-refs",
			Build: func() any {
				cfg := &models.DomainSettingsConfig{
					MTLS: &models.DomainMTLSConfig{
						Enabled: false,
						CACerts: []models.MTLSCACert{
							{ID: "ca-1", Name: "Corp Root", SecretName: "corp-root-ca", SecretKey: "ca.crt"},
						},
					},
				}
				return BuildClientTrafficPolicyConfig(fixtureDomain(), cfg, []kubernetes.SecretRefPolicyConfig{
					caRef("corp-root-ca"),
				})
			},
		},
		// Everything at once, to pin field ordering across the whole struct.
		{
			Name: "ctp-all-settings",
			Build: func() any {
				cfg := &models.DomainSettingsConfig{
					ClientConnection: &models.ClientConnectionConfig{
						TCPKeepalive:    &models.TCPKeepaliveConfig{Probes: i32Ptr(9), IdleTime: strPtr("75s"), Interval: strPtr("15s")},
						ProxyProtocol:   &models.ProxyProtocolConfig{Enabled: true},
						ConnectionLimit: &models.ConnectionLimitConfig{MaxConnections: i32Ptr(2048)},
						BufferLimit:     strPtr("1Mi"),
					},
					ClientIPDetection: &models.ClientIPDetectionConfig{
						XForwardedFor: &models.XForwardedForConfig{NumTrustedHops: 1},
					},
					Timeout: &models.TimeoutConfig{
						HTTP: &models.HTTPTimeoutConfig{RequestReceivedTimeout: strPtr("10s"), IdleTimeout: strPtr("120s")},
					},
					HTTP3: &models.HTTP3Config{Enabled: true},
					TLS:   &models.TLSSettingsConfig{MinVersion: strPtr("TLS1.3"), MaxVersion: strPtr("TLS1.3")},
					MTLS: &models.DomainMTLSConfig{
						Enabled:  true,
						Optional: false,
						CACerts:  []models.MTLSCACert{{ID: "ca-1", Name: "Corp Root", SecretName: "corp-root-ca", SecretKey: "ca.crt"}},
					},
				}
				return BuildClientTrafficPolicyConfig(fixtureDomain(), cfg, []kubernetes.SecretRefPolicyConfig{
					caRef("corp-root-ca"),
				})
			},
		},
	}
}

// ─── BuildBackendTrafficPolicyConfig fixtures ────────────────────────────────

func backendTrafficPolicyFixtures() []domainManifestFixture {
	return []domainManifestFixture{
		// Domain-level BTP: targets the Gateway, RouteID empty, DomainID set.
		{
			Name: "btp-domain-level-retry",
			Build: func() any {
				cfg := &models.BackendTrafficPolicyConfig{
					Retry: &models.RetryConfig{
						NumRetries: i32Ptr(5),
						RetryOn: &models.RetryOn{
							HTTPStatusCodes: []int{502, 503},
							Triggers:        []string{"5xx", "reset"},
						},
						PerRetryPolicy: &models.PerRetryPolicy{
							Timeout: strPtr("2s"),
							BackOff: &models.BackOffPolicy{
								BaseInterval: strPtr("100ms"),
								MaxInterval:  strPtr("1s"),
							},
						},
					},
				}
				return BuildBackendTrafficPolicyConfig(fixtureDomain(), cfg)
			},
		},
		// All three known compression types, each selecting a different arm of
		// the switch.
		{
			Name: "btp-compression-all-types",
			Build: func() any {
				cfg := &models.BackendTrafficPolicyConfig{
					Compression: []models.CompressionConfig{
						{Type: models.CompressionTypeGzip, Gzip: &models.GzipConfig{}},
						{Type: models.CompressionTypeBrotli, Brotli: &models.BrotliConfig{}},
						{Type: models.CompressionTypeZstd, Zstd: &models.ZstdConfig{}},
					},
				}
				return BuildBackendTrafficPolicyConfig(fixtureDomain(), cfg)
			},
		},
		// The switch's implicit default arm: an unrecognised compression type
		// is copied through with NO algorithm sub-config attached.
		{
			Name: "btp-compression-unknown-type",
			Build: func() any {
				cfg := &models.BackendTrafficPolicyConfig{
					Compression: []models.CompressionConfig{
						{Type: models.CompressionType("Snappy")},
					},
				}
				return BuildBackendTrafficPolicyConfig(fixtureDomain(), cfg)
			},
		},
		{
			Name: "btp-load-balancer-and-circuit-breaker",
			Build: func() any {
				cfg := &models.BackendTrafficPolicyConfig{
					LoadBalancer: &models.LoadBalancerConfig{
						Type: models.LoadBalancerTypeConsistentHash,
						ConsistentHash: &models.ConsistentHashConfig{
							Type:   models.ConsistentHashTypeHeader,
							Header: &models.ConsistentHashHeader{Name: "x-user-id"},
						},
					},
					CircuitBreaker: &models.CircuitBreakerConfig{
						MaxConnections:           i64Ptr(100),
						MaxPendingRequests:       i64Ptr(50),
						MaxParallelRequests:      i64Ptr(25),
						MaxParallelRetries:       i64Ptr(3),
						MaxRequestsPerConnection: i64Ptr(10),
					},
				}
				return BuildBackendTrafficPolicyConfig(fixtureDomain(), cfg)
			},
		},
		{
			Name: "btp-request-buffer-response-override-timeout",
			Build: func() any {
				cfg := &models.BackendTrafficPolicyConfig{
					RequestBuffer: &models.RequestBufferConfig{Limit: "4Ki"},
					ResponseOverride: []models.ResponseOverrideRule{
						{
							Match: models.ResponseOverrideMatch{
								StatusCodes: []models.StatusCodeMatch{
									{Type: "Value", Value: intPtr(503)},
								},
							},
							Response: models.ResponseOverrideResponse{
								ContentType: "text/plain",
								Body: models.ResponseOverrideBody{
									Type:   "Inline",
									Inline: "service unavailable",
								},
							},
						},
					},
					Timeout: &models.BTPTimeoutConfig{
						TCP: &models.BTPTCPTimeoutConfig{ConnectTimeout: "5s"},
						HTTP: &models.BTPHTTPTimeoutConfig{
							RequestTimeout:        "30s",
							ConnectionIdleTimeout: "60s",
							MaxConnectionDuration: "1h",
							MaxStreamDuration:     "10m",
						},
					},
				}
				return BuildBackendTrafficPolicyConfig(fixtureDomain(), cfg)
			},
		},
		// HealthCheck / FaultInjection / RateLimit make IsEmpty() false -- so the
		// builder returns a non-nil config -- yet the domain-level builder maps
		// NONE of them, while the route path
		// (internal/routeplan/backendtrafficpolicy.go:72-82) maps all three.
		// The result is a BackendTrafficPolicy with an entirely empty body.
		//
		// NOT a live defect: DomainService rejects all three at domain level
		// before the builder is reached ("healthCheck is not supported at
		// domain level", internal/services/domain_service.go:598-607). This
		// golden pins the CONTRACT BOUNDARY -- the builder itself has no such
		// guard, so if that upstream validation is ever relaxed, an empty
		// policy is what would ship. The golden makes that consequence visible
		// at the point the guard is removed.
		{
			Name: "btp-unmapped-fields-only",
			Build: func() any {
				cfg := &models.BackendTrafficPolicyConfig{
					HealthCheck: &models.HealthCheckConfig{},
				}
				return BuildBackendTrafficPolicyConfig(fixtureDomain(), cfg)
			},
		},
	}
}

// ─── BuildEnvoyExtensionPolicyConfig fixtures ────────────────────────────────

func envoyExtensionPolicyFixtures() []domainManifestFixture {
	return []domainManifestFixture{
		{
			Name: "eep-lua-inline",
			Build: func() any {
				cfg := &models.EnvoyExtensionPolicyConfig{
					Lua: &models.LuaExtensionConfig{
						Type:   "Inline",
						Inline: "function envoy_on_request(handle) handle:logInfo('hello') end",
					},
				}
				return BuildEnvoyExtensionPolicyConfig(fixtureDomain(), cfg)
			},
		},
		{
			Name: "eep-lua-value-ref",
			Build: func() any {
				cfg := &models.EnvoyExtensionPolicyConfig{
					Lua: &models.LuaExtensionConfig{
						Type: "ValueRef",
						ValueRef: &models.ValueRef{
							Group:     "",
							Kind:      "ConfigMap",
							Name:      "lua-scripts",
							Namespace: "gateway-ns",
						},
					},
				}
				return BuildEnvoyExtensionPolicyConfig(fixtureDomain(), cfg)
			},
		},
		{
			Name: "eep-wasm-http",
			Build: func() any {
				wasmCfg := `{"key":"value"}`
				cfg := &models.EnvoyExtensionPolicyConfig{
					Wasm: &models.WasmExtensionConfig{
						Name:   "my-wasm-filter",
						RootID: "my-root",
						Code: models.WasmCodeSource{
							Type: "HTTP",
							HTTP: &models.WasmHTTPSource{
								URL:    "https://example.com/filter.wasm",
								SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
							},
						},
						Config: &wasmCfg,
					},
				}
				return BuildEnvoyExtensionPolicyConfig(fixtureDomain(), cfg)
			},
		},
		{
			Name: "eep-wasm-image-with-pull-secret",
			Build: func() any {
				cfg := &models.EnvoyExtensionPolicyConfig{
					Wasm: &models.WasmExtensionConfig{
						Name: "oci-wasm-filter",
						Code: models.WasmCodeSource{
							Type: "Image",
							Image: &models.WasmImageSource{
								URL:    "oci://registry.example.com/filters/auth:v1",
								SHA256: "b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3",
								PullSecret: &models.ValueRef{
									Group:     "",
									Kind:      "Secret",
									Name:      "registry-pull-secret",
									Namespace: "gateway-ns",
								},
							},
						},
					},
				}
				return BuildEnvoyExtensionPolicyConfig(fixtureDomain(), cfg)
			},
		},
		// Image source WITHOUT a pull secret: the nested nil guard.
		{
			Name: "eep-wasm-image-no-pull-secret",
			Build: func() any {
				cfg := &models.EnvoyExtensionPolicyConfig{
					Wasm: &models.WasmExtensionConfig{
						Name: "public-oci-wasm-filter",
						Code: models.WasmCodeSource{
							Type:  "Image",
							Image: &models.WasmImageSource{URL: "oci://registry.example.com/filters/public:v1"},
						},
					},
				}
				return BuildEnvoyExtensionPolicyConfig(fixtureDomain(), cfg)
			},
		},
		{
			Name: "eep-extproc-full",
			Build: func() any {
				cfg := &models.EnvoyExtensionPolicyConfig{
					ExtProc: &models.ExtProcExtensionConfig{
						BackendRef: models.ExtProcBackendRef{
							Name:      "ext-processor",
							Namespace: "processing-ns",
							Port:      9001,
						},
						ProcessingMode: &models.ExtProcProcessingMode{
							Request:  &models.ExtProcBodyMode{Body: "Buffered"},
							Response: &models.ExtProcBodyMode{Body: "Streamed"},
						},
						FailOpen: true,
					},
				}
				return BuildEnvoyExtensionPolicyConfig(fixtureDomain(), cfg)
			},
		},
		// ProcessingMode nil, and the request-only sub-branch is covered by the
		// asymmetric mode below.
		{
			Name: "eep-extproc-no-processing-mode",
			Build: func() any {
				cfg := &models.EnvoyExtensionPolicyConfig{
					ExtProc: &models.ExtProcExtensionConfig{
						BackendRef: models.ExtProcBackendRef{
							Name:      "ext-processor",
							Namespace: "processing-ns",
							Port:      9001,
						},
						FailOpen: false,
					},
				}
				return BuildEnvoyExtensionPolicyConfig(fixtureDomain(), cfg)
			},
		},
		{
			Name: "eep-extproc-request-mode-only",
			Build: func() any {
				cfg := &models.EnvoyExtensionPolicyConfig{
					ExtProc: &models.ExtProcExtensionConfig{
						BackendRef: models.ExtProcBackendRef{
							Name:      "ext-processor",
							Namespace: "processing-ns",
							Port:      9001,
						},
						ProcessingMode: &models.ExtProcProcessingMode{
							Request: &models.ExtProcBodyMode{Body: "Buffered"},
						},
					},
				}
				return BuildEnvoyExtensionPolicyConfig(fixtureDomain(), cfg)
			},
		},
		// All three extension kinds on one policy.
		{
			Name: "eep-all-extensions",
			Build: func() any {
				wasmCfg := `{"mode":"strict"}`
				cfg := &models.EnvoyExtensionPolicyConfig{
					Lua: &models.LuaExtensionConfig{
						Type:   "Inline",
						Inline: "function envoy_on_response(handle) end",
					},
					Wasm: &models.WasmExtensionConfig{
						Name:   "my-wasm-filter",
						RootID: "my-root",
						Code: models.WasmCodeSource{
							Type: "HTTP",
							HTTP: &models.WasmHTTPSource{
								URL:    "https://example.com/filter.wasm",
								SHA256: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
							},
						},
						Config: &wasmCfg,
					},
					ExtProc: &models.ExtProcExtensionConfig{
						BackendRef: models.ExtProcBackendRef{
							Name:      "ext-processor",
							Namespace: "processing-ns",
							Port:      9001,
						},
						FailOpen: true,
					},
				}
				return BuildEnvoyExtensionPolicyConfig(fixtureDomain(), cfg)
			},
		},
	}
}

// domainFixtures returns every golden fixture across all four builders.
func domainFixtures() []domainManifestFixture {
	var fixtures []domainManifestFixture
	fixtures = append(fixtures, gatewayFixtures()...)
	fixtures = append(fixtures, clientTrafficPolicyFixtures()...)
	fixtures = append(fixtures, backendTrafficPolicyFixtures()...)
	fixtures = append(fixtures, envoyExtensionPolicyFixtures()...)
	return fixtures
}
