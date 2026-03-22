# Implementation Plan: Demo 2 Keto Authorization Enforcement

## Overview

This implementation plan adds Ory Keto relationship-based authorization checks to auth-proxy. The demo enables fine-grained permission enforcement, demonstrating "The platform enforces permissions."

**Target Outcome:** Valid JWT + valid permission → HTTP 200. Valid JWT + invalid permission → HTTP 403. Keto unavailable → HTTP 500 with clear error.

**Technology Stack:** Go (auth-proxy enhancements), Ory Keto (already deployed), Kubernetes

## Tasks

### Reference Codebases

**Local Reference Implementation:**
- Keto examples: `.kiro/specs/agentic-enterprise-onboarding/references/identity-auth/keto/`
- auth-proxy base: `cmd/auth-proxy/`, `internal/auth-proxy/`

- [ ] 1. Implement Keto client and configuration
  - Create `internal/auth-proxy/client/keto.go` with KetoClient interface
  - Implement CheckPermission using GET /relation-tuples/check (Keto v0.x API)
  - Add query parameter construction (namespace, object, relation, subject_id)
  - Parse Keto response JSON `{"allowed":true/false}`
  - Add 2-second timeout with proper error handling
  - Add KETO_READ_URL, KETO_WRITE_URL, KETO_TIMEOUT to config.go
  - Initialize KetoClient in main.go and pass to ValidateHandler
  - _Requirements: 3.6, 3.7_

- [ ] 2. Enhance extAuthz handler with Keto permission checks
  - Add getRequiredPermission helper (map path/method → Keto relation)
  - Implement platform_admin bypass (check role claim, skip Keto if platform_admin)
  - Add X-Platform-Admin-Bypass header for audit when bypass used
  - Extract tenant_id from JWT claims for Keto check
  - Call KetoClient.CheckPermission after JWT validation
  - Return HTTP 403 if Keto denies permission
  - Return HTTP 500 if Keto unavailable
  - Skip Keto check for tenant_create (no tenant_id in JWT yet)
  - Add structured logging (allowed/denied/error with user_id, tenant_id, relation)
  - _Requirements: 3.6, 3.7, 3.8_

- [ ] 3. Add observability for Keto checks
  - Add Prometheus counter: auth_proxy_keto_checks_total{result="allowed|denied|error"}
  - Add Prometheus histogram: auth_proxy_keto_check_duration_seconds
  - Add Prometheus counter: auth_proxy_keto_errors_total{reason="timeout|unavailable|invalid_response"}
  - Add structured logs (JSON) for Keto allowed/denied/error events
  - Expose metrics at /metrics endpoint
  - _Requirements: 3.6, 3.7_

- [ ] 4. Update auth-proxy deployment with Keto configuration
  - Add KETO_READ_URL env var to manifests/platform-identity/auth-proxy/deployment.yaml
  - Add KETO_WRITE_URL env var (http://ory-keto-write.ory-system.svc.cluster.local:80)
  - Add KETO_TIMEOUT env var (default: 2s)
  - Verify Keto deployment unchanged (already deployed from Demo 1)
  - Apply deployment changes to hub cluster
  - Verify auth-proxy pods restart successfully
  - _Requirements: 3.6, 3.7_

- [ ] 5. Manual demo script execution and validation
  - Create test user Alice (email: alice@acme.com, role: tenant_admin)
  - Create test user Bob (email: bob@acme.com, role: tenant_admin, no Keto tuple)
  - Alice authenticates, creates tenant "acme-corp" → Keto tuple created
  - Alice provisions environment → Keto check passes, HTTP 202
  - Bob authenticates (no tuple), tries to access acme-corp environment → HTTP 403
  - Stop Keto pod, Alice tries to provision → HTTP 500
  - Restart Keto pod, Alice retries → HTTP 202 (self-healing)
  - Verify Prometheus metrics show Keto checks
  - Verify logs show Keto allowed/denied/error events
  - _Requirements: 3.6, 3.7, 3.8_

- [ ] 6. Documentation and demo preparation
  - Update cmd/auth-proxy/README.md with Keto integration
  - Document Keto environment variables
  - Document platform_admin bypass pattern
  - Document Demo 2 scope (same-tenant only, cross-tenant deferred to Demo 3)
  - Add Keto API version note (v0.x GET with query params)
  - Prepare demo script showing permission enforcement
  - _Requirements: 3.6, 3.7, 3.8_

## Notes

- All tasks focus on manual testing only per requirements (no unit/integration tests)
- Keto already deployed from Demo 1 (no infrastructure changes needed)
- Uses Keto v0.x Read API (GET with query params, not POST with body)
- platform_admin bypasses Keto checks (Option B - role-based skip)
- Cross-tenant access control deferred to Demo 3 (same-tenant only in Demo 2)
- Demo validates "The platform enforces permissions" outcome
- Tasks build incrementally from Keto client through permission checks to manual validation
