#!/usr/bin/env bash
#
# Seed the externally-sourced Infisical keys — the ones no controller can
# regenerate — from the local k8-secrets/ tree.
#
# WHY THIS EXISTS
#
#   Most platform secrets are produced by the hub-operator: it generates them,
#   writes them to Infisical, and ESO delivers them. A handful cannot be, because
#   they are issued by third parties — Hetzner, Grafana Cloud, GitHub. When
#   Infisical is reinitialised those keys are simply gone, every ExternalSecret
#   that reads one reports "could not get secret data from provider", and nothing
#   in the platform can recover them. They live in k8-secrets/ and have to be put
#   back by hand. This script is that by-hand step, made repeatable.
#
#   The key -> file mapping is the same one recorded in
#   scripts/validate/preflight/95-infisical-key-producers.sh (its EXTERNAL map),
#   which is what stops the validator failing on these. Keep the two in step: if
#   you add a key here, declare it there, or preflight will call it unproduced.
#
# USAGE
#   scripts/secret/seed-external-secrets.sh <cell-id> [--dry-run]
#
#   e.g. scripts/secret/seed-external-secrets.sh spoke-pool-hybrid-dev-01
#
# AUTH
#   Reads INFISICAL_CLIENT_ID / INFISICAL_CLIENT_SECRET from the environment, or
#   from the live cluster (platform-security/infisical-auth) when a kubeconfig is
#   available. Never prints a secret value — only key names and outcomes.
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SECRETS_DIR="${SECRETS_DIR:-${REPO_ROOT}/k8-secrets}"
INFISICAL_URL="${INFISICAL_BASE_URL:-https://infisical.dev.nutgraf.in}"
PROJECT_SLUG="${INFISICAL_PROJECT_SLUG:-hub-secrets}"
ENVIRONMENT="${INFISICAL_ENVIRONMENT:-dev}"

CELL_ID="${1:-}"
DRY_RUN=0
[[ "${2:-}" == "--dry-run" ]] && DRY_RUN=1

[[ -z "$CELL_ID" ]] && { sed -n '/^# USAGE/,/^#$/p' "$0" | sed 's/^# \{0,1\}//'; exit 1; }

# ── key : source file : infisical path ──────────────────────────────────────
# An empty path means the Infisical root, which is where the root-scoped
# consumers (cert-manager's DNS solver, the GHCR pull secret) read from.
SHARED="/spoke-pool/${CELL_ID}/shared"
MAPPINGS=(
  "S3_ACCESS_KEY_ID:s3/access-key-id:${SHARED}"
  "S3_SECRET_ACCESS_KEY:s3/secret-access-key:${SHARED}"
  "GRAFANA_CLOUD_API_KEY:grafana-cloud/api-key:${SHARED}"
  "GRAFANA_CLOUD_LOKI_URL:grafana-cloud/loki-url:${SHARED}"
  "GRAFANA_CLOUD_LOKI_USER:grafana-cloud/loki-user:${SHARED}"
  "GRAFANA_CLOUD_PROMETHEUS_URL:grafana-cloud/prometheus-url:${SHARED}"
  "GRAFANA_CLOUD_PROMETHEUS_USER:grafana-cloud/prometheus-user:${SHARED}"
  "hcloud-token:hetzner/token:/"
)

