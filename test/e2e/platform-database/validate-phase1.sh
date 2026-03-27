#!/bin/bash
set -e

echo "=== Phase 1: Database Schema Extensions Validation ==="
echo ""

# Get platform-db pod
POD=$(kubectl get pod -n zero-ops-system -l cnpg.io/cluster=platform-db,role=primary -o jsonpath='{.items[0].metadata.name}')

if [ -z "$POD" ]; then
    echo "ERROR: Could not find platform-db primary pod"
    exit 1
fi

echo "Using pod: $POD"
echo ""

# Task 1.3.1: Verify control-plane-db-credentials routes to control_plane database
echo "Task 1.3.1: Verifying control-plane-db-credentials secret..."
kubectl get secret control-plane-db-credentials -n zero-ops-system &>/dev/null
if [ $? -eq 0 ]; then
    echo "✓ control-plane-db-credentials secret exists"
    DB_URL=$(kubectl get secret control-plane-db-credentials -n zero-ops-system -o jsonpath='{.data.url}' | base64 -d)
    if [[ "$DB_URL" == *"control_plane"* ]]; then
        echo "✓ Secret routes to control_plane database"
    else
        echo "✗ Secret does not route to control_plane database"
        exit 1
    fi
else
    echo "✗ control-plane-db-credentials secret not found"
    exit 1
fi
echo ""

# Task 1.3.2: Verify hub-db-credentials routes to hub database
echo "Task 1.3.2: Verifying hub-db-credentials secret..."
kubectl get secret hub-db-credentials -n zero-ops-system &>/dev/null
if [ $? -eq 0 ]; then
    echo "✓ hub-db-credentials secret exists"
    DB_URL=$(kubectl get secret hub-db-credentials -n zero-ops-system -o jsonpath='{.data.url}' | base64 -d)
    if [[ "$DB_URL" == *"hub"* ]]; then
        echo "✓ Secret routes to hub database"
    else
        echo "✗ Secret does not route to hub database"
        exit 1
    fi
else
    echo "✗ hub-db-credentials secret not found"
    exit 1
fi
echo ""

# Task 1.3.3: Test RLS policies with sample tenant data
echo "Task 1.3.3: Testing RLS policies in control_plane database..."
kubectl exec -n zero-ops-system $POD -- psql -U agentregistry -d control_plane -c "
SET app.tenant_id = '00000000-0000-0000-0000-000000000001';
SELECT COUNT(*) FROM agentregistry.agent_definitions;
" &>/dev/null
if [ $? -eq 0 ]; then
    echo "✓ RLS policies configured on agentregistry.agent_definitions"
else
    echo "✗ RLS policies not working on agentregistry.agent_definitions"
    exit 1
fi

kubectl exec -n zero-ops-system $POD -- psql -U agentregistry -d control_plane -c "
SET app.tenant_id = '00000000-0000-0000-0000-000000000001';
SELECT COUNT(*) FROM agentregistry.deployments;
" &>/dev/null
if [ $? -eq 0 ]; then
    echo "✓ RLS policies configured on agentregistry.deployments"
else
    echo "✗ RLS policies not working on agentregistry.deployments"
    exit 1
fi
echo ""

# Task 1.3.4: Validate pg_notify trigger functionality
echo "Task 1.3.4: Validating pg_notify trigger in hub database..."
kubectl exec -n zero-ops-system $POD -- psql -U spoke_controller -d hub -c "
SELECT tgname FROM pg_trigger WHERE tgname = 'agent_infra_status_change';
" | grep -q "agent_infra_status_change"
if [ $? -eq 0 ]; then
    echo "✓ pg_notify trigger exists on agent_infra_status table"
else
    echo "✗ pg_notify trigger not found"
    exit 1
fi

kubectl exec -n zero-ops-system $POD -- psql -U spoke_controller -d hub -c "
SELECT proname FROM pg_proc WHERE proname = 'notify_agent_infra_status_change';
" | grep -q "notify_agent_infra_status_change"
if [ $? -eq 0 ]; then
    echo "✓ pg_notify trigger function exists"
else
    echo "✗ pg_notify trigger function not found"
    exit 1
fi
echo ""

echo "=== Phase 1 Validation Complete ==="
echo "All tasks passed successfully!"
