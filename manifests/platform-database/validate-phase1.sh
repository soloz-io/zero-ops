#!/bin/bash
# Phase 1 Database Schema Extensions - Validation Script
# This script performs READ-ONLY assertions to verify database setup
# CRITICAL: This script NEVER mutates state - it only validates

set -e

echo "🔍 Phase 1: Database Schema Extensions - Validation"
echo "=================================================="

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

FAILED=0

# Function to run SQL query and check result
check_sql() {
    local db=$1
    local query=$2
    local expected=$3
    local description=$4
    
    echo -n "Checking: $description... "
    
    result=$(kubectl exec -n zero-ops-system platform-db-1 -- \
        psql -U postgres -d "$db" -tAc "$query" 2>/dev/null || echo "ERROR")
    
    if [ "$result" = "$expected" ]; then
        echo -e "${GREEN}✓${NC}"
        return 0
    else
        echo -e "${RED}✗${NC}"
        echo "  Expected: $expected"
        echo "  Got: $result"
        FAILED=$((FAILED + 1))
        return 1
    fi
}

echo ""
echo "📊 Task 1.1: Control Plane Shared DB Validation"
echo "-----------------------------------------------"

# 1.1.1 - Verify agentregistry schema exists
check_sql "control_plane" \
    "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = 'agentregistry';" \
    "1" \
    "agentregistry schema exists"

# 1.1.2 - Verify agent_definitions table exists
check_sql "control_plane" \
    "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'agentregistry' AND table_name = 'agent_definitions';" \
    "1" \
    "agent_definitions table exists"

# 1.1.2 - Verify deployments table exists
check_sql "control_plane" \
    "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'agentregistry' AND table_name = 'deployments';" \
    "1" \
    "deployments table exists"

# 1.1.3 - Verify RLS is enabled on agent_definitions
check_sql "control_plane" \
    "SELECT relrowsecurity FROM pg_class WHERE relname = 'agent_definitions' AND relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = 'agentregistry');" \
    "t" \
    "RLS enabled on agent_definitions"

# 1.1.3 - Verify RLS policy exists on agent_definitions
check_sql "control_plane" \
    "SELECT COUNT(*) FROM pg_policies WHERE schemaname = 'agentregistry' AND tablename = 'agent_definitions' AND policyname = 'tenant_isolation';" \
    "1" \
    "RLS policy 'tenant_isolation' exists on agent_definitions"

# 1.1.3 - Verify RLS is enabled on deployments
check_sql "control_plane" \
    "SELECT relrowsecurity FROM pg_class WHERE relname = 'deployments' AND relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = 'agentregistry');" \
    "t" \
    "RLS enabled on deployments"

# 1.1.3 - Verify RLS policy exists on deployments
check_sql "control_plane" \
    "SELECT COUNT(*) FROM pg_policies WHERE schemaname = 'agentregistry' AND tablename = 'deployments' AND policyname = 'tenant_isolation';" \
    "1" \
    "RLS policy 'tenant_isolation' exists on deployments"

# 1.1.5 - Verify agentregistry role exists
check_sql "postgres" \
    "SELECT COUNT(*) FROM pg_roles WHERE rolname = 'agentregistry';" \
    "1" \
    "agentregistry database role exists"

# 1.1.6 - Verify control-plane-db-credentials secret exists
echo -n "Checking: control-plane-db-credentials secret exists... "
if kubectl get secret control-plane-db-credentials -n zero-ops-system &>/dev/null; then
    echo -e "${GREEN}✓${NC}"
else
    echo -e "${RED}✗${NC}"
    FAILED=$((FAILED + 1))
fi

# 1.1.6 - Verify control-plane-db-credentials has required keys
echo -n "Checking: control-plane-db-credentials has 'url' key... "
if kubectl get secret control-plane-db-credentials -n zero-ops-system -o jsonpath='{.data.url}' | base64 -d | grep -q "control_plane"; then
    echo -e "${GREEN}✓${NC}"
else
    echo -e "${RED}✗${NC}"
    FAILED=$((FAILED + 1))
fi

echo ""
echo "📊 Task 1.2: Hub Centralised DB Validation"
echo "------------------------------------------"

