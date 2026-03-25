# CNPG Database + Migration Pattern

**Version:** 1.0  
**Date:** 2026-03-25  
**Pattern:** Single CNPG Cluster + Kubernetes Job Migrations

## Core Principle

**CNPG manages infrastructure. Kubernetes Jobs manage schema lifecycle.**

```
CNPG Cluster (infra)
  ├── Database creation (postInitSQL)
  ├── User/role creation (postInitSQL)
  └── One-time bootstrap
           ↓
Migration Jobs (schema lifecycle)
  ├── Versioned migrations
  ├── Per-database targeting
  └── Retryable + observable
           ↓
Application (runtime)
  └── Connects to ready databases
```

## The Three Layers

### 1. CNPG Cluster: Infrastructure Provisioning

**Single cluster with multiple logical databases:**

```yaml
# manifests/platform-database/platform-db.yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: platform-db
  namespace: zero-ops-system
spec:
  instances: 3
  storage:
    size: 20Gi
    storageClass: hcloud-volumes
  
  bootstrap:
    initdb:
      database: postgres
      owner: postgres
      postInitSQL:
        # Create databases
        - CREATE DATABASE control_plane;
        - CREATE DATABASE hub;
        
        # Create users
        - CREATE USER agentregistry WITH PASSWORD 'changeme';
        - CREATE USER mcp_server WITH PASSWORD 'changeme';
        - CREATE USER spoke_controller WITH PASSWORD 'changeme';
        
        # Grant database access
        - GRANT ALL PRIVILEGES ON DATABASE control_plane TO agentregistry;
        - GRANT ALL PRIVILEGES ON DATABASE control_plane TO mcp_server;
        - GRANT ALL PRIVILEGES ON DATABASE hub TO spoke_controller;
```

**What belongs in postInitSQL:**
- ✅ CREATE DATABASE
- ✅ CREATE USER / CREATE ROLE
- ✅ GRANT database-level privileges
- ✅ GRANT schema-level privileges (USAGE, CREATE on schemas)
- ❌ Schema creation (use migrations)
- ❌ Table creation (use migrations)
- ❌ Versioned changes (use migrations)

**CRITICAL - Schema Grants Required:**
CNPG does NOT auto-grant schema permissions. Database access ≠ schema access in PostgreSQL.

After creating users, you MUST grant schema-level privileges:
```sql
-- Grant schema access
GRANT USAGE ON SCHEMA public TO agentregistry;
GRANT CREATE ON SCHEMA public TO agentregistry;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO agentregistry;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO agentregistry;
```

**Why single cluster:**
- Shared resource pool (CPU, memory, connections)
- Simplified backup/restore
- Single monitoring endpoint
- Lower operational overhead

### 2. Migration Jobs: Schema Lifecycle

**Kubernetes Jobs run after cluster is ready:**

```yaml
# manifests/platform-database/migrations/control-plane-migrations.yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: control-plane-migrations
  namespace: zero-ops-system
spec:
  template:
    spec:
      restartPolicy: OnFailure
      containers:
      - name: migrate
        image: migrate/migrate:latest
        command:
          - migrate
          - -path=/migrations
          - -database=postgresql://agentregistry:changeme@platform-db-rw:5432/control_plane?sslmode=require
          - up
        volumeMounts:
        - name: migrations
          mountPath: /migrations
      volumes:
      - name: migrations
        configMap:
          name: control-plane-migrations
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: control-plane-migrations
  namespace: zero-ops-system
data:
  001_agentregistry_schema.sql: |
    CREATE SCHEMA IF NOT EXISTS agentregistry;
    
    CREATE TABLE IF NOT EXISTS agentregistry.agent_definitions (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
        tenant_id UUID NOT NULL,
        name TEXT NOT NULL,
        version TEXT NOT NULL,
        agent_type TEXT NOT NULL,
        system_message TEXT NOT NULL,
        tool_access JSONB NOT NULL,
        memory_config JSONB NOT NULL,
        guardrail_policies JSONB NOT NULL,
        model_config JSONB NOT NULL,
        created_at TIMESTAMPTZ DEFAULT NOW(),
        updated_at TIMESTAMPTZ DEFAULT NOW(),
        UNIQUE(tenant_id, name, version)
    );
    
    ALTER TABLE agentregistry.agent_definitions ENABLE ROW LEVEL SECURITY;
    
    CREATE POLICY tenant_isolation ON agentregistry.agent_definitions
        USING (tenant_id = current_setting('app.tenant_id')::UUID);
  
  002_agents_schema.sql: |
    CREATE SCHEMA IF NOT EXISTS agents;
    
    CREATE TABLE IF NOT EXISTS agents.authorized_tools (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
        tenant_id UUID NOT NULL,
        tool_name VARCHAR(255) NOT NULL,
        category VARCHAR(100),
        enabled BOOLEAN DEFAULT true,
        created_at TIMESTAMPTZ DEFAULT NOW(),
        UNIQUE(tenant_id, tool_name)
    );
```

