#!/usr/bin/env bash
# =============================================================================
# scripts/recover-tenant-workloads.sh
#
# Recover a tenant's workloads after the spoke's Postgres has been re-bootstrapped.
#
# WHEN YOU NEED THIS
# ------------------
# Rebuilding a home worker destroys the database. CNPG stores it on a `local-path`
# PVC, and ADR-046 is explicit that those are node-pinned: "data lives on the exact
# home worker that bound the claim". Stop-VM / Remove-VM takes the disk with it.
# The PVC object survives and rebinds to an empty directory, so CNPG starts, finds
# no PGDATA, and exits cleanly in a loop — pg_controldata returns exit status 1.
#
# What follows is a chain where every symptom is far from its cause:
#
#   database volume lost
#     -> CNPG Database CR still reports applied:true / observedGeneration:N, so the
#        operator never recreates the database (it believes it already did)
#     -> the PreSync migration Job cannot connect, exhausts backoffLimit, and is
#        pruned by its hook-delete-policy
#     -> ArgoCD resumes an operation whose cached syncResult says the hook is
#        Running while the live Job is gone, and waits on it forever
#     -> the Rollouts are never created, so frontend/bff Services have no pods
#     -> the tenant hostname answers 503 "no healthy upstream" from Envoy, with
#        every Gateway, certificate and DNS record perfectly healthy
#
# Nothing in that chain reports an error at the level you are looking at, which is
# why this script exists: the diagnosis costs hours and the fix costs seconds.
#
# WHAT THIS SCRIPT IS NOT
# -----------------------
# Two of the steps below paper over defects that deserve a real fix. They are
# called out at the point of use so they do not quietly become permanent:
#
#   * The Database CR is orphaned — no ownerReferences, no argocd.argoproj.io/
#     instance label. If it were reconciled by the tenant-db composition
#     (manifests/spoke/spoke-catalog/infra/tenant-db/composition.yaml) or by
#     ArgoCD, a lost database would be recreated automatically and step 1 would
#     not need to exist.
#   * Losing the database on every worker rebuild is a consequence of node-pinned
#     local-path storage. A replicated or backed-up data tier removes the whole
#     class; this script only shortens the recovery.
#
# Usage:
#   ./recover-tenant-workloads.sh --tenant waypoint --env dev
#   ./recover-tenant-workloads.sh --tenant waypoint --env dev --dry-run
#   ./recover-tenant-workloads.sh --tenant waypoint --env dev --spoke spoke-pool-hybrid-dev-01
# =============================================================================
set -euo pipefail

TENANT=""; ENVIRONMENT="dev"; SPOKE=""; DRY_RUN=0
HUB_KUBECONFIG="${HUB_KUBECONFIG:-$(cd "$(dirname "$0")/.." && pwd)/k8-secrets/kubeconfig/hub-hybrid-dev.kubeconfig}"

usage() { sed -n '2,60p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tenant)  TENANT="$2"; shift 2 ;;
    --env)     ENVIRONMENT="$2"; shift 2 ;;
    --spoke)   SPOKE="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage 0 ;;
    *) echo "ERROR: unknown option $1" >&2; usage 1 ;;
  esac
done
[[ -n "$TENANT" ]] || { echo "ERROR: --tenant is required" >&2; usage 1; }
[[ -f "$HUB_KUBECONFIG" ]] || { echo "ERROR: hub kubeconfig not found at $HUB_KUBECONFIG" >&2; exit 1; }

SPOKE="${SPOKE:-spoke-pool-hybrid-${ENVIRONMENT}-01}"
APP="tenant-${TENANT}-${ENVIRONMENT}-workloads"
NS="tenant-${TENANT}"
DB="tenant-${TENANT}-db"

hub()   { kubectl --kubeconfig "$HUB_KUBECONFIG" "$@"; }
run()   { if [[ "$DRY_RUN" == "1" ]]; then echo "      [dry-run] $*"; else "$@"; fi; }

