#!/bin/bash
# Validation script for CNPG disaster-recovery credential management
# This script performs READ-ONLY assertions to verify the implementation

set -e

echo "=== CNPG Disaster Recovery Credential Validation ==="
echo ""

# Check 1: Verify ExternalSecret exists and is synced
echo "1. Checking ExternalSecret for platform-db-app..."
kubectl get externalsecret platform-db-app-credentials -n hub-platform-data -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' | grep -q "True" && \
  echo "✓ ExternalSecret is synced from Infisical" || \
  echo "✗ ExternalSecret is NOT synced"

# Check 2: Verify platform-db-app secret exists with correct type
echo ""
echo "2. Checking platform-db-app secret..."
SECRET_TYPE=$(kubectl get secret platform-db-app -n hub-platform-data -o jsonpath='{.type}')
if [ "$SECRET_TYPE" = "kubernetes.io/basic-auth" ]; then
  echo "✓ Secret exists with correct type: $SECRET_TYPE"
else
  echo "✗ Secret type incorrect: $SECRET_TYPE (expected: kubernetes.io/basic-auth)"
fi

# Check 3: Verify CNPG Cluster uses the pre-created secret
echo ""
echo "3. Checking CNPG Cluster bootstrap configuration..."
BOOTSTRAP_SECRET=$(kubectl get cluster.postgresql.cnpg.io platform-db -n hub-platform-data -o jsonpath='{.spec.bootstrap.initdb.secret.name}')
if [ "$BOOTSTRAP_SECRET" = "platform-db-app" ]; then
  echo "✓ CNPG Cluster configured to use pre-created secret: $BOOTSTRAP_SECRET"
else
  echo "✗ CNPG Cluster NOT using pre-created secret (found: $BOOTSTRAP_SECRET)"
fi

# Check 4: Verify CNPG Cluster owner is 'app' (not 'postgres')
echo ""
echo "4. Checking CNPG Cluster owner..."
OWNER=$(kubectl get cluster.postgresql.cnpg.io platform-db -n hub-platform-data -o jsonpath='{.spec.bootstrap.initdb.owner}')
if [ "$OWNER" = "app" ]; then
  echo "✓ CNPG Cluster owner is 'app'"
else
  echo "✗ CNPG Cluster owner is NOT 'app' (found: $OWNER)"
fi

# Check 5: Verify password rotation job exists
echo ""
echo "5. Checking password rotation job..."
kubectl get job rotate-platform-db-password -n hub-platform-data &>/dev/null && \
  echo "✓ Password rotation job exists" || \
  echo "✗ Password rotation job NOT found"

# Check 6: Verify database role 'app' exists
echo ""
echo "6. Checking database role 'app'..."
kubectl exec -n hub-platform-data platform-db-1 -c postgres -- \
  psql -U postgres -tc "SELECT 1 FROM pg_roles WHERE rolname = 'app'" | grep -q 1 && \
  echo "✓ Database role 'app' exists" || \
  echo "✗ Database role 'app' NOT found"

# Check 7: Verify 'app' role can connect to databases
echo ""
echo "7. Testing 'app' role database access..."
APP_PASSWORD=$(kubectl get secret platform-db-app -n hub-platform-data -o jsonpath='{.data.password}' | base64 -d)

for DB in control_plane hub infisical; do
  PGPASSWORD="$APP_PASSWORD" kubectl exec -n hub-platform-data platform-db-1 -c postgres -- \
    psql -U app -d $DB -c "SELECT 1" &>/dev/null && \
    echo "✓ Role 'app' can connect to database: $DB" || \
    echo "✗ Role 'app' CANNOT connect to database: $DB"
done

# Check 8: Verify sync-wave ordering
echo ""
echo "8. Checking ArgoCD sync-wave ordering..."
PLACEHOLDER_WAVE=$(kubectl get secret platform-db-app -n hub-platform-data -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/sync-wave}')
ES_WAVE=$(kubectl get externalsecret platform-db-app-credentials -n hub-platform-data -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/sync-wave}')
CNPG_WAVE=$(kubectl get cluster platform-db -n hub-platform-data -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/sync-wave}')
ROTATION_WAVE=$(kubectl get job rotate-platform-db-password -n hub-platform-data -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/sync-wave}')

echo "  Placeholder secret: wave $PLACEHOLDER_WAVE"
echo "  ExternalSecret: wave $ES_WAVE"
echo "  CNPG Cluster: wave $CNPG_WAVE"
echo "  Password rotation: wave $ROTATION_WAVE"

if [ "$PLACEHOLDER_WAVE" = "0" ] && [ "$ES_WAVE" = "1" ] && [ "$CNPG_WAVE" = "2" ] && [ "$ROTATION_WAVE" = "5" ]; then
  echo "✓ Sync-wave ordering is correct"
else
  echo "✗ Sync-wave ordering is INCORRECT"
fi

echo ""
echo "=== Validation Complete ==="
echo ""
echo "Next Steps:"
echo "1. Add credentials to Infisical:"
echo "   - Key: platform-db-app-username, Value: app"
echo "   - Key: platform-db-app-password, Value: <secure-password>"
echo ""
echo "2. Test password rotation:"
echo "   - Update platform-db-app-password in Infisical"
echo "   - Wait for ExternalSecret refresh (1h) or force sync"
echo "   - Trigger ArgoCD sync to run rotation job"
echo "   - Verify new password works"
echo ""
echo "3. Test disaster recovery:"
echo "   - Delete and recreate cluster"
echo "   - Verify CNPG uses credentials from Infisical"
echo "   - Confirm database access with pre-created credentials"
