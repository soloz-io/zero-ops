#!/usr/bin/env bash
#
# Realign a tenant's PostgreSQL role password with the Secret ESO delivers.
#
# WHY THIS EXISTS
#
#   Crossplane's provider-sql cannot read a password back — no database exposes
#   one — so it infers whether an update is needed from the referenced Secret's
#   resourceVersion. When the role is recreated underneath it while the Secret is
#   unchanged, there is nothing for it to notice: it reports
#
#     Ready=True Available
#     Synced=True ReconcileSuccess
#
#   having done nothing, while the role in PostgreSQL carries a different password
#   from the Secret every consumer is using. The tenant's migrations then fail with
#   `password authentication failed`, and the tenant's own resources all report
#   healthy.
#
#   This happens after a tenant database is rebuilt — which, under the node-pinned
#   local-path storage of ADR-046, is the ordinary consequence of losing a node.
#
# WHAT IT DOES
#
#   Reads the password ESO delivered and applies it to the role with ALTER ROLE,
#   making PostgreSQL agree with the Secret rather than the other way round. The
#   Secret is downstream of Infisical, which is the System of Record (ADR-003), so
#   the Secret is authoritative and the role is what must move.
#
#   The password is never printed, never passed as an argument, and never written
#   to disk: it is piped into psql on stdin so it does not appear in the process
#   list on the database node either.
#
# USAGE
#   scripts/secret/realign-tenant-db-password.sh <tenant-id> [--dry-run]
#
#   KUBECONFIG must point at the SPOKE hosting the tenant.
#
set -euo pipefail

TENANT="${1:-}"
DRY_RUN=0
[[ "${2:-}" == "--dry-run" ]] && DRY_RUN=1
[[ -z "$TENANT" ]] && { sed -n '/^# USAGE/,/^#$/p' "$0" | sed 's/^# \{0,1\}//'; exit 1; }

NS="tenant-${TENANT}"
SECRET="tenant-${TENANT}-db-credentials"
ROLE="tenant-${TENANT}-user"
DATA_NS="${DATA_NS:-platform-data}"
CLUSTER="${CNPG_CLUSTER:-shared-cnpg}"

log()  { printf '%s\n' "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

log "=== Realigning DB password for tenant '${TENANT}' ==="
log "    role:    ${ROLE}"
log "    secret:  ${NS}/${SECRET}"
log "    cluster: ${DATA_NS}/${CLUSTER}"

PRIMARY=$(kubectl -n "$DATA_NS" get cluster "$CLUSTER" \
  -o jsonpath='{.status.currentPrimary}' 2>/dev/null || true)
[[ -z "$PRIMARY" ]] && fail "could not determine the primary for ${CLUSTER}"
log "    primary: ${PRIMARY}"

# Refuse to act on a Secret ESO has not successfully synced: applying a stale
# value would move the role further from the System of Record, not closer.
STATE=$(kubectl -n "$NS" get externalsecret -o json 2>/dev/null | python3 -c "
import json,sys
try: items=json.load(sys.stdin)['items']
except Exception: items=[]
for i in items:
    if i.get('spec',{}).get('target',{}).get('name')=='${SECRET}':
        c=(i.get('status',{}).get('conditions') or [{}])[0]
        print(c.get('reason','')); break
" 2>/dev/null || true)
if [[ -n "$STATE" && "$STATE" != "SecretSynced" ]]; then
  fail "the ExternalSecret for ${SECRET} reports ${STATE}; fix delivery before realigning the role"
fi

kubectl -n "$NS" get secret "$SECRET" >/dev/null 2>&1 \
  || fail "secret ${NS}/${SECRET} not found"

PW=$(kubectl -n "$NS" get secret "$SECRET" -o jsonpath='{.data.password}' | base64 -d)
[[ -z "$PW" ]] && fail "secret ${NS}/${SECRET} has no password"
log "    password: ${#PW} characters (value not shown)"

if [[ $DRY_RUN -eq 1 ]]; then
  log ""
  log "DRY RUN — would ALTER ROLE \"${ROLE}\" to match the delivered secret."
  exit 0
fi

# Passed on stdin rather than in the SQL text so the value never reaches a
# command line. ON_ERROR_STOP turns a failed ALTER into a non-zero exit rather
# than a success with a warning.
printf "%s" "ALTER ROLE \"${ROLE}\" WITH PASSWORD '${PW}';" \
  | kubectl -n "$DATA_NS" exec -i "$PRIMARY" -c postgres -- \
      psql -U postgres -v ON_ERROR_STOP=1 -f - >/dev/null \
  || fail "ALTER ROLE failed — if this reports 'permission denied to alter role', the composition's ADMIN OPTION grant has not reconciled"

log ""
log "✓ ${ROLE} now matches ${NS}/${SECRET}"
log ""
log "Consumers holding a pooled connection opened with the previous password"
log "will not notice until they reconnect. Restart the tenant's workloads, or"
log "delete the pooler pods, if authentication failures persist."
