#!/bin/bash
# Generate locally-trusted TLS certificates using mkcert
# Install mkcert: brew install mkcert (macOS) or see https://github.com/FiloSottile/mkcert

set -e

echo "Generating locally-trusted TLS certificates..."

# Check if mkcert is installed
if ! command -v mkcert &> /dev/null; then
    echo "❌ mkcert is not installed"
    echo "Install it with: brew install mkcert (macOS)"
    echo "Or see: https://github.com/FiloSottile/mkcert"
    exit 1
fi

# Install local CA
echo "Installing local CA..."
mkcert -install

# Create certs directory
mkdir -p certs

# Generate certificates
echo "Generating certificates..."
mkcert -cert-file certs/api.nutgraf.in.crt \
       -key-file certs/api.nutgraf.in.key \
       api.nutgraf.in

mkcert -cert-file certs/auth.nutgraf.in.crt \
       -key-file certs/auth.nutgraf.in.key \
       auth.nutgraf.in

mkcert -cert-file certs/console.nutgraf.in.crt \
       -key-file certs/console.nutgraf.in.key \
       console.nutgraf.in

echo "✅ Certificates generated in ./certs/"
echo ""
echo "Create Kubernetes secrets with:"
echo "  kubectl create secret tls api-zero-ops-tls --cert=certs/api.nutgraf.in.crt --key=certs/api.nutgraf.in.key -n api-gateway"
echo "  kubectl create secret tls auth-zero-ops-tls --cert=certs/auth.nutgraf.in.crt --key=certs/auth.nutgraf.in.key -n identity-services"
echo "  kubectl create secret tls console-zero-ops-tls --cert=certs/console.nutgraf.in.crt --key=certs/console.nutgraf.in.key -n ory-system"
