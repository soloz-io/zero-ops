# Phase 2 — Infisical Identity Lockout Self-Heal (Operator Hardening)

Prerequisite: `.kiro/specs/completed/infisical-pki-authority/` (P0 PASSED).
Self-heal targets the CONFIRMED single authority (pod .80 via svc URL) only.

## Requirements

1. R1 — Detect: on each reconcile, probe the tracked machine identity's
   Universal-Auth login state using the identity's own stored credentials.
2. R2 — Classify: distinguish `healthy` (200) from `locked` (401 "temporarily
   locked") from `defect` (401 "Invalid credentials"). The three states are
   mutually exclusive and drive different actions.
3. R3 — Heal locked only: on `locked`, clear the lockout via the admin
   endpoint (`POST /api/v1/auth/universal-auth/sign-in/clear-lockout`) and
   re-probe to confirm recovery.
4. R4 — NEVER clear on `defect`: "Invalid credentials" means the stored
   secret does not authenticate (e.g., missing key, rotated-but-not-delivered).
   Clearing would re-arm the failure loop and hide the defect. Surface it via
   a status condition instead.
5. R5 — Flood guard: at most one probe + one clear per reconcile; no
   per-identity tight retry loops (rely on existing reconcile backoff).
6. R6 — Target only the single authority: the operator's configured
   `--infisical-url` (svc URL). No multi-endpoint probing.
7. R7 — Acceptance (deliberate-lock test, production topology): cause a
   real lockout on a fleet identity (3 bad logins), verify the operator
   clears it, and verify login + spoke CertificateRequests recover. This
   proves recovery against the actual production topology, not an assumed
   backend.

## Design

Owner: `spoke-identity-operator` (hub-resident; already authenticates as the
hub admin machine identity against the svc URL; owns the SpokeMachineIdentity
CRs; write access to `platform-capi` CRS wrappers).

Flow per SpokeMachineIdentity reconcile (new step, after `ensureCRSWrapper`):

1. If `status.clientId` empty -> skip (no material yet, fail-closed).
2. Read credentials: CRS wrapper `{spoke}-machine-identity` (`identity.yaml`,
   platform-ops copy `client-id`/`client-secret`); fallback `smi-{spoke}-auth`
   (`clientId`/`clientSecret`).
3. `ProbeIdentityLockout(clientId, clientSecret)`:
   - 200 -> healthy; delete any transient lockout state; continue.
   - locked -> `ClearLockout(identityId, clientId)` -> re-probe.
   - defect -> set `Ready=False`, reason `AuthenticationDefect`, message with
     body excerpt; return nil (do NOT fail the whole reconcile with error, do
     NOT clear).
4. Set `Ready=True` reason `Reconciled` as today.

Client additions (`internal/infisical`):

- `ProbeIdentityLockout(ctx, clientID, clientSecret) (locked bool, err error)`
  - classifies 401 body: contains `"temporarily locked"` -> locked=true,nil;
    contains `"Invalid credentials"` -> defect sentinel error
    `ErrInvalidCredentials`; other 401 -> error.
- `ClearLockout(ctx, identityID, clientID) error`
  - POST `/api/v1/auth/universal-auth/sign-in/clear-lockout` with the
    operator's bearer token, body `{identityId, clientId, lockedOut: false}`;
    idempotent (accept 200/201; treat already-unlocked as success).

Failure modes the design MUST NOT amplify (learned from the incident):
- empty/missing secret key -> probe yields `Invalid credentials` -> DEFECT,
  no clear, condition surfaces it (would have caught the incident in minutes).
- issuer re-locker -> heal clears lock, issuer logs in 200 (backend deletes
  lock keys on success), no loop because cleared state stays cleared while the
  secret is valid.

## Deliverables

- [ ] `internal/infisical/paths.go`: clear-lockout path constant
- [ ] `internal/infisical/client.go`: ProbeIdentityLockout + ClearLockout
- [ ] `internal/infisical/client_test.go`: httptest coverage of 200/locked/
      defect/other-401/idempotent-clear
- [ ] `internal/controller/spokemachineidentity_controller.go`:
      ensureIdentityUnlock step + AuthenticationDefect condition handling
- [ ] `internal/controller/..._test.go`: locked->clear, defect->no-clear,
      missing-credentials->skip
- [ ] build + unit tests green
- [ ] rollout via ArgoCD (image build/tag)
- [ ] Acceptance: deliberate-lock test on production topology (R7), run
      against identity `94a8cbac` with the operator live; record result