log()  { printf '%s\n' "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

# ── Credentials ─────────────────────────────────────────────────────────────
if [[ -z "${INFISICAL_CLIENT_ID:-}" || -z "${INFISICAL_CLIENT_SECRET:-}" ]]; then
  if command -v kubectl >/dev/null && kubectl -n platform-security get secret infisical-auth >/dev/null 2>&1; then
    log "Reading Infisical credentials from platform-security/infisical-auth"
    INFISICAL_CLIENT_ID=$(kubectl -n platform-security get secret infisical-auth -o jsonpath='{.data.client-id}' | base64 -d)
    INFISICAL_CLIENT_SECRET=$(kubectl -n platform-security get secret infisical-auth -o jsonpath='{.data.client-secret}' | base64 -d)
  else
    fail "set INFISICAL_CLIENT_ID and INFISICAL_CLIENT_SECRET, or point KUBECONFIG at the hub"
  fi
fi

log "=== Seeding external secrets ==="
log "    Infisical:  ${INFISICAL_URL}"
log "    Project:    ${PROJECT_SLUG}  (env: ${ENVIRONMENT})"
log "    Cell:       ${CELL_ID}"
log "    Source:     ${SECRETS_DIR}"
[[ $DRY_RUN -eq 1 ]] && log "    MODE:       dry run — nothing will be written"
log ""

TOKEN=$(curl -sS -X POST "${INFISICAL_URL}/api/v1/auth/universal-auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"clientId\":\"${INFISICAL_CLIENT_ID}\",\"clientSecret\":\"${INFISICAL_CLIENT_SECRET}\"}" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin).get("accessToken",""))' 2>/dev/null || true)
[[ -z "$TOKEN" ]] && fail "Infisical login failed — credentials rejected"

WORKSPACE_ID=$(curl -sS "${INFISICAL_URL}/api/v1/workspace" -H "Authorization: Bearer ${TOKEN}" \
  | python3 -c "
import json,sys
for w in json.load(sys.stdin).get('workspaces',[]):
    if w.get('slug')=='${PROJECT_SLUG}': print(w['id']); break
" 2>/dev/null || true)
[[ -z "$WORKSPACE_ID" ]] && fail "no project with slug '${PROJECT_SLUG}'"

seeded=0; skipped=0; missing=0

for entry in "${MAPPINGS[@]}"; do
  KEY="${entry%%:*}"; rest="${entry#*:}"
  FILE="${rest%%:*}"; PATH_="${rest#*:}"
  SRC="${SECRETS_DIR}/${FILE}"

  if [[ ! -f "$SRC" ]]; then
    log "  MISSING  ${KEY}  (no ${FILE} in k8-secrets)"
    missing=$((missing+1)); continue
  fi

  # Trailing newlines are the classic cause of a credential that looks correct
  # and authenticates nowhere.
  VALUE=$(tr -d '\r\n' < "$SRC")
  [[ -z "$VALUE" ]] && { log "  MISSING  ${KEY}  (${FILE} is empty)"; missing=$((missing+1)); continue; }

  if [[ $DRY_RUN -eq 1 ]]; then
    log "  WOULD    ${KEY}  <- ${FILE}  -> ${PATH_}"
    seeded=$((seeded+1)); continue
  fi

  body=$(python3 -c "
import json,sys
print(json.dumps({'workspaceId':sys.argv[1],'environment':sys.argv[2],'secretPath':sys.argv[3],
                  'secretValue':sys.argv[4],'type':'shared'}))" "$WORKSPACE_ID" "$ENVIRONMENT" "$PATH_" "$VALUE")

  code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "${INFISICAL_URL}/api/v3/secrets/raw/${KEY}" \
    -H 'Content-Type: application/json' -H "Authorization: Bearer ${TOKEN}" -d "$body")

  # 409 means it already exists; update instead of failing the run.
  if [[ "$code" == "409" || "$code" == "400" ]]; then
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X PATCH "${INFISICAL_URL}/api/v3/secrets/raw/${KEY}" \
      -H 'Content-Type: application/json' -H "Authorization: Bearer ${TOKEN}" -d "$body")
    [[ "$code" =~ ^2 ]] && { log "  UPDATED  ${KEY}  -> ${PATH_}"; skipped=$((skipped+1)); continue; }
  fi

  if [[ "$code" =~ ^2 ]]; then
    log "  SEEDED   ${KEY}  -> ${PATH_}"; seeded=$((seeded+1))
  else
    log "  FAILED   ${KEY}  (HTTP ${code})"; missing=$((missing+1))
  fi
done

log ""
log "seeded=${seeded} updated=${skipped} missing=${missing}"
[[ $missing -gt 0 ]] && log "NOTE: missing entries have no source file — supply them in ${SECRETS_DIR} and re-run."
exit 0
