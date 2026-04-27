# ADR: Multi-Tenant Database Pattern

**Date:** 2026-04-27  
**Status:** Accepted  
**Authors:** Platform Engineering Team  
**Related ADRs:** 
- [CNPG Database Migration Pattern](./cnpg-database-migration-pattern.md)
- [ESO-Infisical Pattern](./eso-infisical-pattern.md)
- [Declarative Operator State](./declarative-operator-state-over-imperative-jobs.md)
- [ADR 005: Hub-Spoke Crossplane Composition](./0005-unified-abstraction-layers-crossplane.md)

## Context

The platform provisions multi-tenant SaaS environments where each tenant requires isolated database credentials. Unlike static platform databases (control_plane, hub) with a fixed set of users, tenant databases have dynamic user lifecycles:

- **Tenants are created continuously** via API/MCP requests
- **Each tenant needs unique credentials** (username, password, database)
- **Passwords must be externalized** (Infisical is source of truth per ESO-Infisical ADR)
- **Users must be self-healing** (recreated if manually deleted)

The existing CNPG Migration Pattern ADR documents user creation via `postInitSQL`, which is suitable for static platform users but NOT for dynamic tenant users.

### The Problem with postInitSQL for Tenants

```yaml
# ❌ Anti-Pattern: Cannot scale to dynamic tenants
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
spec:
  bootstrap:
    initdb:
      postInitSQL:
        - CREATE USER tenant_app_creator_user WITH PASSWORD 'hardcoded';
        - CREATE USER tenant_xyz_user WITH PASSWORD 'hardcoded';
        # ... 1000+ tenants? Not scalable!
```

**Why This Fails:**
1. **Not Dynamic**: postInitSQL runs once at cluster bootstrap, cannot create users for tenants added later
2. **Not Externalized**: Passwords hardcoded in YAML, violates Infisical-as-source-of-truth principle
3. **Not Self-Healing**: If user deleted manually, postInitSQL won't recreate it
4. **Not Declarative**: Requires cluster recreation to add new users

## Decision

For multi-tenant databases, we adopt the **Externalized Identity Pattern**:

```
Infisical (source of truth)
  ↓
ESO (creates K8s secret with credentials)
  ↓
Crossplane provider-sql (creates PostgreSQL user declaratively)
  ↓
Application (consumes credentials)
```

### Architecture Layers

#### Layer 1: Infisical (Source of Truth)

Tenant credentials are stored in Infisical with dynamic paths:

**Path Structure:**
```
/spoke-pool/<cell-id>/tenants/<tenant-id>/db-credentials
```

**Properties:**
```json
{
  "username": "tenant_app-creator_user",
  "password": "8b729a04-95ac-4dfe-92ea-ad0ccebc"
}
```

**Creation Method:**
- Kube-SBT/open-sbt Application Plane generates password during tenant onboarding
- Uploads to Infisical via API (Go SDK)
- Follows Pattern A2a from ESO-Infisical ADR (Kube-SBT → Infisical Only → ESO)

#### Layer 2: ESO (Secret Delivery)

ExternalSecret pulls credentials from Infisical and creates K8s secret:

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: tenant-app-creator-db-credentials-restore
  namespace: tenant-app-creator
spec:
  secretStoreRef:
    name: infisical-backend
    kind: ClusterSecretStore
  target:
    name: tenant-app-creator-db-credentials
    creationPolicy: Owner  # ESO creates and owns the secret
    template:
      type: Opaque
      data:
        username: "{{ .username }}"
        password: "{{ .password }}"
        database: "tenant-app-creator-db"  # Computed (patched from spec.databaseName)
        host: "shared-cnpg-rw.spoke-platform-data.svc.cluster.local"  # Computed
        port: "5432"  # Computed
  data:
    - secretKey: username
      remoteRef:
        key: /spoke-pool/spoke-pool-eu-prod-01/tenants/app-creator/db-credentials
        property: username
    - secretKey: password
      remoteRef:
        key: /spoke-pool/spoke-pool-eu-prod-01/tenants/app-creator/db-credentials
        property: password
```

**Key Points:**
- `creationPolicy: Owner` - ESO creates the secret from scratch
- **Only secrets (username, password) pulled from Infisical**
- **Configuration fields (database, host, port) computed dynamically in template**
- Database name patched via Crossplane from `spec.databaseName`
- Dynamic path construction via Crossplane patches (cellId + tenantId)
- Follows Pattern B from ESO-Infisical ADR (Application Secrets)

#### Layer 3: Crossplane provider-sql (User Creation)

provider-sql Role creates PostgreSQL user declaratively:

```yaml
apiVersion: postgresql.sql.crossplane.io/v1alpha1
kind: Role
metadata:
  name: tenant_app-creator_user