**Migration tool options:**
- **golang-migrate/migrate**: Simple, container-ready, version tracking
- **Flyway**: Java-based, enterprise features
- **Liquibase**: XML/YAML definitions
- **goose**: Go-native, embedded or CLI

**Why Jobs over postInitSQL:**
- ✅ Versioned migrations (001, 002, 003...)
- ✅ Rollback support
- ✅ Per-database targeting
- ✅ Retryable on failure
- ✅ Observable via kubectl logs
- ✅ Can run after cluster is ready
- ✅ Decoupled from infrastructure

### 3. Application: Runtime Usage

**Applications connect to ready databases:**

```go
// cmd/zero-ops-api/main.go
func main() {
    dbURL := os.Getenv("DATABASE_URL")
    // postgresql://agentregistry:changeme@platform-db-rw:5432/control_plane
    
    pool, err := pgxpool.New(context.Background(), dbURL)
    if err != nil {
        log.Fatal(err)
    }
    
    // Application logic
}
```

**Connection patterns:**
- Use `-rw` service for read-write (platform-db-rw)
- Use `-ro` service for read-only (platform-db-ro)
- Use `-r` service for any replica (platform-db-r)

## Complete Flow

### Step 1: Deploy CNPG Cluster

```bash
kubectl apply -f manifests/platform-database/platform-db.yaml
```

**What happens:**
1. CNPG operator creates 3 PostgreSQL instances
2. postInitSQL runs once on primary
3. Databases created: control_plane, hub
4. Users created: agentregistry, mcp_server, spoke_controller
5. Cluster becomes Ready

### Step 2: Run Migration Jobs

```bash
kubectl apply -f manifests/platform-database/migrations/
```

**What happens:**
1. Job pods start after cluster is Ready
2. Migration tool connects to specific database
3. Runs migrations in order (001, 002, 003...)
4. Tracks version in schema_migrations table
5. Job completes successfully

### Step 3: Deploy Applications

```bash
kubectl apply -f manifests/zero-ops-api/
```

**What happens:**
1. Application pods start
2. Connect to platform-db-rw:5432/control_plane
3. Query tables created by migrations
4. Application becomes Ready

## Directory Structure

```
manifests/
  platform-database/
    platform-db.yaml              ← CNPG Cluster (infra)
    migrations/
      control-plane-migrations.yaml   ← Job + ConfigMap
      hub-migrations.yaml             ← Job + ConfigMap

internal/
  agent-core/
    database/
      migrations/
        001_agentregistry_schema.sql  ← Source migrations
        002_agents_schema.sql
        003_hub_agent_infra_status.sql
```

**Build process:**
1. Developer writes SQL in `internal/*/database/migrations/`
2. CI packages migrations into ConfigMaps
3. GitOps deploys Jobs with ConfigMaps
4. Jobs run migrations in cluster

## Migration Job Best Practices

### Use Init Containers for Readiness

