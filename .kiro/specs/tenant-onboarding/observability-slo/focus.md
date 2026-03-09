## Phase 5 — Hardening, Observability & SLO Verification
**Duration: 3–4 days | Risk: Low**

This isn't "nice to have" — it's what makes the PRD's SLO (`< 500ms p99`) verifiable.

Deliverables:
- Structured logging (request ID, `org_id`, `tenant_id`, duration) on all three Go services
- Prometheus metrics: request latency histograms, K8s SSA duration, DB query duration
- Load test: `POST /api/v1/tenants` at 50 RPS, verify p99 `< 500ms` with Informer cache active
- Runbook: what happens when Hydra is down? When K8s API server is slow? Document the degraded behavior.
- All PRD acceptance tests from section 7 automated in CI