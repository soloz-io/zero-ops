#!/usr/bin/env bash
# scripts/hetzner/reconcile-tenant-dns.sh
# =============================================================================
# Codifies the public DNS A records for tenant ingress on the Hetzner Cloud DNS
# zone (nutgraf.in). Idempotent: safe to re-run; reconciles existing records
# to the declared values and creates missing ones.
#
# Background (ADR-049 gateway migration):
#   Tenant ingress moved from Ingress-NGINX to the Cilium Gateway API. The
#   gateway's Hetzner LoadBalancer (cilium-gateway-waypoint-gateway) is the
#   public endpoint; waypoint.nutgraf.in / api.waypoint.nutgraf.in must point
#   at its LB IP. Records are managed via the Hetzner Cloud API (hcloud zone
#   rrset), NOT the legacy dns.hetzner.com API.
#
# Declared records (name -> type -> value):
#   waypoint.nutgraf.in      A  77.42.12.176   (waypoint frontend gateway LB)
#   api.waypoint.nutgraf.in  A  77.42.12.176   (waypoint BFF gateway LB)
#
# Usage:
#   ./reconcile-tenant-dns.sh                        # reconcile all declared records
#   HCLOUD_TOKEN=<token> ./reconcile-tenant-dns.sh   # override token source
#
# Token source: $ZERO_OPS_DIR/k8-secrets/hetzner/token (gitignored).
# =============================================================================
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ZERO_OPS_DIR="$(git -C "$HERE/.." rev-parse --show-toplevel 2>/dev/null || echo "$(dirname "$HERE")")"
TOKEN_FILE="${ZERO_OPS_DIR}/k8-secrets/hetzner/token"

if [[ -z "${HCLOUD_TOKEN:-}" ]]; then
  if [[ ! -f "$TOKEN_FILE" ]]; then
    echo "ERROR: HCLOUD_TOKEN not set and $TOKEN_FILE missing." >&2
    exit 1
  fi
  export HCLOUD_TOKEN="$(cat "$TOKEN_FILE")"
fi

ZONE_NAME="nutgraf.in"
ZONE_ID="1476064"   # hcloud zone list -> nutgraf.in

# Declared records: <name>|<type>|<value>
# NOTE: rrset name is relative to the zone (no trailing .nutgraf.in).
#   waypoint   = waypoint.nutgraf.in
#   api.waypoint = api.waypoint.nutgraf.in
declare -a RECORDS=(
  "waypoint|A|77.42.12.176"
  "api.waypoint|A|77.42.12.176"
)

# ── Helpers ──────────────────────────────────────────────────────────────────
rrset_exists() {
  local name="$1" type="$2"
  hcloud zone rrset describe "$ZONE_ID" "$name" "$type" &>/dev/null
}

rrset_values() {
  local name="$1" type="$2"
  hcloud zone rrset describe "$ZONE_ID" "$name" "$type" -o json 2>/dev/null \
    | python3 -c 'import json,sys; print("\n".join(r["value"] for r in json.load(sys.stdin).get("records", [])))'
}

# ── Main ─────────────────────────────────────────────────────────────────────
echo "=== Reconcile tenant DNS records on ${ZONE_NAME} (zone ${ZONE_ID}) ==="

for entry in "${RECORDS[@]}"; do
  IFS='|' read -r NAME TYPE VALUE <<< "$entry"
  echo "--- ${NAME}.${ZONE_NAME} ${TYPE} -> ${VALUE} ---"

  if rrset_exists "$NAME" "$TYPE"; then
    if rrset_values "$NAME" "$TYPE" | grep -qx "$VALUE"; then
      echo "    ✓ ${NAME} already ${VALUE} (no change)"
    else
      CUR=$(rrset_values "$NAME" "$TYPE" | head -1)
      echo "    ~ ${NAME} currently ${CUR:-<none>} -> updating to ${VALUE}"
      hcloud zone rrset update --name "$NAME" --type "$TYPE" --record "$VALUE" "$ZONE_ID" >/dev/null
      echo "    ✓ updated"
    fi
  else
    echo "    + creating ${NAME} -> ${VALUE}"
    hcloud zone rrset create --name "$NAME" --type "$TYPE" --record "$VALUE" "$ZONE_ID" >/dev/null
    echo "    ✓ created"
  fi
done

echo ""
echo "=== ✓ Tenant DNS reconciled ==="
echo "    Verify: dig +short waypoint.${ZONE_NAME}  # expect ${RECORDS[0]##*|}"
