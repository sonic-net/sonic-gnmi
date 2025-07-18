#!/bin/bash
# Generate self-signed certificates for testing TLS functionality

set -e

CERT_DIR="${1:-./}"
CERT_FILE="${CERT_DIR}/server.crt"
KEY_FILE="${CERT_DIR}/server.key"
CA_CERT_FILE="${CERT_DIR}/ca.crt"
CA_KEY_FILE="${CERT_DIR}/ca.key"

echo "Generating test certificates in ${CERT_DIR}..."

# Create directory if it doesn't exist
mkdir -p "${CERT_DIR}"

# Generate CA private key
openssl genrsa -out "${CA_KEY_FILE}" 4096

# Generate CA certificate
openssl req -new -x509 -key "${CA_KEY_FILE}" -sha256 -subj "/C=US/ST=CA/O=SonicOPS/CN=SonicOPS-CA" -days 3650 -out "${CA_CERT_FILE}"

# Generate server private key
openssl genrsa -out "${KEY_FILE}" 4096

# Generate server certificate signing request
openssl req -new -key "${KEY_FILE}" -out server.csr -subj "/C=US/ST=CA/O=SonicOPS/CN=localhost" \
    -config <(echo '[req]'; echo 'distinguished_name=req'; echo '[v3_req]'; echo 'keyUsage=keyEncipherment,dataEncipherment'; echo 'extendedKeyUsage=serverAuth'; echo 'subjectAltName=@alt_names'; echo '[alt_names]'; echo 'DNS.1=localhost'; echo 'IP.1=127.0.0.1')

# Generate server certificate signed by CA
openssl x509 -req -in server.csr -CA "${CA_CERT_FILE}" -CAkey "${CA_KEY_FILE}" -CAcreateserial -out "${CERT_FILE}" -days 365 -sha256 \
    -extensions v3_req -extfile <(echo '[v3_req]'; echo 'keyUsage=keyEncipherment,dataEncipherment'; echo 'extendedKeyUsage=serverAuth'; echo 'subjectAltName=@alt_names'; echo '[alt_names]'; echo 'DNS.1=localhost'; echo 'IP.1=127.0.0.1')

# Clean up CSR
rm server.csr

echo "Test certificates generated successfully:"
echo "  CA Certificate: ${CA_CERT_FILE}"
echo "  Server Certificate: ${CERT_FILE}"
echo "  Server Private Key: ${KEY_FILE}"
echo ""
echo "Usage examples:"
echo "  # Run server with TLS:"
echo "  ./bin/sonic-ops-server -addr localhost:9999"
echo ""
echo "  # Test with grpcurl (using CA cert):"
echo "  grpcurl -cacert ${CA_CERT_FILE} localhost:9999 list"
echo ""
echo "  # Test with grpcurl (insecure, for self-signed):"
echo "  grpcurl -insecure localhost:9999 list"
echo ""
echo "  # Run server without TLS (for development):"
echo "  DISABLE_TLS=true ./bin/sonic-ops-server -addr localhost:9999"
echo "  grpcurl -plaintext localhost:9999 list"