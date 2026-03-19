---
inclusion: always
---
<!------------------------------------------------------------------------------------
   Add rules to this file or a short description and have Kiro refine them for you.
   
   Learn about inclusion modes: https://kiro.dev/docs/steering/#inclusion-modes
-------------------------------------------------------------------------------------> 

# open-sbt Additional Design Sections

This document provides references to additional design patterns discovered from SBT-AWS analysis.

## 12. Tier Management

**Key Finding:** In SBT-AWS, tiers are **not a separate entity** but rather **attributes on tenants**. This flexible approach allows tiers to be defined implicitly through tenant configuration.

**Pattern:**
- Tier is passed as part of `tenantData` during registration
- Tier flows through events (e.g., `onboardingRequest` includes `tier` field)
- Provisioning logic branches based on tier value
- No separate tier CRUD operations needed

**See:** `docs/open-sbt-tier-management.md` for detailed implementation patterns including:
- Tier as tenant attribute (recommended approach)
- Optional tier configuration table for centralized definitions
- Quota enforcement middleware
- Tier upgrade/downgrade workflows

## 13. Multi-Tenant Microservice Libraries

**Key Finding:** SBT-AWS provides the **Token Vending Machine** library that tenant microservices can use to obtain tenant-scoped AWS credentials using ABAC (Attribute-Based Access Control).

**Pattern:**
- Validates JWT tokens
- Extracts tenant attributes from claims
- Provides tenant-scoped credentials
- Enforces tenant isolation at the credential level

**open-sbt Adaptation:**
The Token Vending Machine pattern inspired a suite of Go libraries for tenant microservices:

1. **Identity Token Manager** - JWT validation and tenant context extraction
2. **Logging Manager** - Auto-inject tenant_id into all logs
3. **Metrics Manager** - Auto-tag metrics with tenant_id
4. **Token Vending Machine** - Tenant-scoped credentials for K8s resources
5. **Database Isolation Helper** - Automatic PostgreSQL RLS context setting

**See:** `docs/open-sbt-microservice-libraries.md` for complete implementation with code examples.

**Key Benefit:** Application developers write only business logic - multi-tenancy is handled by libraries.
