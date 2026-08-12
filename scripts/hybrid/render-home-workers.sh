#!/usr/bin/env bash
# scripts/hybrid/render-home-workers.sh
# ─────────────────────────────────────────────────────────────────────────────
# Renders the home-workers JSON annotation value from home-lab.env.
# Useful to update the SpokePool claim annotation without hand-editing JSON.
#
# Usage:
#   ./render-home-workers.sh [--env ./home-lab.env]
#   Output: JSON array suitable for the home-workers annotation.
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

ENV_FILE="${1:-$(dirname "$0")/home-lab.env}"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: $ENV_FILE not found." >&2
  exit 1
fi
# shellcheck source=/dev/null
source "$ENV_FILE"

# Build JSON array from HOME_WORKER_NODES
JSON="["
FIRST=true
while IFS='|' read -r HOSTNAME SSH_TARGET WSL_DISTRO TAILNET_HOST BOX_TAG; do
  [[ -z "$HOSTNAME" ]] && continue
  if [[ "$FIRST" == "true" ]]; then
    FIRST=false
  else
    JSON="${JSON},"
  fi
  JSON="${JSON}{\"hostname\":\"${HOSTNAME}\",\"tailnetHost\":\"${TAILNET_HOST}\"}"
done <<< "$HOME_WORKER_NODES"
JSON="${JSON}]"

echo "$JSON"
echo ""
echo "# Paste into SpokePool annotation:"
echo "# home-workers: '${JSON}'"
