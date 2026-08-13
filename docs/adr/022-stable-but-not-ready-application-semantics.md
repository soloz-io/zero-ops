# ADR 022: Stable-but-Not-Ready Application Semantics

## Status
Accepted

## Context
To prevent applications from crashing before their dependent databases were ready, we previously utilized `initContainers` containing blocking shell loops (e.g., `until pg_isready; do sleep 2; done`). Additionally, we allowed pods to enter `CrashLoopBackOff` as a mechanism for waiting on dependencies.

This approach is an operational anti-pattern:
1. `pg_isready` only validates socket availability, not logical schema readiness or RBAC grants.
2. `CrashLoopBackOff` triggers alert storms, pollutes metrics, and confuses SRE dashboards.
3. `initContainers` used for distributed orchestration hide system state from the Kubernetes scheduler.

## Decision
We will adopt **Stable-but-Not-Ready** semantics across the platform, leaning fully into Kubernetes Eventual Consistency.

1. **Purge Orchestration InitContainers:** We will remove all `wait-for-db` style `initContainers`. (Note: `initContainers` used for local filesystem prep or certificate injection remain permitted).
2. **Internal Exponential Backoff:** All custom Go applications (`kube-sbt-api`, `mcp-server`, `auth-proxy`) MUST implement internal, infinite exponential backoff loops for their external dependencies (Databases, NATS, APIs) rather than executing `log.Fatal()` or `os.Exit(1)` on startup.
3. **Probe Separation:** 
   - `livenessProbes` must remain `True` while the application is waiting for dependencies (preventing restarts).
   - `readinessProbes` must report `False` until all dependencies are connected (keeping traffic away from the pod).
   - `startupProbes` must be configured with wide failure thresholds (e.g., 5-10 minutes) for third-party apps (Ory, Spire) to allow infrastructure convergence without triggering restarts.

## Amendment (2026-05-28): Retry Constraints

### Context
Infinite exponential backoff loops in custom controllers and applications mask systemic failures, create thundering herd scenarios, and prevent accurate observability of degraded dependencies.

### Decision
All custom applications and controllers implementing retry logic for external dependencies (Databases, NATS, APIs) must enforce strict boundaries.

**Retry Constraints:**
- Maximum backoff duration is capped at 5 minutes.
- Full jitter must be applied to all backoff calculations to prevent synchronized retries across the fleet.
- Applications must expose a `dependency_unavailable_total` Prometheus counter metric, incremented upon every failed connection attempt.
- Applications must expose a `dependency_status` Prometheus gauge metric (0=unavailable, 1=available) for each dependency, enabling immediate determination of current failure state.

## Ownership

This ADR defines application-level readiness probe semantics and does not own platform resources. For resource ownership, see ADR-039.

## Consequences
* **Positive:** Significant reduction in restart storms and false-positive alerts during cluster convergence.
* **Positive:** Preserves memory state and provides cleaner logs for debugging dependency failures.
* **Positive:** Prevents thundering herd scenarios through jittered backoff.
* **Positive:** Enables observability of dependency health via Prometheus metrics.
* **Negative:** Requires strict adherence to connection retry logic in all custom software development.