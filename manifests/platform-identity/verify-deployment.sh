#!/bin/bash
set -e

echo "=== Platform Identity Infrastructure Verification ==="
echo ""

# Check namespaces
echo "1. Checking namespaces..."
kubectl get namespace ory-system 2>/dev/null && echo "  ✓ ory-system namespace exists" || echo "  ✗ ory-system namespace missing"
kubectl get namespace api-gateway 2>/dev/null && echo "  ✓ api-gateway namespace exists" || echo "  ✗ api-gateway namespace missing"
echo ""

# Check secrets
echo "2. Checking secrets..."
kubectl get secret identity-postgres-passwords -n ory-system 2>/dev/null && echo "  ✓ Database passwords secret exists" || echo "  ✗ Database passwords secret missing"
echo ""

# Check CNPG cluster
echo "3. Checking CNPG cluster..."
if kubectl get cluster identity-postgres -n ory-system 2>/dev/null; then
  CLUSTER_STATUS=$(kubectl get cluster identity-postgres -n ory-system -o jsonpath='{.status.phase}')
  echo "  Cluster status: $CLUSTER_STATUS"
  if [ "$CLUSTER_STATUS" = "Cluster in healthy state" ]; then
    echo "  ✓ CNPG cluster is healthy"
  else
    echo "  ⚠ CNPG cluster is not yet healthy"
  fi
else
  echo "  ✗ CNPG cluster not found"
fi
echo ""

# Check databases
echo "4. Checking database CRDs..."
kubectl get database hydra-db -n ory-system 2>/dev/null && echo "  ✓ hydra-db exists" || echo "  ✗ hydra-db missing"
kubectl get database kratos-db -n ory-system 2>/dev/null && echo "  ✓ kratos-db exists" || echo "  ✗ kratos-db missing"
kubectl get database keto-db -n ory-system 2>/dev/null && echo "  ✓ keto-db exists" || echo "  ✗ keto-db missing"
echo ""

# Check ConfigMaps
echo "5. Checking ConfigMaps..."
kubectl get configmap kratos-identity-schema -n ory-system 2>/dev/null && echo "  ✓ Kratos identity schema ConfigMap exists" || echo "  ✗ Kratos identity schema ConfigMap missing"
echo ""

# Check Ory deployments
echo "6. Checking Ory deployments..."
kubectl get deployment ory-hydra -n ory-system 2>/dev/null && echo "  ✓ Hydra deployment exists" || echo "  ⚠ Hydra deployment not found (may not be deployed yet)"
kubectl get deployment ory-kratos -n ory-system 2>/dev/null && echo "  ✓ Kratos deployment exists" || echo "  ⚠ Kratos deployment not found (may not be deployed yet)"
kubectl get deployment ory-keto -n ory-system 2>/dev/null && echo "  ✓ Keto deployment exists" || echo "  ⚠ Keto deployment not found (may not be deployed yet)"
kubectl get deployment kratos-selfservice-ui -n ory-system 2>/dev/null && echo "  ✓ Kratos UI deployment exists" || echo "  ⚠ Kratos UI deployment not found (may not be deployed yet)"
echo ""

# Check demo echo
echo "7. Checking demo echo service..."
kubectl get deployment demo-echo -n api-gateway 2>/dev/null && echo "  ✓ Demo echo deployment exists" || echo "  ⚠ Demo echo deployment not found (may not be deployed yet)"
echo ""

# Check pod status
echo "8. Checking pod status..."
echo "  ory-system pods:"
kubectl get pods -n ory-system 2>/dev/null | grep -v NAME || echo "    No pods found"
echo ""
echo "  api-gateway pods:"
kubectl get pods -n api-gateway 2>/dev/null | grep -v NAME || echo "    No pods found"
echo ""

# Check ArgoCD applications
echo "9. Checking ArgoCD applications..."
kubectl get application -n argocd 2>/dev/null | grep -E "(identity-postgres|ory-hydra|ory-kratos|ory-keto|kratos-selfservice-ui|demo-echo)" || echo "  ⚠ No ArgoCD applications found"
echo ""

echo "=== Verification Complete ==="
