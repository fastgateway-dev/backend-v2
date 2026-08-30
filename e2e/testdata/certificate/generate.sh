#!/bin/bash
# Generate test certificates for FastGateway E2E testing
# Requires: step CLI (https://smallstep.com/docs/step-cli/)
#
# Creates 4 Root CAs, each with 2 server certs and 2 client certs.
# All certs are valid for 10 years (3650 days).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
VALIDITY="8760h" # 1 year

echo "Generating test certificates in ${SCRIPT_DIR}..."

###############################################################################
# Root CA 1 — Backend TLS testing
###############################################################################
DIR="${SCRIPT_DIR}/root-ca-1"
mkdir -p "${DIR}"

step certificate create "FastGateway Test Root CA 1" \
  "${DIR}/root-ca.crt" "${DIR}/root-ca.key" \
  --profile root-ca --no-password --insecure --force \
  --not-after "${VALIDITY}"

# Server certs (backends that the gateway connects TO)
step certificate create "backend-tls-server-1" \
  "${DIR}/server-1.crt" "${DIR}/server-1.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "backend-tls-server-1" \
  --san "backend-tls-server-1.default.svc.cluster.local" \
  --san "localhost" \
  --not-after "${VALIDITY}"

step certificate create "backend-tls-server-2" \
  "${DIR}/server-2.crt" "${DIR}/server-2.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "backend-tls-server-2" \
  --san "backend-tls-server-2.default.svc.cluster.local" \
  --san "localhost" \
  --not-after "${VALIDITY}"

# Client certs (gateway presents these to backends — used in backend-mtls)
step certificate create "backend-tls-client-1" \
  "${DIR}/client-1.crt" "${DIR}/client-1.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "gateway-client-1.fastgateway.local" \
  --not-after "${VALIDITY}"

step certificate create "backend-tls-client-2" \
  "${DIR}/client-2.crt" "${DIR}/client-2.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "gateway-client-2.fastgateway.local" \
  --not-after "${VALIDITY}"

echo "Root CA 1 done."

###############################################################################
# Root CA 2 — Backend mTLS testing
###############################################################################
DIR="${SCRIPT_DIR}/root-ca-2"
mkdir -p "${DIR}"

step certificate create "FastGateway Test Root CA 2" \
  "${DIR}/root-ca.crt" "${DIR}/root-ca.key" \
  --profile root-ca --no-password --insecure --force \
  --not-after "${VALIDITY}"

step certificate create "backend-mtls-server-1" \
  "${DIR}/server-1.crt" "${DIR}/server-1.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "backend-mtls-server-1" \
  --san "backend-mtls-server-1.default.svc.cluster.local" \
  --san "localhost" \
  --not-after "${VALIDITY}"

step certificate create "backend-mtls-server-2" \
  "${DIR}/server-2.crt" "${DIR}/server-2.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "backend-mtls-server-2" \
  --san "backend-mtls-server-2.default.svc.cluster.local" \
  --san "localhost" \
  --not-after "${VALIDITY}"

step certificate create "backend-mtls-client-1" \
  "${DIR}/client-1.crt" "${DIR}/client-1.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "gateway-client-1.fastgateway.local" \
  --not-after "${VALIDITY}"

step certificate create "backend-mtls-client-2" \
  "${DIR}/client-2.crt" "${DIR}/client-2.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "gateway-client-2.fastgateway.local" \
  --not-after "${VALIDITY}"

echo "Root CA 2 done."

###############################################################################
# Root CA 3 — Domain mTLS / Client mTLS testing (inbound)
###############################################################################
DIR="${SCRIPT_DIR}/root-ca-3"
mkdir -p "${DIR}"

step certificate create "FastGateway Test Root CA 3" \
  "${DIR}/root-ca.crt" "${DIR}/root-ca.key" \
  --profile root-ca --no-password --insecure --force \
  --not-after "${VALIDITY}"

step certificate create "domain-mtls-server-1" \
  "${DIR}/server-1.crt" "${DIR}/server-1.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "api.fastgateway.local" \
  --san "localhost" \
  --not-after "${VALIDITY}"

step certificate create "domain-mtls-server-2" \
  "${DIR}/server-2.crt" "${DIR}/server-2.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "api2.fastgateway.local" \
  --san "localhost" \
  --not-after "${VALIDITY}"

# Client certs — presented by end-users/services TO the gateway
# These have distinct DNS SANs and URI SANs for XFCC header matching
step certificate create "inbound-client-1" \
  "${DIR}/client-1.crt" "${DIR}/client-1.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "client-1.fastgateway.local" \
  --san "spiffe://fastgateway.local/client-1" \
  --not-after "${VALIDITY}"

step certificate create "inbound-client-2" \
  "${DIR}/client-2.crt" "${DIR}/client-2.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "client-2.fastgateway.local" \
  --san "spiffe://fastgateway.local/client-2" \
  --not-after "${VALIDITY}"

echo "Root CA 3 done."

###############################################################################
# Root CA 4 — Spare / additional testing
###############################################################################
DIR="${SCRIPT_DIR}/root-ca-4"
mkdir -p "${DIR}"

step certificate create "FastGateway Test Root CA 4" \
  "${DIR}/root-ca.crt" "${DIR}/root-ca.key" \
  --profile root-ca --no-password --insecure --force \
  --not-after "${VALIDITY}"

