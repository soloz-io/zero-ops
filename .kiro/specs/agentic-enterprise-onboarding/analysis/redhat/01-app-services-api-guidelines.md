# Red Hat Application Services API Guidelines - Applicability Analysis

## Project Overview
**Source:** `.kiro/specs/agentic-enterprise-onboarding/references/redhat/app-services-api-guidelines/`

RHOAS API guidelines based on OpenAPI and Spectral tooling for API standards validation.

## Applicability to Zero-Ops Platform

### ✅ HIGHLY APPLICABLE

**Use Case:** API Design Standards for zero-ops-api and MCP Tool Servers

**Justification:**
1. **Consistency Requirement**: Zero-Ops exposes multiple API surfaces (REST API, MCP tools, identity-service). Standardized API design prevents drift.
2. **OpenAPI-First**: PRD mandates OpenAPI specs for all APIs. RHOAS guidelines provide production-grade patterns.
3. **Spectral Validation**: Automated linting catches design issues pre-merge (CI/CD integration).
4. **Enterprise Patterns**: RHOAS guidelines encode Red Hat's multi-year API evolution lessons (pagination, error schemas, versioning).

## Recommended Adoption

### Phase 1: Core API Standards (Immediate)
**Apply to:**
- `cmd/zero-ops-api/` - Tenant/environment CRUD endpoints
- `pkg/api/` - Shared API models
- `pkg/agents/mcp/` - MCP tool server schemas

**Standards to Adopt:**
- Error response schema (RFC 7807 Problem Details)
- Pagination patterns (cursor-based for large result sets)
- Filtering/sorting query parameter conventions
- HTTP status code usage (201 vs 200, 409 vs 400)
- Deprecation headers and sunset policies

**Source Reference:**
```
.kiro/specs/agentic-enterprise-onboarding/references/redhat/app-services-api-guidelines/docs/api-standards.md
.kiro/specs/agentic-enterprise-onboarding/references/redhat/app-services-api-guidelines/spectral/
```

### Phase 2: Spectral CI Integration (Sprint 2)
**Implementation:**
1. Add Spectral ruleset to `zero-ops/` monorepo root
2. CI pipeline validates OpenAPI specs on PR
3. Block merge if critical violations detected

**Configuration Path:**
```
zero-ops/.spectral.yaml (to be created)
zero-ops/api/openapi/ (OpenAPI specs)
```

### Phase 3: MCP Tool Schema Validation (Post-MVP)
**Extend to:**
- MCP tool input/output schemas
- AgentGateway routing rules
- identity-service API contracts

## Alignment with Zero-Ops Architecture

| Zero-Ops Component | RHOAS Guideline Benefit |
|---|---|
| zero-ops-api | Consistent REST API design, error handling |
| MCP tool servers | Standardized tool schemas, validation |
| identity-service | OAuth/OIDC endpoint conventions |
| Platform Console API | Pagination, filtering for large datasets |
| Crossplane XRD | API versioning strategy (v1alpha1 → v1) |

## Implementation Priority
**Priority:** HIGH (Sprint 1 - Day 3)

**Rationale:** API design decisions made early are expensive to change post-launch. Adopting standards now prevents technical debt.

## Risks if NOT Adopted
- Inconsistent error responses across API surfaces
- Poor pagination UX (offset-based instead of cursor-based)
- Breaking changes without deprecation warnings
- Difficult API evolution (no versioning strategy)

## Next Steps
1. Review RHOAS error schema - map to Zero-Ops error types
2. Define Spectral ruleset for zero-ops-api
3. Generate OpenAPI spec from existing Go handlers (swaggo/swag)
4. Add Spectral validation to GitHub Actions CI
