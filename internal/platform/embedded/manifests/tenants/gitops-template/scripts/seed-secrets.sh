#!/usr/bin/env bash
# Supply this fleet's secrets for the first time, from a local .env file.
#
#   ./scripts/seed-secrets.sh <environment> [--confirm]
#
# WHEN YOU RUN THIS
#
# Once, when a fleet's secrets are first supplied -- or when migrating values
# that currently live in a seed file. Not per application, and NOT after a
# rebuild: your secret store's data is backed up outside the box and the keys
# that decrypt it are escrowed, so a rebuilt cluster restores every secret
# rather than asking for them again. Re-running this after a rebuild means the
# escrow or the backup is broken, and that is the thing to fix.
#
# For one secret, or to rotate one, use `soloz fleet secrets set` instead. It
# prompts with echo disabled and touches no file at all, which is the better
# path whenever you are not handling a whole set at once.
#
# WHAT IT DOES
#
#   1. Generates environments/<env>/.env holding EXACTLY the keys this fleet
#      declares, if that file does not exist yet.
#   2. Shows which of its keys would be written, and writes nothing.
#   3. With --confirm, writes them.
#
# WHAT IT DOES NOT DO
#
# Feed the whole file. Only keys declared under `secrets:` in
# environments/<env>/values.yaml are written. A seed file usually carries
# configuration and connection settings alongside the credentials -- those
# belong in `config:` in that same values.yaml, in git, where they can be
# reviewed. Writing them here would put configuration back in a secret store.
#
# The filtering is not implemented here. This script calls the same
# `soloz fleet secrets` that `status` and `set` use, so there is ONE answer to
# "which keys does this fleet declare" -- a second implementation in a script
# is a second answer, and the two disagree the moment either changes.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

usage() {
  cat >&2 <<EOF
Usage: $(basename "$0") <environment> [--confirm]

  environment   names environments/<env>/ in this repository
  --confirm     actually write; without it, nothing is written

Requires: soloz on PATH, and KUBECONFIG pointing at this box's hub.
EOF
  exit 1
}

[[ $# -lt 1 || $# -gt 2 ]] && usage
ENVIRONMENT="$1"
CONFIRM=""
if [[ $# -eq 2 ]]; then
  [[ "$2" == "--confirm" ]] || usage
  CONFIRM="--confirm"
fi

FLEET_DIR="${REPO_ROOT}/environments/${ENVIRONMENT}"
ENV_FILE="${FLEET_DIR}/.env"
# What to call the file when talking to a person: they are standing in the
# repository root, not holding an absolute path.
ENV_FILE_REL="environments/${ENVIRONMENT}/.env"

[[ -d "$FLEET_DIR" ]] || {
  echo "No fleet at environments/${ENVIRONMENT}/." >&2
  echo "The environment names a directory in this repository." >&2
  exit 1
}
command -v soloz >/dev/null || {
  echo "soloz is not on PATH. It is the same binary that scaffolded this" >&2
  echo "repository, and it holds the credential for this box's secret store." >&2
  exit 1
}
# Plain test rather than ${KUBECONFIG:?...}: inside that expansion bash parses
# an apostrophe in the message as an opening quote, even within double quotes,
# and the script fails to parse rather than printing the message.
if [[ -z "${KUBECONFIG:-}" ]]; then
  echo "KUBECONFIG is not set. It must point at this box's hub: the credential" >&2
  echo "for the secret store is held in the cluster, not on this machine." >&2
  exit 1
fi

# Generated, not hand-written. Transcribing the declaration into a file by hand
# is where a typo becomes a key the import silently skips -- an undeclared key
# and a key you chose not to supply look identical to it.
if [[ ! -f "$ENV_FILE" ]]; then
  echo "Creating ${ENV_FILE_REL} from what this fleet declares..."
  soloz fleet secrets template "$ENVIRONMENT" --repo "$REPO_ROOT" > "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  echo ""
  echo "Fill in the values in that file, then run this again."
  echo "It is gitignored; it must never be committed."
  exit 0
fi

soloz fleet secrets import "$ENVIRONMENT" \
  --from-env "$ENV_FILE" \
  --repo "$REPO_ROOT" \
  --kubeconfig "$KUBECONFIG" \
  $CONFIRM

if [[ -n "$CONFIRM" ]]; then
  echo ""
  echo "Verify, and see anything still outstanding:"
  echo "  soloz fleet secrets status ${ENVIRONMENT} --repo . --kubeconfig \$KUBECONFIG"
  echo ""
  echo "Then delete ${ENV_FILE_REL} -- its values are now held where the"
  echo "fleet's declaration says they are, and a copy here is one nothing rotates."
fi