echo "=== Recover tenant workloads ==="
echo "    tenant=${TENANT} env=${ENVIRONMENT} spoke=${SPOKE} app=${APP}"
[[ "$DRY_RUN" == "1" ]] && echo "    DRY RUN — no changes will be made"
echo ""

# ── Spoke kubeconfig, always from the hub Secret ─────────────────────────────
# Never a file under k8-secrets/: those go stale the moment the spoke's endpoint
# changes, and reading the wrong one silently targets a cluster that no longer
# exists. CAPI keeps this Secret in step with the live controlPlaneEndpoint.
SPOKE_KC="$(mktemp -t spoke-kc-XXXXXX)"
trap 'rm -f "$SPOKE_KC"' EXIT
if ! hub get secret -n platform-capi "${SPOKE}-kubeconfig" -o jsonpath='{.data.value}' 2>/dev/null \
     | base64 -d > "$SPOKE_KC" 2>/dev/null || [[ ! -s "$SPOKE_KC" ]]; then
  echo "ERROR: could not read ${SPOKE}-kubeconfig from the hub" >&2; exit 1
fi
spoke() { kubectl --kubeconfig "$SPOKE_KC" "$@"; }

CNPG_POD="$(spoke get pods -n platform-data -l cnpg.io/instanceRole=primary -o name 2>/dev/null | head -1 | cut -d/ -f2)"
[[ -n "$CNPG_POD" ]] || CNPG_POD="$(spoke get pods -n platform-data --no-headers 2>/dev/null | awk '/cnpg/ && $2=="1/1"{print $1; exit}')"

# ── 1. Database exists in Postgres, not just as a CR ─────────────────────────
# The CR reporting applied:true is not evidence the database is there. After a
# re-bootstrap the operator's observedGeneration still matches, so it does not
# reconcile — and an annotation will not move it either, because the gate is on
# .spec, not metadata. Recreating the CR is what forces a fresh reconcile.
# databaseReclaimPolicy: retain means deleting the CR never drops data.
echo "    [1/4] Database ${DB}..."
if [[ -z "$CNPG_POD" ]]; then
  echo "      ✗ no running CNPG primary in platform-data — fix Postgres first, then re-run"
  echo "        (a CrashLoopBackOff with 'pg_controldata: exit status 1' means the PVC is"
  echo "         empty: delete the PVC so CNPG re-bootstraps, then re-run this script)"
  exit 1
fi
if spoke exec -n platform-data "$CNPG_POD" -c postgres -- \
     psql -U postgres -tAc "SELECT 1 FROM pg_database WHERE datname='${DB}'" 2>/dev/null | grep -q 1; then
  echo "      ✓ present in Postgres"
else
  echo "      → CR claims applied but the database is absent; recreating the CR to force reconcile"
  CR_YAML="$(spoke get database.postgresql.cnpg.io -n platform-data "$DB" -o yaml 2>/dev/null \
             | python3 -c "
import sys,yaml
d=yaml.safe_load(sys.stdin)
for k in ('creationTimestamp','resourceVersion','uid','generation','managedFields','annotations'):
    d['metadata'].pop(k,None)
d.pop('status',None)
print(yaml.safe_dump(d))" 2>/dev/null || true)"
  if [[ -z "$CR_YAML" ]]; then
    echo "      ✗ Database CR ${DB} not found — nothing to recreate."
    echo "        It is orphaned by design today (no ownerRefs, no ArgoCD instance label),"
    echo "        so if it is gone nothing will recreate it. Re-apply the tenant-db"
    echo "        composition output, or recreate the CR by hand."
    exit 1
  fi
  run spoke delete database.postgresql.cnpg.io -n platform-data "$DB" --ignore-not-found=true
  if [[ "$DRY_RUN" == "0" ]]; then
    printf '%s' "$CR_YAML" | spoke apply -f - >/dev/null
    for _ in $(seq 1 12); do
      sleep 5
      spoke exec -n platform-data "$CNPG_POD" -c postgres -- \
        psql -U postgres -tAc "SELECT 1 FROM pg_database WHERE datname='${DB}'" 2>/dev/null | grep -q 1 && break
    done
    if spoke exec -n platform-data "$CNPG_POD" -c postgres -- \
         psql -U postgres -tAc "SELECT 1 FROM pg_database WHERE datname='${DB}'" 2>/dev/null | grep -q 1; then
      echo "      ✓ database created"
    else
      echo "      ✗ still absent after 60s — check the CNPG operator logs before continuing"
      exit 1
    fi
  fi
