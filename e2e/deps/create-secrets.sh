#!/bin/bash
# Create Kubernetes secrets for E2E testing.
# Run from the repo root: bash e2e/deps/create-secrets.sh
#
# Uses the kubeconfig's current context by default (this is what CI and
# most local runs want). To target a specific context instead -- e.g. a
# local orbstack cluster -- export KUBE_CONTEXT first:
#   KUBE_CONTEXT=orbstack bash e2e/deps/create-secrets.sh
# Same convention as e2e/harness/config.go's KubeContext: empty/unset
# means "use the current context".

set -euo pipefail

# Array, not a plain string: a plain "${KUBE_CONTEXT:+--context=$KUBE_CONTEXT}"
# relies on word-splitting when it's later used unquoted as `kubectl "${CTX[@]}"`,
# which breaks on a context name containing whitespace. An array expands
# safely with `kubectl "${CTX[@]}"` regardless of what's inside it.
CTX=()
[ -n "${KUBE_CONTEXT:-}" ] && CTX=(--context="$KUBE_CONTEXT")
CERT_DIR="$(cd "$(dirname "$0")/../testdata/certificate" && pwd)"

# e2e/testdata/certificate/ always exists in a fresh checkout (only
# README.md and generate.sh are tracked in git -- everything else,
# including tls.crt, is gitignored and generated), so `cd`-ing into it
# above succeeds even when no certificates have been generated yet. Fail
# here with a clear pointer to the prerequisite instead of dying later on
# a bare "no such file or directory" from one of the `--cert=`/`--from-file=`
# flags below.
if [ ! -f "${CERT_DIR}/tls.crt" ]; then
  echo "error: no certificates found in ${CERT_DIR} (missing tls.crt)." >&2
  echo "Run e2e/testdata/certificate/generate.sh first to generate the e2e test PKI." >&2
  exit 1
fi

echo "==> Using context: ${KUBE_CONTEXT:-$(kubectl config current-context)}"

# --- fastgateway-system namespace (secrets referenced by FastGateway routes) ---

NS_FG="fastgateway-system"

echo "==> Creating namespace $NS_FG (if not exists)..."
kubectl "${CTX[@]}" create namespace "$NS_FG" --dry-run=client -o yaml | kubectl "${CTX[@]}" apply -f -

echo "==> [$NS_FG] backend-tls-ca (Root CA 1 - gateway verifies backend cert)..."
kubectl "${CTX[@]}" create secret generic backend-tls-ca \
  --namespace="$NS_FG" \
  --from-file=ca.crt="$CERT_DIR/root-ca-1/root-ca.crt" \
  --dry-run=client -o yaml | kubectl "${CTX[@]}" apply -f -

echo "==> [$NS_FG] backend-mtls-ca (Root CA 2 - gateway verifies backend cert for mTLS)..."
kubectl "${CTX[@]}" create secret generic backend-mtls-ca \
  --namespace="$NS_FG" \
  --from-file=ca.crt="$CERT_DIR/root-ca-2/root-ca.crt" \
  --dry-run=client -o yaml | kubectl "${CTX[@]}" apply -f -

echo "==> [$NS_FG] backend-mtls-client (Root CA 2 - gateway client cert for mTLS)..."
kubectl "${CTX[@]}" create secret tls backend-mtls-client \
  --namespace="$NS_FG" \
  --cert="$CERT_DIR/root-ca-2/client-1.crt" \
  --key="$CERT_DIR/root-ca-2/client-1.key" \
  --dry-run=client -o yaml | kubectl "${CTX[@]}" apply -f -

echo "==> [$NS_FG] domain-tls (Gateway listener TLS cert)..."
kubectl "${CTX[@]}" create secret tls domain-tls \
  --namespace="$NS_FG" \
  --cert="$CERT_DIR/tls.crt" \
  --key="$CERT_DIR/tls.key" \
  --dry-run=client -o yaml | kubectl "${CTX[@]}" apply -f -

echo "==> [$NS_FG] domain-mtls-ca (Root CA 3 - inbound domain mTLS)..."
kubectl "${CTX[@]}" create secret generic domain-mtls-ca \
  --namespace="$NS_FG" \
  --from-file=ca.crt="$CERT_DIR/root-ca-3/root-ca.crt" \
  --dry-run=client -o yaml | kubectl "${CTX[@]}" apply -f -

# Envoy Gateway's APIKeyAuth SecurityPolicy expects an Opaque Secret with
# each API key stored under a key named for its client ID (verified
# against Envoy Gateway's apikey-auth docs: "The secret is an Opaque
# secret, with each API key stored under a key corresponding to the
# client ID"). Fixed name/client-id/value so tests can reference them
# directly -- e2e/suites/security's general-mode API-key tests and
# e2e/suites/grpcroute's security_api_key_test.go point their
# APIKeyAuth.SecretName at this Secret.
echo "==> [$NS_FG] e2e-apikey-auth (APIKeyAuth SecurityPolicy credential)..."
kubectl "${CTX[@]}" create secret generic e2e-apikey-auth \
  --namespace="$NS_FG" \
  --from-literal=e2e-client="e2e-test-api-key-not-for-production" \
  --dry-run=client -o yaml | kubectl "${CTX[@]}" apply -f -

# --- default namespace (secrets mounted by backend TLS/mTLS nginx pods) ---

NS_DEF="default"

echo "==> [$NS_DEF] backend-tls-server-cert (Root CA 1 - server cert for TLS nginx)..."
kubectl "${CTX[@]}" create secret tls backend-tls-server-cert \
  --namespace="$NS_DEF" \
  --cert="$CERT_DIR/root-ca-1/server-1.crt" \
  --key="$CERT_DIR/root-ca-1/server-1.key" \
  --dry-run=client -o yaml | kubectl "${CTX[@]}" apply -f -

echo "==> [$NS_DEF] backend-mtls-server-cert (Root CA 2 - server cert for mTLS nginx)..."
kubectl "${CTX[@]}" create secret tls backend-mtls-server-cert \
  --namespace="$NS_DEF" \
  --cert="$CERT_DIR/root-ca-2/server-1.crt" \
  --key="$CERT_DIR/root-ca-2/server-1.key" \
  --dry-run=client -o yaml | kubectl "${CTX[@]}" apply -f -

echo "==> [$NS_DEF] backend-mtls-client-ca (Root CA 2 - CA cert for mTLS client verification)..."
kubectl "${CTX[@]}" create secret generic backend-mtls-client-ca \
  --namespace="$NS_DEF" \
  --from-file=ca.crt="$CERT_DIR/root-ca-2/root-ca.crt" \
  --dry-run=client -o yaml | kubectl "${CTX[@]}" apply -f -

echo "Done! All secrets created."
