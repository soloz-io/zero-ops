#!/usr/bin/env bash
# scripts/cnpg-bump-incarnation.sh
# ─────────────────────────────────────────────────────────────────────────────
# Move a spoke's CNPG cluster between archive INCARNATIONS, deterministically.
#
# WHY THIS EXISTS
#
#   CloudNativePG has no in-place recovery. Restoring a physical backup IS the
#   bootstrap of a NEW cluster, and bootstrap is read exactly once, at creation,
#   then ignored forever. So "restore the database" is a manifest transition
#   followed by a delete/recreate, not a command run against a live cluster.
#
#   That transition touches two fields that must agree and are in different
#   parts of the file:
#
#       bootstrap.recovery.source        the incarnation recovered FROM
#       backup.barmanObjectStore.serverName   the incarnation archived TO
#
#   They must NEVER be equal: a cluster whose archive target is also its
#   recovery source is recovering from itself. And the archive name must change
#   on every rebuild, because barman refuses to write into a serverName that
#   already holds another cluster's WALs:
#
#       WAL archive check failed for server shared-cnpg-v2: Expected empty archive
#
#   which leaves ContinuousArchiving=False, blocks every base backup behind it,
#   and does all of that while the Cluster still reports "healthy". The repo
#   carried a comment saying BUMP THE SUFFIX. It was honoured once, in September
#   2026, and missed on the rebuild after it. A comment is not a mechanism.
#
# WHY NOT A PERMANENT recovery STANZA
#
#   destinationPath is rewritten per-spoke, so a brand-new spoke has no archive
#   under ANY serverName. A provider manifest that always declares
#   bootstrap.recovery would make every first-ever provisioning fail to
#   bootstrap at all -- trading a silent empty database for a loud broken one.
#   initdb is therefore the steady state, and recovery is a deliberate,
#   validated, temporary transition.
#
# THE TWO MODES
#
#   recover   initdb @ vN                 ->  recovery <- vN, archive v(N+1)
#   settle    recovery <- vN, archive vM  ->  initdb @ vM
#
#   `settle` is not optional bookkeeping. Left in recovery, the NEXT rebuild of
#   that spoke restores vN again and silently discards everything written since
#   the restore. Run it once the recovered cluster is healthy.
#
# IT NEVER PARTIALLY WRITES
#
#   Preconditions are checked against the parsed manifest first; the transform
#   is applied to a temporary copy; the result is re-parsed and the invariant
#   re-asserted; only then does the file move into place. Any failure leaves the
#   working tree byte-for-byte untouched, which is what makes it safe in CI.
#
# USAGE
#
#   scripts/cnpg-bump-incarnation.sh --provider hybrid --mode recover [--dry-run]
#   scripts/cnpg-bump-incarnation.sh --provider hybrid --mode settle
#
#   or, preferred:
#
#   make cnpg-bump-incarnation PROVIDER=hybrid
#   make cnpg-settle-incarnation PROVIDER=hybrid
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/.." && pwd)"

PROVIDER=""
MODE=""
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --provider) PROVIDER="${2:-}"; shift 2 ;;
    --mode)     MODE="${2:-}"; shift 2 ;;
    --dry-run)  DRY_RUN=true; shift ;;
    -h|--help)  sed -n '2,70p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "ERROR: unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [[ -z "$PROVIDER" || -z "$MODE" ]]; then
  echo "ERROR: --provider and --mode are both required." >&2
  echo "       scripts/cnpg-bump-incarnation.sh --provider <hybrid|hetzner> --mode <recover|settle>" >&2
  exit 2
fi

case "$PROVIDER" in
  hybrid|hetzner) ;;
  *) echo "ERROR: --provider must be 'hybrid' or 'hetzner', got '$PROVIDER'." >&2; exit 2 ;;
esac

case "$MODE" in
  recover|settle) ;;
  *) echo "ERROR: --mode must be 'recover' or 'settle', got '$MODE'." >&2; exit 2 ;;
esac

MANIFEST="${REPO_ROOT}/manifests/spoke/spoke-catalog/providers/${PROVIDER}/cnpg-cluster.yaml"
if [[ ! -f "$MANIFEST" ]]; then
  echo "ERROR: manifest not found: $MANIFEST" >&2
  exit 1
fi

# ruamel, not PyYAML: this file is mostly comments, and several of them record
# incidents that cost days to diagnose. PyYAML round-trips the data and discards
# every one of them, which would make the transform destructive in a way that no
# test would catch and no reviewer would enjoy discovering.
if ! python3 -c "import ruamel.yaml" >/dev/null 2>&1; then
  echo "ERROR: python3 module 'ruamel.yaml' is required (it preserves comments; PyYAML does not)." >&2
  echo "       pip install ruamel.yaml" >&2
  exit 1
fi

