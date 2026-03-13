#!/bin/bash
# Demo 1 Complete Deployment Script
set -e

echo "========================================="
echo "Demo 1: OAuth2 PKCE Flow Deployment"
echo "========================================="
echo ""

# Check prerequisites
echo "Checking prerequisites..."
command -v kubectl >/dev/null 2>&1 || { echo "❌ kubectl not found"; exit 1; }
command -v kustomize >/dev/null 2>&1 || { echo "⚠️  kustomize not found, using kubectl kustomize"; }

echo "✓ Prerequisites met"
echo ""

# Step 1: Bootstrap secrets
echo "Step 1: Creating namespaces and secrets..."
./manifests/platform-identity/bootstrap-secrets.sh
echo ""

# Step 2: Verify CNPG cluster exists
echo "Step 2: Verifying zero-ops-platform-db cluster..."
kubectl wait --for=condition=Ready clusters.postgresql.cnpg.io/zero-ops-platform-db -n zero-ops-system --timeout=60s
echo "✓ CNPG cluster ready"
echo ""

# Step 3: Create database roles
echo "Step 3: Creating database roles..."
kubectl apply -f manifests/platform-identity/databases/setup-roles-job.yaml
kubectl wait --for=condition=complete job/setup-identity-roles -n zero-ops-system --timeout=60s
echo "✓ Database roles created"
echo ""

# Step 4: Create databases
echo "Step 4: Creating databases..."
kubectl apply -f manifests/platform-identity/databases/hydra-db.yaml
kubectl apply -f manifests/platform-identity/databases/kratos-db.yaml
kubectl apply -f manifests/platform-identity/databases/keto-db.yaml
echo "Waiting for databases to be ready..."
sleep 10
echo ""

# Step 5: Deploy Kratos identity schema
echo "Step 5: Deploying Kratos identity schema..."
kubectl apply -f manifests/platform-identity/ory-kratos/identity-schema-configmap.yaml
echo ""

# Step 6: Deploy Ory services via Helm
echo "Step 6: Deploying Ory services..."
echo "Installing Hydra..."
helm upgrade --install hydra ory/hydra -n ory-system -f manifests/platform-identity/ory-hydra/values.yaml --wait
echo "Installing Kratos..."
helm upgrade --install kratos ory/kratos -n ory-system -f manifests/platform-identity/ory-kratos/values.yaml --wait
echo "Installing Keto..."
helm upgrade --install keto ory/keto -n ory-system -f manifests/platform-identity/ory-keto/values.yaml --wait
echo "✓ Ory services deployed"
echo ""

# Step 7: Deploy auth-proxy
echo "Step 7: Deploying auth-proxy..."
kubectl apply -k manifests/platform-identity/auth-proxy/
echo ""

# Step 8: Deploy AgentGateway
echo "Step 8: Deploying AgentGateway..."
kubectl apply -k manifests/api-gateway/
echo ""

# Step 9: Deploy Ingress
echo "Step 9: Deploying Ingress..."
kubectl apply -k manifests/ingress/
echo ""

# Step 10: Apply NetworkPolicies
echo "Step 10: Applying NetworkPolicies..."
kubectl apply -f manifests/platform-identity/network-policies.yaml
echo ""

echo "========================================="
echo "✅ Deployment Complete!"
echo "========================================="
echo ""
echo "Next steps:"
echo "1. Wait for all pods to be ready:"
echo "   kubectl get pods -n ory-system"
echo "   kubectl get pods -n identity-services"
echo "   kubectl get pods -n api-gateway"
echo ""
echo "2. For local development, setup DNS and TLS:"
echo "   cd manifests/ingress"
echo "   ./generate-local-certs.sh"
echo "   ./setup-local-dns.sh 127.0.0.1"
echo ""
echo "3. Verify endpoints:"
echo "   curl https://api.zero-ops.io/health"
echo "   curl https://auth.zero-ops.io/health/ready"
echo "   curl https://console.zero-ops.io"
echo ""
echo "4. Seed demo user (see manifests/platform-identity/README.md)"
