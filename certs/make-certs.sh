#!/bin/bash
set -e

# Generate self-signed certificate for local HTTPS development
# Usage: ./make-certs.sh [LAN_IP]
# Example: ./make-certs.sh 192.168.1.173

LAN_IP=${1:-}

# Build Subject Alternative Names
SAN="DNS:localhost,IP:127.0.0.1"
if [ -n "$LAN_IP" ]; then
    SAN="$SAN,IP:$LAN_IP"
    CN="$LAN_IP"
else
    CN="localhost"
fi

echo "Generating certificate with SAN: $SAN"

openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
    -keyout dev.key \
    -out dev.crt \
    -subj "/CN=$CN" \
    -addext "subjectAltName=$SAN"

echo "Certificate generated successfully:"
echo "  - dev.crt (certificate)"
echo "  - dev.key (private key)"
echo ""
echo "Valid for: localhost, 127.0.0.1${LAN_IP:+, $LAN_IP}"
