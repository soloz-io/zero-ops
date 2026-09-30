#!/usr/bin/env bash
# Publish the next bundle and promote a tenant to it, in one ordered operation.
#
#   scripts/release.sh                       next rc after the tenant's current
#   scripts/release.sh --version 0.1.17-rc.1 an explicit version
#   scripts/release.sh --dry-run             say what it would do, change nothing
#   scripts/release.sh --no-promote          publish only
#   scripts/release.sh --sync                promote, then hard-sync the box
#
# WHY ONE SCRIPT. Publishing and promoting are two halves of one intent, and
# doing them by hand has failed in three distinct ways that this sequence
# removes:
#
#   the version was bumped in values.yaml only, so the root Application synced
#   and every CHILD stayed pinned to the previous release -- reporting Synced
#   while shipping nothing (rc.111, rc.112);
#
#   the promotion commit was written against a stale checkout, so the push was
#   rejected and a later force would have discarded a CI bot's own commit;
#
#   .state/*.json was staged along with the version files, committing local
#   machine state into a tenant's repository.
#
# The ordering is deliberate and is the point: PULL, then compute the version,
# then publish, then promote. Computing the version before pulling reads a stale
# current version and can reuse one already published. Promoting before
# publishing writes a reference to an artefact that does not exist, which ArgoCD
# reports as a chart pull failure naming a version nobody can find.
set -euo pipefail

GITOPS_DIR="${GITOPS_DIR:-.local-e2e/nutgraf-gitops}"
OWNER="${OWNER:-soloz-io}"
VERSION=""
DRY_RUN=0
PROMOTE=1
SYNC=0

while [ $# -gt 0 ]; do
  case "$1" in
    --version)    VERSION="${2:?--version needs a value}"; shift 2 ;;
    --dry-run)    DRY_RUN=1; shift ;;
    --no-promote) PROMOTE=0; shift ;;
    --sync)       SYNC=1; shift ;;
    -h|--help)    sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)            echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

say()  { printf '\n\033[1m%s\033[0m\n' "$*"; }
note() { printf '   %s\n' "$*"; }
die()  { printf '\n\033[31mFAILED\033[0m %s\n' "$*" >&2; exit 1; }
run()  { if [ "$DRY_RUN" = 1 ]; then note "would run: $*"; else "$@"; fi; }

[ -d "$GITOPS_DIR/.git" ] || die "no tenant GitOps checkout at $GITOPS_DIR (set GITOPS_DIR)"

# ---------------------------------------------------------------------------
# 1. Pull the tenant repository FIRST.
#
# Before the version is computed, because the current version is read from this
# repository and a stale read picks a version that may already be published.
# Before any edit, because a rebase with local changes in the tree is a conflict
# resolved under time pressure -- and the only safe resolution of a conflict on
# a version file is to start again.
# ---------------------------------------------------------------------------
say "1. Pull the tenant repository"
cd "$GITOPS_DIR"
git diff --quiet -- registry/ environments/ || die "uncommitted changes under registry/ or environments/ in $GITOPS_DIR
   This script rewrites version references. Commit or discard them first;
   it will not rebase on top of work it did not make."

if [ "$DRY_RUN" = 1 ]; then
  note "would: git pull --rebase"
else
  # Autostash covers .state/*.json, which this box writes and never commits.
  git -c rebase.autoStash=true pull --rebase -q || die "could not update $GITOPS_DIR -- resolve it there, then re-run"
fi
note "at $(git log --oneline -1)"

# ---------------------------------------------------------------------------
# 2. Compute the next version FROM THE TENANT, not from a tag or a file here.
#
# The tenant's environmentRevision is what is actually running, and it is the
# only source that cannot disagree with the box. A version derived from a git
# tag, or from the highest published chart, answers "what exists" rather than
# "what comes next for this box" -- and those differ the moment a publish
# succeeds and its promotion does not, which is a state this script can leave
# behind on a failure and must be able to resume from.
# ---------------------------------------------------------------------------
say "2. Decide the version"
# Every cluster's environmentRevision, de-duplicated.
#
# `grep -m1` over several files matches once PER FILE, not once in total, so the
# naive read returns one line per cluster and the "current version" becomes two
# versions joined by a newline. Sorted unique instead, and a disagreement is a
# refusal rather than a guess: clusters on different versions is a real state --
# a promotion that half-applied -- and picking either one silently completes it
# in a direction nobody chose.
CURRENT="$(awk '/^environmentRevision:/ {print $2}' registry/clusters/*/values.yaml 2>/dev/null | sort -u)"
[ -n "$CURRENT" ] || die "no environmentRevision found under $GITOPS_DIR/registry/clusters/*/values.yaml"
if [ "$(printf '%s\n' "$CURRENT" | wc -l | tr -d ' ')" != "1" ]; then
  die "clusters are on different versions, so there is no single current version:
