#!/bin/bash
set -e

CERT_DIR="$(cd "$(dirname "\$0")" && pwd)"
cd "$CERT_DIR"

echo "=== Generating Development CA ==="

# Generate CA private key
openssl genrsa -out ca-key.pem 4096

# Generate CA certificate
openssl req -new -x509 -days 3650 -key ca-key.pem -out ca.pem \
    -subj "/C=US/ST=Dev/L=Local/O=CloudCtrl Dev/CN=CloudCtrl Dev CA"

echo "=== Generating Server Certificate ==="

# Generate server private key
openssl genrsa -out server-key.pem 2048

# Generate server CSR
openssl req -new -key server-key.pem -out server.csr \
    -subj "/C=US/ST=Dev/L=Local/O=CloudCtrl Dev/CN=localhost"

# Create extension file for SAN
cat > server-ext.cnf << EOF
[v3_req]
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names

[alt_names]
DNS.1 = localhost
DNS.2 = controller.local
DNS.3 = *.local
IP.1 = 127.0.0.1
IP.2 = 0.0.0.0
IP.3 = 192.168.1.1
EOF

# Sign server certificate with CA
openssl x509 -req -days 365 -in server.csr \
    -CA ca.pem -CAkey ca-key.pem -CAcreateserial \
    -out server.pem \
    -extfile server-ext.cnf -extensions v3_req

# Clean up CSR
rm server.csr server-ext.cnf

echo "=== Generating Device Test Certificate ==="

# Generate a test device key (for testing mTLS if needed later)
openssl genrsa -out device-test-key.pem 2048
openssl req -new -key device-test-key.pem -out device-test.csr \
    -subj "/C=US/ST=Dev/L=Local/O=CloudCtrl Dev/CN=test-device-001"
openssl x509 -req -days 365 -in device-test.csr \
    -CA ca.pem -CAkey ca-key.pem -CAcreateserial \
    -out device-test.pem
rm device-test.csr

echo "=== Verifying Certificates ==="
echo ""
echo "CA Certificate:"
openssl x509 -in ca.pem -noout -subject -issuer -dates
echo ""
echo "Server Certificate:"
openssl x509 -in server.pem -noout -subject -issuer -dates
echo ""
echo "Certificate chain verification:"
openssl verify -CAfile ca.pem server.pem

echo ""
echo "=== Certificate files generated ==="
echo "  CA cert:          $CERT_DIR/ca.pem"
echo "  CA key:           $CERT_DIR/ca-key.pem"
echo "  Server cert:      $CERT_DIR/server.pem"
echo "  Server key:       $CERT_DIR/server-key.pem"
echo "  Device test cert: $CERT_DIR/device-test.pem"
echo "  Device test key:  $CERT_DIR/device-test-key.pem"