step certificate create "spare-server-1" \
  "${DIR}/server-1.crt" "${DIR}/server-1.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "spare-server-1.default.svc.cluster.local" \
  --san "localhost" \
  --not-after "${VALIDITY}"

step certificate create "spare-server-2" \
  "${DIR}/server-2.crt" "${DIR}/server-2.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "spare-server-2.default.svc.cluster.local" \
  --san "localhost" \
  --not-after "${VALIDITY}"

step certificate create "spare-client-1" \
  "${DIR}/client-1.crt" "${DIR}/client-1.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "spare-client-1.fastgateway.local" \
  --san "spiffe://fastgateway.local/spare-client-1" \
  --not-after "${VALIDITY}"

step certificate create "spare-client-2" \
  "${DIR}/client-2.crt" "${DIR}/client-2.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "spare-client-2.fastgateway.local" \
  --san "spiffe://fastgateway.local/spare-client-2" \
  --not-after "${VALIDITY}"

echo "Root CA 4 done."

###############################################################################
# Domain TLS Certificate (self-signed, gateway listener default)
###############################################################################
echo ""
echo "Generating domain TLS certificate..."

step certificate create "fastgateway.local" \
  "${SCRIPT_DIR}/tls.crt" "${SCRIPT_DIR}/tls.key" \
  --profile self-signed --subtle --no-password --insecure --force \
  --san "api.fastgateway.local" \
  --not-after "${VALIDITY}"

echo "Domain TLS certificate done."

###############################################################################
# TLS Domain Certificates (self-signed, individual per domain)
###############################################################################
echo ""
echo "Generating TLS domain certificates..."

step certificate create "tls1.fastgateway.local" \
  "${SCRIPT_DIR}/tls1.crt" "${SCRIPT_DIR}/tls1.key" \
  --profile self-signed --subtle --no-password --insecure --force \
  --san "tls1.fastgateway.local" \
  --not-after "${VALIDITY}"

step certificate create "tls2.fastgateway.local" \
  "${SCRIPT_DIR}/tls2.crt" "${SCRIPT_DIR}/tls2.key" \
  --profile self-signed --subtle --no-password --insecure --force \
  --san "tls2.fastgateway.local" \
  --not-after "${VALIDITY}"

step certificate create "tls3.fastgateway.local" \
  "${SCRIPT_DIR}/tls3.crt" "${SCRIPT_DIR}/tls3.key" \
  --profile self-signed --subtle --no-password --insecure --force \
  --san "tls3.fastgateway.local" \
  --not-after "${VALIDITY}"

echo "TLS domain certificates done."

###############################################################################
# mTLS Domain Certificates (signed by Root CA 3)
###############################################################################
echo ""
echo "Generating mTLS domain certificates..."
DIR="${SCRIPT_DIR}/root-ca-3"

step certificate create "mtls1.fastgateway.local" \
  "${DIR}/mtls1.crt" "${DIR}/mtls1.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "mtls1.fastgateway.local" \
  --san "localhost" \
  --not-after "${VALIDITY}"

step certificate create "mtls2.fastgateway.local" \
  "${DIR}/mtls2.crt" "${DIR}/mtls2.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "mtls2.fastgateway.local" \
  --san "localhost" \
  --not-after "${VALIDITY}"

step certificate create "mtls3.fastgateway.local" \
  "${DIR}/mtls3.crt" "${DIR}/mtls3.key" \
  --profile leaf --ca "${DIR}/root-ca.crt" --ca-key "${DIR}/root-ca.key" \
  --no-password --insecure --force \
  --san "mtls3.fastgateway.local" \
  --san "localhost" \
  --not-after "${VALIDITY}"

echo "mTLS domain certificates done."

###############################################################################
# Print certificate fingerprints for reference
###############################################################################
echo ""
echo "============================================"
echo "Certificate SHA-256 Fingerprints"
echo "============================================"
for ca_dir in root-ca-1 root-ca-2 root-ca-3 root-ca-4; do
  echo ""
  echo "--- ${ca_dir} ---"
  for cert in "${SCRIPT_DIR}/${ca_dir}"/*.crt; do
    name=$(basename "${cert}")
    hash=$(step certificate fingerprint "${cert}" 2>/dev/null || openssl x509 -in "${cert}" -noout -fingerprint -sha256 2>/dev/null | sed 's/.*=//')
    echo "  ${name}: ${hash}"
  done
done

echo ""
echo "============================================"
echo "Client cert SANs (for XFCC matching)"
echo "============================================"
for ca_dir in root-ca-3 root-ca-4; do
  echo ""
  echo "--- ${ca_dir} ---"
  for cert in "${SCRIPT_DIR}/${ca_dir}"/client-*.crt; do
    name=$(basename "${cert}")
    echo "  ${name}:"
    step certificate inspect "${cert}" --format json 2>/dev/null | \
      python3 -c "import sys,json; d=json.load(sys.stdin); sans=d.get('extensions',{}).get('subject_alternative_name',{}); [print(f'    DNS: {v}') for v in sans.get('dns_names',[])] ; [print(f'    URI: {v}') for v in sans.get('uris',[])]" 2>/dev/null || true
  done
done

echo ""
echo "All certificates generated successfully!"