TMP="$(mktemp "${TMPDIR:-/tmp}/cnpg-incarnation.XXXXXX.yaml")"
trap 'rm -f "$TMP"' EXIT

MANIFEST="$MANIFEST" MODE="$MODE" PROVIDER="$PROVIDER" TMP="$TMP" python3 <<'PY'
import os, re, sys, copy
from ruamel.yaml import YAML

manifest = os.environ["MANIFEST"]
mode     = os.environ["MODE"]
provider = os.environ["PROVIDER"]
tmp      = os.environ["TMP"]

yaml = YAML()
yaml.preserve_quotes = True
# The manifests open with a `---`; without this ruamel drops it and every file
# this script touches picks up an unrelated one-line diff.
yaml.explicit_start = True
# The manifests are indented with two spaces and sequences are indented under
# their key; matching that keeps the diff to the lines actually changed instead
# of reflowing the whole file.
yaml.indent(mapping=2, sequence=4, offset=2)
yaml.width = 4096

def die(msg):
    sys.exit(f"ERROR: {msg}")

with open(manifest) as fh:
    docs = list(yaml.load_all(fh))

clusters = [(i, d) for i, d in enumerate(docs)
            if isinstance(d, dict) and d.get("kind") == "Cluster"]
if len(clusters) != 1:
    die(f"expected exactly 1 Cluster document in {manifest}, found {len(clusters)}")
idx, cluster = clusters[0]
spec = cluster["spec"]

# ── Read the current incarnation ─────────────────────────────────────────────
try:
    bos = spec["backup"]["barmanObjectStore"]
except (KeyError, TypeError):
    die("spec.backup.barmanObjectStore is missing — this is not a manifest this script understands")

server = bos.get("serverName")
if not isinstance(server, str):
    die("spec.backup.barmanObjectStore.serverName is missing or not a string")

m = re.fullmatch(r"(?P<stem>.+)-v(?P<n>\d+)", server)
if not m:
    die(f"serverName {server!r} is not of the form <name>-v<N>; refusing to guess the next incarnation")
stem = m.group("stem")
n    = int(m.group("n"))

def inc(k):
    return f"{stem}-v{k}"

bootstrap = spec.get("bootstrap")
if not isinstance(bootstrap, dict) or len(bootstrap) != 1:
    die("spec.bootstrap must hold exactly one method (CNPG rejects more than one)")
current_method = next(iter(bootstrap))

# The canonical initdb block. Hardcoded on purpose: `settle` has to restore a
# block that `recover` removed, and a script that silently reconstructs a
# DIFFERENT initdb than the chart declares would quietly change what a greenfield
# spoke bootstraps. Compared field-by-field on the way out, so a deliberate chart
# change fails here and is updated here, rather than being lost.
CANONICAL_INITDB = {
    "database": "app",
    "owner": "app",
    "postInitSQL": [
        "CREATE EXTENSION IF NOT EXISTS vector;",
        "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;",
        "ALTER ROLE app WITH CREATEROLE;",
    ],
}

def external_store_for(name):
    """A READ-ONLY view of a previous incarnation.

    Deliberately not a copy of the live backup block: `data` and
    `retentionPolicy` describe how this cluster WRITES backups, and carrying them
    onto a recovery source invites someone to believe the source is still being
    maintained. Only what barman needs to READ is declared.
    """
    src = {
        "destinationPath": bos["destinationPath"],
        "serverName": name,
        "endpointURL": bos["endpointURL"],
        "s3Credentials": copy.deepcopy(bos["s3Credentials"]),
    }
    if "wal" in bos:
        src["wal"] = copy.deepcopy(bos["wal"])
    return {"name": name, "barmanObjectStore": src}

# ── Transform ────────────────────────────────────────────────────────────────
# externalClusters is present in EVERY state, including the steady one where
# bootstrap is initdb and nothing reads it.
#
# Not cosmetic. The per-spoke prefix is applied by a JSON-Patch in the
# platform-services ApplicationSet, and a JSON-Patch op against an absent path
# fails the ENTIRE kustomization — so an externalClusters that only existed
# during a recovery would take every spoke's sync down in between recoveries.
# Keeping one inert entry keeps that patch target always present.
#
# The invariant differs by state, and both are asserted after the write:
#
#   steady      externalClusters == archive        (inert; nothing reads it)
#   recovering  externalClusters == archive - 1    (and recovery.source == it)

def set_external(source_name):
    """Declare exactly one externalClusters entry, placed next to bootstrap.

    Next to bootstrap rather than appended at the end of spec: the two are one
    decision — what this cluster would recover from — and a reader who finds
    `recovery.source` should not have to scroll past the whole spec to resolve it.
    """
    entry = [external_store_for(source_name)]
    if "externalClusters" in spec:
        spec["externalClusters"] = entry
        return
    keys = list(spec.keys())
    pos = keys.index("bootstrap") + 1 if "bootstrap" in keys else len(keys)
    spec.insert(pos, "externalClusters", entry)