$(grep -H '^environmentRevision:' registry/clusters/*/values.yaml)
   Bring them to one version, or pass --version to drive them all to a new one."
fi
note "current: $CURRENT"

if [ -z "$VERSION" ]; then
  case "$CURRENT" in
    *-rc.*)
      base="${CURRENT%-rc.*}"
      n="${CURRENT##*-rc.}"
      case "$n" in
        ''|*[!0-9]*) die "cannot increment $CURRENT: the text after -rc. is not a number" ;;
      esac
      VERSION="${base}-rc.$((n + 1))"
      ;;
    *) die "cannot increment $CURRENT automatically: it is not an -rc.N version. Pass --version." ;;
  esac
fi
note "next:    $VERSION"

# Refuse to reuse a version. A republished tag with different content is the one
# failure GitOps cannot diagnose: every reference still resolves, and the box
# runs something other than what the reference names.
EVERY_REF="$(grep -rn -- "$VERSION" registry/ --include='*.yaml' 2>/dev/null || true)"
[ -z "$EVERY_REF" ] || die "$VERSION is already referenced in $GITOPS_DIR/registry/ -- a version is published once
$EVERY_REF"

cd - >/dev/null

# ---------------------------------------------------------------------------
# 3. Publish. The existing script, unchanged -- one packaging path, one gate set.
# ---------------------------------------------------------------------------
say "3. Publish $VERSION"
run make publish-local VERSION="$VERSION" OWNER="$OWNER" \
  || die "publish failed -- nothing has been promoted, so the box is untouched"

if [ "$PROMOTE" = 0 ]; then
  say "Published $VERSION. Not promoted (--no-promote)."
  exit 0
fi

# ---------------------------------------------------------------------------
# 4. Promote EVERY reference, and verify none of the old one survives.
#
# Not just environmentRevision. A bundle.yaml carries the version twice -- once
# as the root Application's targetRevision and once as the value the generated
# child Applications are rendered with -- and moving only the first leaves the
# root Synced while every child stays on the previous release. That is not a
# theoretical ordering problem: it shipped twice.
# ---------------------------------------------------------------------------
say "4. Promote $CURRENT -> $VERSION"
cd "$GITOPS_DIR"
FILES="$(grep -rl -- "$CURRENT" registry/ --include='*.yaml' 2>/dev/null || true)"
[ -n "$FILES" ] || die "no file under registry/ references $CURRENT"
note "rewriting: $(echo "$FILES" | tr '\n' ' ')"

if [ "$DRY_RUN" = 1 ]; then
  note "would rewrite $(echo "$FILES" | wc -l | tr -d ' ') file(s) and push"
  cd - >/dev/null
  say "Dry run complete. Nothing published, nothing promoted."
  exit 0
fi

for f in $FILES; do
  # In place, both GNU and BSD sed.
  sed -i.bak "s|${CURRENT}|${VERSION}|g" "$f" && rm -f "$f.bak"
done

LEFT="$(grep -rn -- "$CURRENT" registry/ --include='*.yaml' 2>/dev/null || true)"
[ -z "$LEFT" ] || die "after rewriting, $CURRENT still appears -- promotion is incomplete and has NOT been pushed
$LEFT"
note "$(grep -rc -- "$VERSION" registry/ --include='*.yaml' 2>/dev/null | grep -v ':0$' | wc -l | tr -d ' ') file(s) now reference $VERSION"

# Staged BY NAME. `git add -A` here commits .state/*.json, which is this box's
# local bootstrap state and belongs to no tenant's repository.
git add $FILES
STAGED="$(git diff --cached --name-only)"
echo "$STAGED" | grep -q '^\.state/' && die "refusing to commit: .state/ is staged"

git commit -q -m "chore(promote): ${CURRENT} -> ${VERSION}

Every reference under registry/, not only environmentRevision: a bundle carries
the version twice, and moving one leaves the children pinned while the root
reports Synced.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"

# Rebase again before pushing. The publish above takes minutes, and a CI bot
# writing a chart bump in that window is ordinary -- the first push of this
# promotion was rejected for exactly that. Never force: the rejection is the
# other party's commit, and discarding it is the failure, not the rejection.
git -c rebase.autoStash=true pull --rebase -q || die "the remote moved and the rebase did not apply cleanly.
   $VERSION IS PUBLISHED. Resolve in $GITOPS_DIR and push; do not re-publish."
git push -q || die "push rejected. $VERSION IS PUBLISHED but not promoted -- resolve in $GITOPS_DIR and push"
note "pushed $(git log --oneline -1)"
cd - >/dev/null

say "Promoted to $VERSION"

if [ "$SYNC" = 1 ]; then
  say "5. Sync the box"
  [ -n "${KUBECONFIG:-}" ] || die "--sync needs KUBECONFIG set to the hub's kubeconfig"
  scripts/argocd-hard-sync.sh --promote "$VERSION" || die "the box did not converge on $VERSION"
else
  echo
  note "Next: KUBECONFIG=$GITOPS_DIR/k8-secrets/kubeconfig/<hub>.kubeconfig \\"
  note "        scripts/argocd-hard-sync.sh --promote $VERSION"
fi