spec:
  forProvider:
    privileges:
      login: true
      createDb: false
      superUser: false
    passwordSecretRef:
      namespace: tenant-app-creator
      name: tenant-app-creator-db-credentials
      key: password
  providerConfigRef:
    name: default
  deletionPolicy: Delete
```

**Key Points:**
- References ESO-created secret (not hardcoded password)
- Continuously reconciles (self-healing if user deleted)
- Declarative (follows ADR 004)
- Managed by Crossplane composition (TenantDatabase XR)

#### Layer 4: Application (Consumption)

Applications consume credentials from ESO-created secrets:

```yaml
# AtlasMigration uses pooler-app secret
apiVersion: db.atlasgo.io/v1alpha1
kind: AtlasMigration
spec:
  urlFrom:
    secretKeyRef:
      name: app-creator-pooler-app  # Created by ESO with template
      key: url
```

**Pooler App Secret (ESO Template):**
```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: app-creator-pooler-app-externalsecret
spec:
  target:
    name: app-creator-pooler-app
    creationPolicy: Owner
    template:
      data:
        username: "{{ .username }}"
        password: "{{ .password }}"
        database: "tenant-app-creator-db"  # Computed
        host: "shared-cnpg-rw.spoke-platform-data.svc.cluster.local"  # Computed
        port: "5432"  # Computed
        pooler-host: "app-creator-pooler.spoke-platform-data.svc"  # Computed
        url: "postgresql://{{ .username }}:{{ .password }}@app-creator-pooler.spoke-platform-data.svc:5432/tenant-app-creator-db"
  data:
    - secretKey: username
      remoteRef:
        key: /spoke-pool/spoke-pool-eu-prod-01/tenants/app-creator/db-credentials
        property: username
    - secretKey: password
      remoteRef:
        key: /spoke-pool/spoke-pool-eu-prod-01/tenants/app-creator/db-credentials
        property: password
```

**Key Points:**
- Only secrets (username, password) from Infisical
- Configuration (host, port, database, pooler-host) computed dynamically
- URL constructed via ESO template (no hardcoding)

## Implementation: Crossplane Composition

### TenantDatabase XR Composition

```yaml
apiVersion: apiextensions.crossplane.io/v1
kind: Composition
metadata:
  name: tenantdatabase-spoke
spec:
  compositeTypeRef:
    apiVersion: nutgraf.in/v1alpha1
    kind: TenantDatabase
  
  mode: Pipeline
  pipeline:
    - step: patch-and-transform
      functionRef:
        name: function-patch-and-transform
      input:
        apiVersion: pt.fn.crossplane.io/v1beta1
        kind: Resources
        resources:
          # Resource 1: ESO ExternalSecret (restore credentials from Infisical)
          - name: db-credentials-restore
            base:
              apiVersion: kubernetes.crossplane.io/v1alpha2
              kind: Object
              spec:
                forProvider:
                  manifest:
                    apiVersion: external-secrets.io/v1
                    kind: ExternalSecret
                    spec:
                      secretStoreRef:
                        name: infisical-backend
                        kind: ClusterSecretStore
                      target:
                        name: tenant-<id>-db-credentials
                        creationPolicy: Owner
                        template:
                          type: Opaque
                          data:
                            username: "{{ .username }}"
                            password: "{{ .password }}"
                            database: ""  # Patched from spec.databaseName
                            host: "shared-cnpg-rw.spoke-platform-data.svc.cluster.local"
                            port: "5432"
                      data:
                        - secretKey: username
                          remoteRef:
                            key: /spoke-pool/<cell-id>/tenants/<tenant-id>/db-credentials
                            property: username
                        - secretKey: password
                          remoteRef:
                            key: /spoke-pool/<cell-id>/tenants/<tenant-id>/db-credentials
                            property: password
            patches:
              - type: FromCompositeFieldPath
                fromFieldPath: spec.databaseName
                toFieldPath: spec.forProvider.manifest.spec.target.template.data.database
              # ... (other patches for tenantId, cellId)
          
          # Resource 2: CNPG Database CR
          - name: database
            base:
              apiVersion: kubernetes.crossplane.io/v1alpha2
              kind: Object
              spec:
                forProvider:
                  manifest:
                    apiVersion: postgresql.cnpg.io/v1
                    kind: Database
                    spec:
                      cluster:
                        name: shared-cnpg
                      owner: crossplane_admin  # NOT tenant user
                      ensure: present
          
          # Resource 3: provider-sql Role (tenant user)
          - name: db-user
            base:
              apiVersion: postgresql.sql.crossplane.io/v1alpha1
              kind: Role
              spec:
                forProvider:
                  passwordSecretRef:
                    namespace: ""  # Patched to tenant-<id>
                    name: ""  # Patched to tenant-<id>-db-credentials
                    key: password
          
          # Resource 4: provider-sql Grant (CONNECT on database)
          - name: grant-connect
            base:
              apiVersion: postgresql.sql.crossplane.io/v1alpha1
              kind: Grant
              spec:
                forProvider:
                  privileges:
                    - CONNECT
                  role: ""  # Patched to tenant_<id>_user
                  database: ""  # Patched to tenant-<id>-db
          
          # Resource 5: provider-sql DefaultPrivileges (tables)
          - name: default-privileges-tables
            base:
              apiVersion: postgresql.sql.crossplane.io/v1alpha1
              kind: DefaultPrivileges
              spec:
                forProvider:
                  privileges:
                    - SELECT
                    - INSERT
                    - UPDATE
                    - DELETE
                    - TRUNCATE
                    - REFERENCES
                    - TRIGGER
                  role: ""  # Patched to tenant_<id>_user
                  database: ""  # Patched to tenant-<id>-db
                  schema: "public"
                  objectType: "table"
                  targetRole: "crossplane_admin"
          
          # Resource 6: ESO ExternalSecret (pooler app secret)
          - name: pooler-app-secret
            base:
              apiVersion: kubernetes.crossplane.io/v1alpha2
              kind: Object
              spec:
                forProvider:
                  manifest:
                    apiVersion: external-secrets.io/v1
                    kind: ExternalSecret
                    # ... (constructs URL from Infisical credentials)