fi

# ── 2. Pods blocked on absent Secrets ────────────────────────────────────────
# Reported, never invented. A missing Secret means either the live spec has
# drifted from Git (ArgoCD will overwrite it in step 4) or an ExternalSecret has
# not synced — and fabricating one here would mask both.
echo "    [2/4] Pods blocked on missing Secrets..."
MISSING="$(spoke get events -n "$NS" --field-selector reason=FailedMount 2>/dev/null \
           | grep -oE 'secret "[^"]+" not found' | sort -u || true)"
if [[ -n "$MISSING" ]]; then
  echo "$MISSING" | sed 's/^/      ! /'
  echo "      → not created here. If the live spec has drifted from Git, step 4 corrects it;"
  echo "        if an ExternalSecret is failing, fix that first or the pod stays Pending."
else
  echo "      ✓ none"
fi

# ── 3. ArgoCD operation wedged on a hook that no longer exists ───────────────
# The signature is "waiting for completion of hook ..." while that Job is absent:
# the cached syncResult still records it Running. Removing .operation does NOT
# clear this — the controller resumes from status.operationState, so that is what
# has to go.
echo "    [3/4] ArgoCD operation state for ${APP}..."
OP_MSG="$(hub get application -n platform-ops "$APP" -o jsonpath='{.status.operationState.message}' 2>/dev/null || true)"
OP_PHASE="$(hub get application -n platform-ops "$APP" -o jsonpath='{.status.operationState.phase}' 2>/dev/null || true)"
if [[ "$OP_MSG" == *"waiting for completion of hook"* ]] || [[ "$OP_PHASE" == "Failed" ]] || [[ "$OP_PHASE" == "Error" ]]; then
  echo "      → phase=${OP_PHASE:-<none>}; clearing status.operationState"
  run hub patch application -n platform-ops "$APP" \
      --type=json -p '[{"op":"remove","path":"/status/operationState"}]'
else
  echo "      ✓ phase=${OP_PHASE:-<none>} — nothing wedged"
fi

# ── 4. Re-sync and report ────────────────────────────────────────────────────
echo "    [4/4] Refreshing ${APP}..."
run hub annotate application -n platform-ops "$APP" argocd.argoproj.io/refresh=hard --overwrite
if [[ "$DRY_RUN" == "1" ]]; then echo ""; echo "=== dry run complete ==="; exit 0; fi

for i in $(seq 1 24); do
  sleep 10
  SYNC="$(hub get application -n platform-ops "$APP" -o jsonpath='{.status.sync.status}' 2>/dev/null || true)"
  HEALTH="$(hub get application -n platform-ops "$APP" -o jsonpath='{.status.health.status}' 2>/dev/null || true)"
  PODS="$(spoke get pods -n "$NS" --no-headers 2>/dev/null | grep -c "Running" || true)"
  printf "      [%02d] sync=%s health=%s running-pods=%s\n" "$i" "${SYNC:-?}" "${HEALTH:-?}" "${PODS:-0}"
  [[ "$SYNC" == "Synced" && "$HEALTH" == "Healthy" ]] && break
done

echo ""
echo "=== Final state ==="
spoke get pods -n "$NS" --no-headers 2>/dev/null | sed 's/^/    /' || true
echo ""
# health Healthy with zero tracked resources is not success — that is the
# ComparisonError false-green. Report what is actually running instead.
if [[ "$(hub get application -n platform-ops "$APP" -o jsonpath='{.status.sync.status}' 2>/dev/null)" == "Synced" ]]; then
  echo "    ✓ ${APP} Synced"
else
  echo "    ! ${APP} not Synced — check: kubectl --kubeconfig \$HUB_KUBECONFIG describe application -n platform-ops ${APP}"
fi
