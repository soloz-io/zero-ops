#!/bin/bash
set -e

echo "Creating ory-system namespace..."
kubectl create namespace ory-system --dry-run=client -o yaml | kubectl apply -f -

echo "Generating database passwords..."
HYDRA_PASSWORD=$(openssl rand -base64 32)
KRATOS_PASSWORD=$(openssl rand -base64 32)
KETO_PASSWORD=$(openssl rand -base64 32)

echo "Creating identity-postgres-passwords secret..."
kubectl create secret generic identity-postgres-passwords \
  --namespace=ory-system \
  --from-literal=hydra-password="$HYDRA_PASSWORD" \
  --from-literal=kratos-password="$KRATOS_PASSWORD" \
  --from-literal=keto-password="$KETO_PASSWORD" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "✓ Database passwords created successfully"
echo ""
echo "Next steps:"
echo "1. Apply CNPG cluster: kubectl apply -f manifests/platform-identity/cnpg-cluster.yaml"
echo "2. Apply database CRDs: kubectl apply -f manifests/platform-identity/databases/"
echo "3. Apply Kratos identity schema ConfigMap: kubectl apply -f manifests/platform-identity/ory-kratos/identity-schema-configmap.yaml"
echo "4. Deploy ArgoCD applications: kubectl apply -f manifests/platform-identity/argocd/"