```

**Key Design Decisions:**

1. **Database Owner**: `crossplane_admin` (not tenant user)
   - Tenant user gets permissions via DefaultPrivileges
   - Allows crossplane_admin to run migrations (AtlasMigration)
   - Tenant user automatically inherits permissions on new tables

2. **No Crossplane-Generated Secrets**: 
   - ❌ Removed: Crossplane patches generating passwords from metadata.uid
   - ✅ Correct: ESO creates secrets from Infisical (source of truth)

3. **Native MRs for provider-sql**:
   - Role, Grant, DefaultPrivileges are native Crossplane MRs (not wrapped in Object)
   - Per ADR 005: Native MRs for Hub-local resources, Object MRs for remote delivery
   - provider-sql resources run on Spoke, so native MRs are correct

## Deployment Sequence

```
1. Kube-SBT Application Plane (Tenant Onboarding)
   ↓
   Generate tenant password
   ↓
   Upload to Infisical: /spoke-pool/<cell-id>/tenants/<tenant-id>/db-credentials
   ↓
2. Crossplane Reconciles TenantDatabase XR
   ↓
   Creates Object MR for ExternalSecret (Resource 1)
   ↓
3. Spoke: Object MR applies ExternalSecret manifest
   ↓
   ESO pulls from Infisical
   ↓
   Creates tenant-<id>-db-credentials secret
   ↓
4. Crossplane Creates provider-sql Role (Resource 3)
   ↓
   References tenant-<id>-db-credentials secret
   ↓
   provider-sql creates PostgreSQL user
   ↓
5. Crossplane Creates Grant + DefaultPrivileges (Resources 4-5)
   ↓
   Tenant user gets database permissions
   ↓
6. ESO Creates pooler-app secret (Resource 6)
   ↓
   Constructs URL from Infisical credentials + computed config
   ↓
7. AtlasMigration Runs
   ↓
   Uses pooler-app secret to connect
   ↓
   Runs schema migrations as crossplane_admin
   ↓
8. Application Connects
   ↓
   Uses tenant-<id>-db-credentials to query tables
```

## Comparison: Static vs Dynamic Users

### Static Platform Users (CNPG postInitSQL)

**Use Case:** Hub platform databases (control_plane, hub)

**Pattern:**
```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
spec:
  bootstrap:
    initdb:
      postInitSQL:
        - CREATE USER agentregistry WITH PASSWORD 'bootstrap-secret';
        - GRANT ALL ON DATABASE control_plane TO agentregistry;
```

**Characteristics:**
- ✅ Fixed set of users (agentregistry, mcp_server, spoke_controller)
- ✅ One-time bootstrap (users never change)
- ✅ Bootstrap secrets acceptable (operator-generated)
- ✅ Simple (no external dependencies)

**When to Use:**
- Platform infrastructure databases
- Static user set known at design time
- Users created once during cluster bootstrap

### Dynamic Tenant Users (Infisical → ESO → provider-sql)

**Use Case:** Multi-tenant SaaS databases (shared-cnpg)

**Pattern:**
```yaml
# Infisical stores credentials
# ESO creates secret
# provider-sql creates user
apiVersion: postgresql.sql.crossplane.io/v1alpha1
kind: Role
spec:
  forProvider:
    passwordSecretRef:
      name: tenant-<id>-db-credentials  # From ESO
