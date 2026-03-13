#!/bin/bash
set -e

echo "Creating ory-system namespace..."
kubectl create namespace ory-system --dry-run=client -o yaml | kubectl apply -f -

echo "Generating database passwords..."
HYDRA_PASSWORD=$(openssl rand -base64 32)
KRATOS_PASSWORD=$(openssl rand -base64 32)
KETO_PASSWORD=$(openssl rand -base64 32)

echo "Creating identity-postgres-passwords secret in zero-ops-system..."
kubectl create secret generic identity-postgres-passwords \
  --namespace=zero-ops-system \
  --from-literal=hydra-password="$HYDRA_PASSWORD" \
  --from-literal=kratos-password="$KRATOS_PASSWORD" \
  --from-literal=keto-password="$KETO_PASSWORD" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "✓ Database passwords created successfully"
