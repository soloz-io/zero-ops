# Phase 2 — Lockout Self-Heal Tasks

Prerequisite: `.kiro/specs/completed/infisical-pki-authority/` (P0 PASSED 2026-08-19)

## Code (completed 2026-08-19)

- [x] `internal/infisical/paths.go`: clear-lockout path constant
- [x] `internal/infisical/client.go`: `ProbeIdentityLockout` + `ClearLockout` +
      `ErrInvalidCredentials` sentinel
- [x] `internal/infisical/client_test.go`: 200 / locked / invalid-credentials /
      500 / idempotent-clear coverage
- [x] `internal/controller/spokemachineidentity_controller.go`:
      `ensureIdentityUnlock` (probe -> classify -> clear-only-if-locked ->
      verify) + `identityCredentials` (wrapper defaulting + rotation fallback) +
      `AuthenticationDefect` condition override
- [x] `internal/controller/..._test.go`: locks->clear+verify; defect->no-clear +
      Ready=False/AuthenticationDefect; skip-without-material; skip-without-clientId;
      identity.yaml parser contract
- [x] `go build ./...` + `go test ./...` green

## Rollout (pending, needs review)

- [ ] Image build/tag + ArgoCD rollout of spoke-identity-operator
- [ ] Acceptance (R7): deliberate-lock test on production topology —
      induce 3 bad logins on identity `94a8cbac` (or a test identity),
      confirm operator clears on next reconcile, confirm login 200 and spoke
      CertificateRequests remain READY
- [ ] Extend `docs/runbooks/backup-credential-chain-recovery.md` with the
      operator-based recovery path (self-heal supersedes manual clear step)