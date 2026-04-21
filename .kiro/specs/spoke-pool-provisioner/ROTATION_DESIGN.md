# Credential Rotation Design

**Feature**: Per-Tenant Database User Credential Rotation  
**Status**: Production-Ready Design  
**Date**: 2026-04-21

---

## Executive Summary

This document provides the high-level design for zero-downtime credential rotation of per-tenant database users. The design uses a dual-user A/B pattern with Infisical as the source of truth and External Secrets Operator (ESO) for Kubernetes Secret synchronization.

**Key Principle**: PostgreSQL is authoritative for credential state. Infisical is the distribution layer for applications. ESO syncs Infisical to K8s Secrets.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│ Rotation Trigger (CronJob or Manual)                        │
│ - Schedule: Every 90 days                                   │
│ - Action: Update PostgreSQL + Infisical                     │
└─────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ PostgreSQL (Authoritative for credential state)             │
│ - ALTER ROLE user_b WITH PASSWORD '<new-pwd>'               │
│ - Both users (user_a, user_b) exist with identical grants   │
└─────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ Infisical (Distribution layer for applications)             │
│ - activeUser: "user_a" or "user_b"                          │
│ - users: { user_a: {...}, user_b: {...} }                   │
│ - Centralized audit trail                                   │
└─────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ ESO (Automatic Sync Layer)                                  │
│ - refreshInterval: 1m                                        │
│ - creationPolicy: Owner                                      │
│ - Projects activeUser → DB_USER, DB_PASSWORD                │
└─────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ K8s Secret (Flat Keys)                                       │
│ - DB_USER: tenant_acme_user_b                                │
│ - DB_PASSWORD: <password>                                    │
└─────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ Applications (Pooler, PostgREST)                             │
│ - New connections use active user                            │
│ - Old connections drain naturally                            │
└─────────────────────────────────────────────────────────────┘
```

---

## Core Design Decisions

### 1. Dual-User A/B Pattern

**Concept**: Two database users (user_a, user_b) with identical permissions alternate as the active user.

**Why**:
- ✅ Zero-downtime rotation (both users valid during grace period)
- ✅ Rollback safety (can revert to previous user instantly)
- ✅ PgBouncer-compatible (no connection pool disruption)
- ✅ No forced restarts required

**User States**:
- **Active**: Primary user for new connections
- **Inactive**: Valid but not primary (grace period for stragglers)
- **Revoked**: Password invalidated (cannot authenticate)

### 2. Infisical as Distribution Layer

**Concept**: All credential updates flow through Infisical for distribution to applications.

**Why**:
- ✅ Aligns with established Pattern B (Application Secrets)
- ✅ Centralized audit trail for compliance
- ✅ ESO automatically syncs to K8s Secrets
- ✅ Consistent with existing infrastructure

**Architectural Precision**:
- **PostgreSQL is authoritative** for credential state (ALTER ROLE is the source of truth)
- **Infisical is the distribution layer** for applications (propagates credentials from PostgreSQL)
- **ESO is the sync layer** (pulls from Infisical, pushes to K8s Secrets)

**Secret Structure**:
```json
{
  "activeUser": "user_a",
  "users": {
    "user_a": {
      "username": "tenant_acme_user_a",
      "password": "<password-a>"
    },
    "user_b": {
      "username": "tenant_acme_user_b",
      "password": "<password-b>"
    }
  }
}
```

### 3. Two-Step Rotation Process

**Concept**: Separate password update from activeUser flip with 24-hour grace period.

**Why**:
- ✅ True grace period (both credentials valid for 24 hours)
- ✅ Abort window (can skip flip if issues detected)
- ✅ Clear separation of concerns
- ✅ Safer for production systems

**Steps**:
1. **Day 90**: Update inactive user password (PostgreSQL + Infisical)
2. **Day 91**: Flip activeUser in Infisical (after 24h grace period)
3. **Day 92**: Revoke old user password (optional cleanup)

### 4. ESO Flat Key Projection

**Concept**: ESO projects activeUser credentials as flat keys (DB_USER, DB_PASSWORD).

**Why**:
- ✅ Applications don't need to parse JSON
- ✅ Standard environment variable pattern
- ✅ Compatible with existing application code
- ✅ Automatic updates when activeUser changes

---

## Rotation Workflow

### Phase 1: Password Update (Day 90)

```
1. Rotation script runs (CronJob or manual)
2. Determine inactive user (opposite of activeUser)
3. Generate new password (32 chars, cryptographically secure)
4. Update PostgreSQL: ALTER ROLE user_b WITH PASSWORD '<new-pwd>'
5. Update Infisical: users.user_b.password = '<new-pwd>'
6. ESO syncs within 1 minute (both credentials now in K8s Secret)
7. Grace period begins (24 hours)
```

**State After Phase 1**:
- User A: Active (primary, unchanged password)
- User B: Active (secondary, new password)
- Applications: Continue using User A
- K8s Secret: Contains both credentials, activeUser still "user_a"

### Phase 2: ActiveUser Flip (Day 91)

```
1. Flip script runs (24 hours after Phase 1)
2. Verify both users have valid passwords
3. Update Infisical: activeUser = "user_b"
4. ESO syncs within 1 minute
5. K8s Secret updated: DB_USER = user_b, DB_PASSWORD = <new-pwd>
6. Applications pick up new credentials on next connection
7. Old connections drain naturally
```

**State After Phase 2**:
- User A: Inactive (still valid for stragglers)
- User B: Active (primary)
- Applications: New connections use User B
- K8s Secret: DB_USER = user_b

### Phase 3: Cleanup (Day 92 - Optional)

```
1. Cleanup script runs (24 hours after Phase 2)
2. Verify no active connections using User A
3. Revoke User A password: ALTER ROLE user_a WITH PASSWORD '<random>'
4. User A transitions to Revoked state
5. User A will be reactivated in next rotation cycle (Day 180)
```

---

## Rotation Timeline

| Day | User A State | User B State | Active User | Trigger |
|-----|-------------|-------------|-------------|---------|
| 0 | Active | Revoked | A | Initial state |
| 90 | Active | Active (new pwd) | A | Password update |
| 91 | Inactive | Active | B | ActiveUser flip |
| 92 | Revoked | Active | B | Cleanup (optional) |
| 180 | Active (new pwd) | Active | B | Next rotation |
| 181 | Active | Inactive | A | Flip back to A |

---

## Rollback Strategy

### During Grace Period (Day 90-91)

**If issues detected with User B**:
1. Update Infisical: activeUser = "user_a" (revert)
2. ESO syncs within 1 minute
3. Applications continue using User A
4. Investigate User B issue
5. Retry rotation after fix

**Why rollback is safe**:
- User A password never changed during grace period
- Both credentials remain valid in PostgreSQL
- Instant rollback via Infisical update
- No application restarts required

### After Flip (Day 91+)

**If issues detected after flip**:
1. Run rotation again (flips back to User A)
2. Or manually update Infisical activeUser
3. ESO syncs automatically
4. Applications pick up previous user

---

## Monitoring and Alerting

### Key Metrics

- `tenant_credential_age_days{tenant_id, user}` - Age of each credential
- `tenant_credential_rotation_total{tenant_id, status}` - Rotation attempts
- `tenant_credential_state{tenant_id, user, state}` - Current state
- `tenant_credential_grace_period_active{tenant_id}` - Grace period status

### Critical Alerts

- **Warning**: Credential age > 85 days (rotation due soon)
- **Critical**: Credential age > 95 days (rotation overdue)
- **Critical**: Rotation failure (manual intervention required)
- **Warning**: Inactive credentials still in use after 7 days

---

## Security Considerations

1. **PostgreSQL is authoritative** for credential state (ALTER ROLE is the source of truth)
2. **Infisical is the distribution layer** for applications (propagates credentials, provides audit trail)
3. **K8s Secrets owned by ESO** - DO NOT EDIT DIRECTLY (creationPolicy: Owner)
4. **Passwords never logged** - No exposure in logs or CR status
5. **Rotation events audited** - Infisical provides audit trail
6. **Grace period minimizes risk** - Both credentials valid during transition
7. **ESO automatic sync** - Credentials always in sync (Infisical → K8s)

---

## Operational Guidelines

### Do's

✅ Use 90-day rotation interval (balances security and operations)  
✅ Rely on dual-user overlap period (not forced restarts)  
✅ Monitor credential age and rotation success  
✅ Test rotation in dev cluster first  
✅ Keep cleanup optional (24-hour grace period usually sufficient)  

### Don'ts

❌ Don't edit K8s Secrets directly (ESO owns them)  
❌ Don't force application restarts (let connections drain)  
❌ Don't skip grace period (safety mechanism)  
❌ Don't over-rotate (90 days is sufficient)  
❌ Don't use Stakater Reloader (bypasses dual-user safety)  

---

## Implementation Phases

### Phase 3 (Current): Manual Trigger

- Platform Admin triggers rotation via kubectl annotate
- Rotation scripts run on-demand
- Validate workflow in production
- Build operational confidence

### Phase 2 (Future): Automated Trigger

- Daily CronJob checks credential age (last-rotation timestamp)
- Triggers rotation scripts directly for credentials > 90 days old
- Fully automated rotation workflow
- Monitoring and alerting in place

---

## References

1. **Infisical Secret Rotation Overview**  
   https://infisical.com/docs/documentation/platform/secret-rotation/overview/

2. **Infisical PostgreSQL Credentials Rotation**  
   https://infisical.com/docs/documentation/platform/secret-rotation/postgres-credentials

3. **ESO-Infisical Pattern ADR**  
   `docs/adr/eso-infisical-pattern.md` - Pattern B: Application Secrets

4. **PostgreSQL ALTER ROLE Documentation**  
   https://www.postgresql.org/docs/current/sql-alterrole.html

---

## Next Steps

1. Review this design with the team
2. Implement rotation scripts (see ROTATION_IMPLEMENTATION.md)
3. Deploy to dev cluster for testing
4. Execute Phase 3.2.5 tasks (manual rotation)
5. Validate in production with manual triggers
6. Plan Phase 2 automation (CronJob)

---

**Document Version**: 1.0  
**Last Updated**: 2026-04-21  
**Status**: Production-Ready Design

