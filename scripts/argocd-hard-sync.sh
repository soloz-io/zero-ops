#!/usr/bin/env bash
# Force an ArgoCD Application to re-read its source and sync -- correctly.
#
#   scripts/argocd-hard-sync.sh <app> [<app>...]         refresh + sync + wait
#   scripts/argocd-hard-sync.sh --refresh-only <app>...  refresh only
#   scripts/argocd-hard-sync.sh --wait <app>...          just report/wait
#   scripts/argocd-hard-sync.sh --promote <version>      a whole promotion, in order
#
#   KUBECONFIG=k8-secrets/kubeconfig/nutgraf-hub.kubeconfig \
#     scripts/argocd-hard-sync.sh --promote 0.1.16-rc.104
#
# WHY A SCRIPT INSTEAD OF A REMEMBERED kubectl PATCH
#
#   The patch carries a trap that is invisible until it fires, and a command typed
#   from memory hits it every time.
#
#   A hand-triggered `operation` DOES NOT INHERIT spec.syncPolicy.syncOptions.
#   ArgoCD applies the options written INTO the operation and nothing else, so a
#   patch that omits them silently downgrades the sync to CLIENT-SIDE apply. On a
#   large CRD that fails as:
#
#     metadata.annotations: Too long: must have at most 262144 bytes
#
#   which reads as a broken manifest rather than a malformed sync request. These
#   Applications declare ServerSideApply=true precisely so normal reconciliation
#   never hits it; a manual sync that forgets it reintroduces the failure by hand.
#
#   syncStrategy.apply.force produces the SAME failure -- force implies a
#   client-side replace. It is not the escape hatch it looks like.
#
#   So this reads each Application's OWN syncOptions and mirrors them, making a
#   manual sync behave exactly like the automated one.
#
# WHAT A HARD REFRESH IS AND IS NOT FOR
#
#   argocd.argoproj.io/refresh=hard drops ArgoCD's cached manifests and re-fetches
#   the source. Reach for it when the SOURCE changed in a way the repo-server would
#   not otherwise notice:
#
#     * a newly published OCI chart, after targetRevision moved -- OCI content is
#       cached, and a soft refresh can keep serving the old chart, which makes a
#       correct promotion look like it did nothing
#     * an Application stuck Synced against a revision that no longer matches
#     * a ComparisonError whose upstream cause has since been fixed
#
#   It does NOT fix:
#
#     * a Degraded resource. Refreshing re-reads the source; it does not repair a
#       workload. Read that resource's own condition first.
#     * anything another controller owns. An ExternalSecret that will not sync, or
#       a SecretStore reporting InvalidProviderConfig, is ESO's business -- ArgoCD
#       will report the object Synced while it fails.
#
# PROMOTION ORDER, WHICH IS THE PART THAT IS EASY TO GET WRONG
#
#   The version lives in the tenant's gitops repo (registry/clusters/*/bundle.yaml
#   and values.yaml). The hub's ROOT Application reads that repo and regenerates the
#   ApplicationSets, and that is what rewrites every child's targetRevision:
#
#     root re-reads gitops  ->  children regenerate at the new version
#                           ->  children re-fetch the new OCI chart
#
#   Refreshing a child first does nothing useful -- it re-fetches the chart it is
#   still pinned to. --promote walks this in order and waits at each step.
set -euo pipefail

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/tool-path.sh"
require_tools kubectl || exit 1

ARGOCD_NS="${ARGOCD_NS:-platform-ops}"
ROOT_APP="${ROOT_APP:-nutgraf-hub-root}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-300}"
# How long the root may take to rewrite a child's targetRevision after it syncs.
# Shorter than WAIT_TIMEOUT: this is one controller writing one field, not a
# workload converging, and a child that has not been touched in this long has not
# been regenerated at all.
REGEN_TIMEOUT="${REGEN_TIMEOUT:-120}"


MODE="sync"
case "${1:-}" in
  --refresh-only) MODE="refresh"; shift ;;
  --promote)      MODE="promote"; shift ;;
  --wait)         MODE="wait";    shift ;;
  -h|--help|"")   sed -n '2,10p' "$0" | sed 's/^# //'; exit 0 ;;
esac

kc()         { kubectl -n "$ARGOCD_NS" "$@"; }
app_exists() { kc get application "$1" >/dev/null 2>&1; }
app_field()  { kc get application "$1" -o jsonpath="{$2}" 2>/dev/null; }