if mode == "recover":
    if current_method != "initdb":
        die(f"--mode recover expects spec.bootstrap.initdb, found spec.bootstrap.{current_method}.\n"
            f"       {provider} is already mid-recovery. Finish it with --mode settle first.")

    got = {k: bootstrap["initdb"].get(k) for k in CANONICAL_INITDB}
    if got != CANONICAL_INITDB:
        die("spec.bootstrap.initdb does not match the canonical block this script knows how to\n"
            "       restore on --mode settle. Update CANONICAL_INITDB in this script to match the\n"
            "       chart, deliberately, rather than letting settle rewrite it to something else.")

    ext = spec.get("externalClusters") or []
    if len(ext) != 1 or ext[0].get("name") != server:
        die(f"steady state expects exactly one externalClusters entry naming the current archive\n"
            f"       ({server!r}); found {[e.get('name') for e in ext]!r}.\n"
            f"       Normalise first with --mode settle, which is idempotent and seeds it.")

    source, archive = inc(n), inc(n + 1)
    spec["bootstrap"] = {"recovery": {"source": source}}
    set_external(source)          # unchanged in value; recovery now reads it
    bos["serverName"] = archive
    summary = f"recover: bootstrap.recovery <- {source}   archive -> {archive}"

else:  # settle
    # Deliberately idempotent and accepted from ANY state, because it is also
    # what seeds externalClusters onto a provider that has never recovered. A
    # settle that insisted on finding a recovery in progress could not do that,
    # and the seeding would fall back to a hand edit — the class of change this
    # script exists to remove.
    spec["bootstrap"] = {"initdb": copy.deepcopy(CANONICAL_INITDB)}
    set_external(server)
    was = f"released {bootstrap['recovery'].get('source')!r}; " if current_method == "recovery" else ""
    summary = f"settle: bootstrap.initdb   archive stays {server}   ({was}externalClusters -> {server})"

# ── Write to a temporary file, then re-read and re-assert ────────────────────
docs[idx] = cluster
with open(tmp, "w") as fh:
    yaml.dump_all(docs, fh)

with open(tmp) as fh:
    rt = [d for d in yaml.load_all(fh) if isinstance(d, dict) and d.get("kind") == "Cluster"]
if len(rt) != 1:
    die("post-transform manifest no longer contains exactly one Cluster")
rs = rt[0]["spec"]
rb = rs["bootstrap"]
ra = rs["backup"]["barmanObjectStore"]["serverName"]

if mode == "recover":
    if "recovery" not in rb:
        die("post-transform: bootstrap.recovery is absent")
    rsrc = rb["recovery"]["source"]
    ext  = rs.get("externalClusters") or []
    if len(ext) != 1:
        die(f"post-transform: expected exactly 1 externalClusters entry, found {len(ext)}")
    if not (rsrc == ext[0]["name"] == ext[0]["barmanObjectStore"]["serverName"]):
        die("post-transform: recovery.source, externalClusters[0].name and its serverName disagree")
    if rsrc == ra:
        die(f"post-transform: recovery source equals archive target ({ra}) — the cluster would recover from itself")
else:
    if "initdb" not in rb:
        die("post-transform: bootstrap.initdb is absent")
    ext = rs.get("externalClusters") or []
    if len(ext) != 1:
        die(f"post-transform: expected exactly 1 externalClusters entry, found {len(ext)}")
    if not (ext[0]["name"] == ext[0]["barmanObjectStore"]["serverName"] == ra):
        die("post-transform: settled externalClusters does not name the current archive — the\n"
            "       next recover would read the wrong incarnation")

print(summary)
PY

if [[ "$DRY_RUN" == true ]]; then
  echo ""
  echo "── dry run · diff that WOULD be applied ──"
  diff -u "$MANIFEST" "$TMP" || true
  echo ""
  echo "(dry run — $MANIFEST is unchanged)"
  exit 0
fi

cp "$TMP" "$MANIFEST"
echo ""
echo "── applied to $MANIFEST ──"
git -C "$REPO_ROOT" --no-pager diff --stat -- "$MANIFEST" 2>/dev/null || true

if [[ "$MODE" == "recover" ]]; then
  cat <<'NEXT'

NEXT, and none of it is optional:
  1. review the diff, commit, and ship it in a release
  2. once ArgoCD has synced that release to the spoke, DELETE the Cluster —
     bootstrap is read only at creation, so nothing restores until it is
     recreated
  3. when the recovered cluster reports healthy AND ContinuousArchiving=True,
     run --mode settle and ship that too

Leaving it in recovery means the NEXT rebuild restores the same old incarnation
and silently discards everything written since this restore.
NEXT
fi