# 1.2.1 - Verify agent_infra_status table exists
check_sql "hub" \
    "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'agent_infra_status';" \
    "1" \
    "agent_infra_status table exists"

# 1.2.2 - Verify pg_notify trigger exists (INSERT + UPDATE = 2 entries)
check_sql "hub" \
    "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_name = 'agent_infra_status_change';" \
    "2" \
    "pg_notify trigger 'agent_infra_status_change' exists (INSERT + UPDATE)"

# 1.2.2 - Verify pg_notify function exists
check_sql "hub" \
    "SELECT COUNT(*) FROM pg_proc WHERE proname = 'notify_agent_infra_status_change';" \
    "1" \
    "pg_notify function 'notify_agent_infra_status_change' exists"

# 1.2.3 - Verify RLS is enabled on agent_infra_status
check_sql "hub" \
    "SELECT relrowsecurity FROM pg_class WHERE relname = 'agent_infra_status';" \
    "t" \
    "RLS enabled on agent_infra_status"

# 1.2.3 - Verify RLS policies exist on agent_infra_status (2 policies: spoke + tenant)
check_sql "hub" \
    "SELECT COUNT(*) FROM pg_policies WHERE tablename = 'agent_infra_status';" \
    "2" \
    "RLS policies exist on agent_infra_status (spoke_cluster_isolation + tenant_isolation)"

# 1.2.5 - Verify spoke_controller role exists
check_sql "postgres" \
    "SELECT COUNT(*) FROM pg_roles WHERE rolname = 'spoke_controller';" \
    "1" \
    "spoke_controller database role exists"

# 1.2.6 - Verify hub-db-credentials secret exists
echo -n "Checking: hub-db-credentials secret exists... "
if kubectl get secret hub-db-credentials -n zero-ops-system &>/dev/null; then
    echo -e "${GREEN}✓${NC}"
else
    echo -e "${RED}✗${NC}"
    FAILED=$((FAILED + 1))
fi

# 1.2.6 - Verify hub-db-credentials has required keys
echo -n "Checking: hub-db-credentials has 'url' key... "
if kubectl get secret hub-db-credentials -n zero-ops-system -o jsonpath='{.data.url}' | base64 -d | grep -q "hub"; then
    echo -e "${GREEN}✓${NC}"
else
    echo -e "${RED}✗${NC}"
    FAILED=$((FAILED + 1))
fi

echo ""
echo "🔒 Security Enforcement Checklist"
echo "---------------------------------"

# Verify NO hardcoded passwords in CNPG postInitSQL
echo -n "Checking: No hardcoded passwords in platform-db.yaml... "
if ! grep -q "PASSWORD.*changeme\|PASSWORD.*'[^$]" manifests/platform-database/platform-db.yaml 2>/dev/null; then
    echo -e "${GREEN}✓${NC}"
else
    echo -e "${RED}✗${NC}"
    echo "  CRITICAL: Found hardcoded password in platform-db.yaml"
    FAILED=$((FAILED + 1))
fi

# Verify setup-platform-roles-job uses secretKeyRef
echo -n "Checking: setup-platform-roles-job uses secretKeyRef pattern... "
if grep -q "secretKeyRef" manifests/platform-database/setup-platform-roles-job.yaml; then
    echo -e "${GREEN}✓${NC}"
else
    echo -e "${RED}✗${NC}"
    echo "  CRITICAL: setup-platform-roles-job does not use secretKeyRef"
    FAILED=$((FAILED + 1))
fi

echo ""
echo "=================================================="
if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}✅ Phase 1 validation PASSED${NC}"
    echo "All database schemas, tables, RLS policies, and credentials are correctly configured."
    exit 0
else
    echo -e "${RED}❌ Phase 1 validation FAILED${NC}"
    echo "Failed checks: $FAILED"
    echo ""
    echo "Troubleshooting:"
    echo "1. Ensure ArgoCD has synced platform-database application (sync-wave 2)"
    echo "2. Run: kubectl get applications -n argocd | grep platform-database"
    echo "3. Check migration jobs: kubectl get jobs -n zero-ops-system | grep migrations"
    echo "4. Run: hub init-secrets (to generate secure credentials)"
    exit 1
fi
