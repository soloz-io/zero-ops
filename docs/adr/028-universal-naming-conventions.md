# ADR 015: Universal Naming Conventions

**Date:** 2026-05-09  
**Status:** Accepted  
**Authors:** Platform Engineering Team  
**Related ADRs:** 
- [Federated API Boundary](./008-federated-api-boundary.md)
- [Multi-Tenant Database Pattern](./006-multi-tenant-database-pattern.md)

## Context

The platform has historically used inconsistent naming patterns across different systems, with some components using underscores (`tenant_acme_db`) while Kubernetes enforces RFC-1123 (DNS-style, hyphens only). This inconsistency causes cognitive load, regex validation failures, and mapping complexity between systems.

## Decision

Mandate **strict RFC-1123 compliance** across **all systems** in the platform, including PostgreSQL databases, roles, and Kubernetes resources.

**RFC-1123 Pattern:**
- Regex: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
- Length: 1-63 characters
- Characters: lowercase letters, numbers, and hyphens only
- Cannot start or end with hyphen

**Universal Naming Pattern:**
```
Tenant ID: acme-corp
Namespace: tenant-acme-corp
Database: tenant-acme-corp-db
Role: tenant-acme-corp-user
Secret: tenant-acme-corp-db-credentials
```

## Implementation

### 1. Kubernetes Resources
All Kubernetes resources must use RFC-1123 compliant names:
```yaml
metadata:
  name: tenant-{{ .tenantId }}-db-credentials
  namespace: tenant-{{ .tenantId }}
```

### 2. PostgreSQL Resources
PostgreSQL databases and roles must use quoted identifiers to support hyphens:
```sql
CREATE DATABASE "tenant-acme-corp-db";
CREATE ROLE "tenant-acme-corp-user" WITH LOGIN PASSWORD '...';
GRANT ALL PRIVILEGES ON DATABASE "tenant-acme-corp-db" TO "tenant-acme-corp-user";
```

### 3. Crossplane XRDs
All Composite Resource Definitions must validate RFC-1123 compliance:
```yaml
tenantId:
  type: string
  description: "Tenant identifier (RFC 1123 compliant)"
  pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'
  maxLength: 63
```

### 4. Application Code
All application code must use hyphenated names consistently:
```go
// Correct
databaseName := fmt.Sprintf("tenant-%s-db", tenantID)
roleName := fmt.Sprintf("tenant-%s-user", tenantID)

// Incorrect - BANNED
databaseName := fmt.Sprintf("tenant_%s_db", tenantID)
roleName := fmt.Sprintf("tenant_%s_user", tenantID)
```

### 5. Infisical Paths
Secret paths in Infisical must use hyphens:
```
/spoke-pool/{cell-id}/tenants/{tenant-id}/db-credentials
```

## Migration Strategy

### Phase 1: New Tenants (Immediate)
- All new tenant provisioning uses RFC-1123 compliant names
- Helm templates updated to use hyphens only
- Crossplane compositions updated with hyphenated patterns

### Phase 2: Existing Tenants (Gradual)
- Backward compatibility maintained for existing underscore-based names
- Migration scripts provided to rename databases/roles
- Documentation updated with new naming standards

### Phase 3: Enforcement (Future)
- Validation hooks reject non-compliant names
- CI/CD pipelines enforce RFC-1123 compliance
- Legacy underscore patterns deprecated and removed

## Ownership

This ADR defines naming convention standards applicable to all platform components. It does not own platform resources. For resource ownership, see ADR-039.

## Consequences

**Positive:**
- Consistent naming across all platform systems
- Eliminates regex validation failures
- Reduces cognitive load for developers
- Simplifies debugging and troubleshooting
- Aligns with Kubernetes native naming standards
- PostgreSQL handles quoted hyphenated identifiers perfectly

**Negative:**
- Requires migration of existing underscore-based resources
- Temporary backward compatibility complexity
- Developer education required for new patterns

**Neutral:**
- PostgreSQL requires quoted identifiers for hyphenated names
- Slightly longer names due to hyphen separators

## Enforcement

### Validation
- RFC-1123 validator in Go code (`internal/hub-cli/validator/rfc1123.go`)
- OpenAPI patterns in all XRDs
- Helm template validation

### Monitoring
- Automated scanning for underscore patterns in code
- Alert on non-compliant resource creation
- Regular compliance audits

### Documentation
- All examples updated to use hyphens
- Migration guides provided
- Training materials for development teams

## Examples

### Correct Patterns
```yaml
# Tenant: acme-corp
metadata:
  name: acme-corp-argocd-agent-client-cert
  namespace: platform-ops

# Database: tenant-acme-corp-db
apiVersion: postgresql.cnpg.io/v1
kind: Database
metadata:
  name: tenant-acme-corp-db-database
spec:
  owner: "tenant-acme-corp-user"

# Role: tenant-acme-corp-user  
apiVersion: postgresql.sql.crossplane.io/v1alpha1
kind: Role
metadata:
  name: tenant-acme-corp-user
spec:
  name: "tenant-acme-corp-user"
```

### Incorrect Patterns (BANNED)
```yaml
# BANNED: underscores
metadata:
  name: acme_corp_argocd_agent_client_cert
  namespace: platform_ops

# BANNED: underscores
metadata:
  name: tenant_acme_corp_db
spec:
  owner: tenant_acme_corp_user
```