```

**Characteristics:**
- ✅ Dynamic user creation (tenants added continuously)
- ✅ Externalized identity (Infisical is source of truth)
- ✅ Self-healing (Crossplane recreates if deleted)
- ✅ Declarative (GitOps-friendly)

**When to Use:**
- Multi-tenant SaaS applications
- Dynamic user lifecycle (create/delete over time)
- Externalized credential management required
- Self-healing infrastructure needed

## Consequences

### Positive

1. **Infisical is Source of Truth**: All tenant credentials originate from Infisical (ESO-Infisical ADR compliant)
2. **Self-Healing**: If tenant user deleted manually, Crossplane recreates it
3. **Declarative**: No imperative scripts, follows ADR 004
4. **Scalable**: Supports 1000+ tenants without cluster recreation
5. **Rotation Support**: Password rotation via Infisical → ESO sync → provider-sql update
6. **Audit Trail**: All credential changes tracked in Infisical
7. **Disaster Recovery**: Credentials restored from Infisical after cluster rebuild

### Negative

1. **Complexity**: More moving parts (Infisical + ESO + provider-sql) vs simple postInitSQL
2. **Dependency Chain**: ESO must sync before provider-sql can create user
3. **Debugging**: Failures can occur at multiple layers (Infisical API, ESO sync, provider-sql reconcile)

### Neutral

1. **Not Suitable for Bootstrap**: Cannot use this pattern for users needed during cluster bootstrap (use postInitSQL for those)
2. **Requires Kube-SBT**: Password generation and Infisical upload must happen before Crossplane reconciles

## Troubleshooting

### Issue: provider-sql Role Not Created

**Symptom:** Grant exists but Role missing

**Root Cause:** ESO secret doesn't exist yet, provider-sql Role waiting for secret

**Solution:**
```bash
# Check if ESO created the secret
kubectl get secret tenant-<id>-db-credentials -n tenant-<id>

# Check ESO ExternalSecret status
kubectl get externalsecret -n tenant-<id>

# Check provider-sql Role status
kubectl get role.postgresql.sql.crossplane.io tenant_<id>_user
```

### Issue: AtlasMigration "no such user"

**Symptom:** Migration fails with "pq: no such user"

**Root Cause:** provider-sql Role not created yet

**Solution:**
```bash
# Verify user exists in PostgreSQL
kubectl exec -n spoke-platform-data shared-cnpg-1 -- \
  psql -U postgres -c "\du tenant_app_creator_user"

# Check provider-sql Role status
kubectl describe role.postgresql.sql.crossplane.io tenant_app_creator_user
```

### Issue: ESO SecretSyncedError

**Symptom:** ExternalSecret shows "could not get secret data from provider"

**Root Cause:** Infisical path doesn't exist or credentials not uploaded

**Solution:**
```bash
# Verify secret exists in Infisical UI
# Path: /spoke-pool/<cell-id>/tenants/<tenant-id>/db-credentials

# Check ESO logs
kubectl logs -n external-secrets-system -l app.kubernetes.io/name=external-secrets
```

## References

- [CNPG Database Migration Pattern](./cnpg-database-migration-pattern.md) - Static platform users
- [ESO-Infisical Pattern](./eso-infisical-pattern.md) - Secret management patterns
- [ADR 004: Declarative Operator State](./declarative-operator-state-over-imperative-jobs.md) - Why declarative CRs over Jobs
- [ADR 005: Hub-Spoke Crossplane](./0005-unified-abstraction-layers-crossplane.md) - Composition patterns
- [Bootstrap vs Application Secrets](./bootstrap-vs-application-secrets.md) - Secret lifecycle patterns

## Summary

**The Pattern:**
1. **Kube-SBT/open-sbt** generates password → uploads to Infisical
2. **ESO** pulls from Infisical → creates K8s secret
3. **Crossplane provider-sql** references secret → creates PostgreSQL user
4. **Application** uses credentials → connects to database

**Why It Works:**
- **Externalized Identity**: Infisical is source of truth (ESO-Infisical ADR Pattern A2a)
- **Declarative**: Crossplane provider-sql manages user lifecycle (ADR 004)
- **Self-Healing**: User recreated if deleted (Kubernetes control loop)
- **Scalable**: Supports dynamic tenant creation (multi-tenant SaaS)
- **Auditable**: All changes tracked in Infisical

**When to Use:**
- Multi-tenant SaaS applications
- Dynamic user lifecycle (tenants created/deleted continuously)
- Externalized credential management required
- Self-healing infrastructure needed

**When NOT to Use:**
- Static platform users (use CNPG postInitSQL instead)
- Bootstrap users needed before ESO starts (use postInitSQL)
- Single-tenant applications (simpler patterns may suffice)