hard_refresh() {
  kc annotate application "$1" argocd.argoproj.io/refresh=hard --overwrite >/dev/null
  echo "   hard refresh requested"
}

# Mirror the Application's own syncOptions; never assume them. They differ between
# boundaries, and an operation carrying the wrong ones fails in a way that does
# not name its cause.
sync_options_for() {
  local app="$1" opts
  opts="$(app_field "$app" .spec.syncPolicy.syncOptions)"
  if [ -z "$opts" ] || [ "$opts" = "null" ]; then
    echo '["ServerSideApply=true"]'
    return
  fi
  case "$opts" in
    *ServerSideApply=true*) echo "$opts" ;;
    *) printf '%s' "$opts" | sed 's/\]$/,"ServerSideApply=true"]/' ;;
  esac
}

trigger_sync() {
  local app="$1" opts rev payload
  opts="$(sync_options_for "$app")"
  rev="$(app_field "$app" .spec.source.targetRevision)"
  echo "   syncOptions: ${opts}"
  # No syncStrategy: apply.force implies a client-side replace and reintroduces
  # exactly the failure this script exists to avoid.
  payload=$(printf '{"operation":{"initiatedBy":{"username":"argocd-hard-sync.sh"},"sync":{"revision":"%s","syncOptions":%s}}}' "$rev" "$opts")
  kc patch application "$app" --type=merge -p "$payload" >/dev/null
  echo "   sync requested at revision ${rev:-<none>}"
}