```yaml
spec:
  template:
    spec:
      initContainers:
      - name: wait-for-db
        image: postgres:16
        command:
          - sh
          - -c
          - |
            until pg_isready -h platform-db-rw -p 5432; do
              echo "Waiting for database..."
              sleep 2
            done
      containers:
      - name: migrate
        # ... migration container
```

### Use Secrets for Credentials

```yaml
env:
- name: DB_PASSWORD
  valueFrom:
    secretKeyRef:
      name: platform-db-app
      key: password
command:
  - migrate
  - -database=postgresql://agentregistry:$(DB_PASSWORD)@platform-db-rw:5432/control_plane
  - up
```

### Track Migration Status

```sql
-- Migration tool creates this automatically
CREATE TABLE schema_migrations (
    version BIGINT PRIMARY KEY,
    dirty BOOLEAN NOT NULL
);
```

### Handle Failures Gracefully

```yaml
spec:
  backoffLimit: 3  # Retry up to 3 times
  template:
    spec:
      restartPolicy: OnFailure
```

## Common Patterns

### Pattern 1: Multiple Databases, Single Cluster

```yaml
# One cluster
platform-db:
  - control_plane database
    - agentregistry schema
    - agents schema
  - hub database
    - public schema (agent_infra_status)
```

**Migration Jobs:**
- `control-plane-migrations.yaml` → connects to control_plane
- `hub-migrations.yaml` → connects to hub

### Pattern 2: Shared Tables Across Schemas

```sql
-- In control_plane database
CREATE SCHEMA IF NOT EXISTS public;
CREATE TABLE public.tenants (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL
);

-- Other schemas reference it
CREATE TABLE agents.authorized_tools (
    tenant_id UUID REFERENCES public.tenants(id)
);
```

### Pattern 3: Cross-Database References (Avoid)

```sql
-- ❌ Don't do this
CREATE TABLE agents.tools (
    status_id UUID REFERENCES hub.agent_infra_status(id)
);
```

**Why:** PostgreSQL doesn't support cross-database foreign keys.

**Solution:** Use application-level joins or denormalize data.

## Troubleshooting

### Migration Job Fails

```bash
# Check job status
kubectl get jobs -n zero-ops-system

# View logs
kubectl logs -n zero-ops-system job/control-plane-migrations

# Common issues:
# - Database not ready → add initContainer
# - Wrong credentials → check secret
# - SQL syntax error → test migration locally
```

### Database Not Created

```bash
# Check cluster status
kubectl get cluster -n zero-ops-system platform-db

# View cluster logs
kubectl logs -n zero-ops-system platform-db-1

# Check postInitSQL execution
kubectl exec -n zero-ops-system platform-db-1 -- psql -U postgres -c '\l'
```

### Connection Refused

```bash
# Check service endpoints
kubectl get svc -n zero-ops-system | grep platform-db

# Test connection from pod
kubectl run -it --rm debug --image=postgres:16 --restart=Never -- \
  psql postgresql://agentregistry:changeme@platform-db-rw:5432/control_plane
```

## Summary

**The Pattern:**
1. **CNPG Cluster** creates infrastructure (databases, users, roles)
2. **Migration Jobs** create schemas and tables (versioned, retryable)
3. **Applications** connect to ready databases

**Why It Works:**
- **Separation of concerns**: Infra vs schema lifecycle
- **Idiomatic Kubernetes**: Jobs for one-time tasks
- **Versioned migrations**: Rollback support, audit trail
- **Observable**: kubectl logs shows migration status
- **Retryable**: Jobs handle transient failures

**Implementation Checklist:**
- [ ] Create CNPG Cluster with postInitSQL (databases + users)
- [ ] Package migrations into ConfigMaps
- [ ] Create Job manifests per database
- [ ] Add initContainers for readiness checks
- [ ] Use Secrets for credentials
- [ ] Test migrations locally before deploying
- [ ] Monitor Job completion in cluster

This is the idiomatic way to manage PostgreSQL databases in Kubernetes with CNPG.