wait_for_regeneration() {
  # Wait for the ROOT to rewrite one child's targetRevision to the new version.
  #
  # Regeneration is ASYNCHRONOUS. The root reaching Synced means it applied the
  # generator; the generated Applications are then rewritten by the
  # ApplicationSet controller a moment later, one at a time. Sampling a child
  # once, immediately, reads whichever ones have not been reached yet -- which is
  # alphabetical luck, not a fault. A promotion of rc.119 -> rc.120 reported
  # twelve children "not regenerated" while platform-appproject-infrastructure,
  # further down the same list, already showed the new version.
  #
  # The protection this must NOT lose: when only environmentRevision was bumped,
  # children were never regenerated AT ALL, and a promotion shipped nothing while
  # exiting 0. That case still fails here -- it simply fails after the timeout
  # rather than instantly, which is the price of telling the two apart.
  local app="$1" want="$2" deadline cur
  deadline=$(( $(date +%s) + REGEN_TIMEOUT ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    cur="$(app_field "$app" .spec.source.targetRevision)"
    [ "$cur" = "$want" ] && return 0
    sleep 5
  done
  return 1
}

wait_for() {
  local app="$1" want="${2:-}" deadline rev sync health
  deadline=$(( $(date +%s) + WAIT_TIMEOUT ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    rev="$(app_field "$app" .spec.source.targetRevision)"
    sync="$(app_field "$app" .status.sync.status)"
    health="$(app_field "$app" .status.health.status)"
    if [ -n "$want" ] && [ "$rev" != "$want" ]; then sleep 5; continue; fi
    if [ "$sync" = "Synced" ]; then
      echo "   ${app}: rev=${rev} sync=${sync} health=${health}"
      # Healthy is REPORTED, not required: a Degraded child is a real finding and
      # must not be hidden behind this timeout.
      [ "$health" = "Healthy" ] || echo "   WARN health=${health} -- read its conditions; a resync will not change it"
      return 0
    fi
    # A ComparisonError means ArgoCD cannot even diff this Application -- a stuck
    # admission webhook, an unrenderable source. Waiting the rest of the timeout
    # cannot change that, so stop now and say which it is instead of reporting a
    # generic TIMEOUT five minutes later.
    if [ "$sync" = "Unknown" ] && app_field "$app" .status.conditions 2>/dev/null | grep -q ComparisonError; then
      echo "   COMPARISON ERROR ${app}: ArgoCD cannot diff it (sync=Unknown) -- waiting will not help"
      return 1
    fi
    sleep 5
  done
  echo "   TIMEOUT ${app} not Synced in ${WAIT_TIMEOUT}s (rev=${rev:-?} sync=${sync:-?} health=${health:-?})"
  return 1
}

# summarise prints the ledger and decides the exit code.
#
# Loud on failure and silent-ish on success, because the useful question after a
# 20-minute promotion is "which ones failed", and the answer must not require
# scrolling. A non-empty ledger exits non-zero -- this script is run by a human
# who then says "it worked", so it has to be the thing that knows.
summarise() {
  echo
  if [ ${#FAILURES[@]} -eq 0 ]; then
    echo "OK  every Application reached Synced."
    echo "Now verify what actually SHIPPED, not just that ArgoCD is green."
    return 0
  fi
  echo "FAILED  ${#FAILURES[@]} Application(s) did not converge:"
  for f in "${FAILURES[@]}"; do echo "  - ${f}"; done
  echo
  echo "This is a FAILED promotion. Nothing below this line shipped reliably --"
  echo "do not treat the box as running the new version until these are resolved."
  return 1
}

conditions_of() {
  local c
  c="$(app_field "$1" .status.conditions)"
  if [ -n "$c" ] && [ "$c" != "null" ]; then echo "   conditions: $c"; fi
}

rc=0
# Every failure, collected rather than only counted.
#
# The per-app output of a promotion is hundreds of lines, so a failure that only
# set an exit code scrolled past and the run looked fine. FAILURES is printed as
# a block at the end and the script exits non-zero -- one place to read, one
# answer to "did that work".
FAILURES=()
fail() { FAILURES+=("$1"); rc=1; }

if [ "$MODE" = "promote" ]; then
  VERSION="${1:?usage: $0 --promote <version>}"
  echo "Promoting to ${VERSION}"
  echo
  echo "-- ${ROOT_APP} (reads the gitops repo)"
  app_exists "$ROOT_APP" || { echo "   ERROR no Application ${ROOT_APP} in ${ARGOCD_NS}"; exit 1; }
  hard_refresh "$ROOT_APP"
  trigger_sync "$ROOT_APP"
  wait_for "$ROOT_APP" || fail "${ROOT_APP}: root did not reach Synced -- children cannot regenerate"
  conditions_of "$ROOT_APP"
  echo
  echo "-- children, once the root has regenerated them at ${VERSION}"
  # Discovered, not hardcoded: which Applications exist is a function of the
  # registry, and a fixed list would quietly skip a newly registered cluster.
  for app in $(kc get applications -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null); do
    [ "$app" = "$ROOT_APP" ] && continue
    cur="$(app_field "$app" .spec.source.targetRevision)"
    # Only version-pinned children are ours to promote; one tracking a branch is not.
    case "$cur" in *rc.*) ;; *) continue ;; esac
    if [ "$cur" != "$VERSION" ]; then
      echo "   ${app}: still ${cur:-<none>} -- waiting for the root to regenerate it"
      if ! wait_for_regeneration "$app" "$VERSION"; then
        # Waited and it never moved. THIS is the rc.111/rc.112 failure: the
        # version was bumped in one file, the root synced against it, and the
        # children were never rewritten -- a promotion that ships nothing while
        # every Application reports Synced at the previous release.
        cur="$(app_field "$app" .spec.source.targetRevision)"
        echo "   NOT REGENERATED ${app}: still ${cur:-<none>} after ${REGEN_TIMEOUT}s"
        fail "${app}: not regenerated -- still ${cur:-<none>}, wanted ${VERSION}"
        continue
      fi
      echo "   ${app}: regenerated at ${VERSION}"
    fi
    echo "-- ${app}"
    hard_refresh "$app"
    trigger_sync "$app"
    wait_for "$app" "$VERSION" || fail "${app}: did not reach Synced at ${VERSION}"
    conditions_of "$app"
  done
  summarise || rc=1
  exit $rc
fi

if [ $# -lt 1 ]; then
  echo "usage: $0 [--refresh-only|--wait|--promote <version>] <app>..." >&2
  exit 1
fi

for app in "$@"; do
  echo "-- ${app}"
  if ! app_exists "$app"; then
    echo "   ERROR no Application ${app} in namespace ${ARGOCD_NS}"
    fail "${app}: no such Application in ${ARGOCD_NS}"; continue
  fi
  case "$MODE" in
    refresh) hard_refresh "$app" ;;
    wait)    wait_for "$app" || fail "${app}: did not reach Synced"; conditions_of "$app" ;;
    sync)    hard_refresh "$app"; trigger_sync "$app"; wait_for "$app" || fail "${app}: did not reach Synced"; conditions_of "$app" ;;
  esac
done

summarise || rc=1
exit $rc
