#!/usr/bin/env bash
# Generate the platform mixin's committed output (ADR-078 add.2 §2, add.3 §3-4).
#
#   scripts/observability/mixin/generate.sh           regenerate in place
#   scripts/observability/mixin/generate.sh --check   fail if the committed output
#                                                     differs from a regeneration
#
# The upstream mixin is pinned in jsonnetfile.json (a tag, never a branch); the
# tools are pinned below. Output:
#   manifests/hub-core-services/platform-dashboards/dashboard-*.yaml
#       one GrafanaDashboard per selected upstream dashboard, in the Platform folder
#   manifests/hub-core-services/platform-alerts/kubernetes-mixin-rules.yaml
#   manifests/spoke/spoke-catalog/infra/platform-alerts/kubernetes-mixin-rules.yaml
#       the recording rules those dashboards read, synced per cluster
#   scripts/observability/mixin/metrics-allowlist.json
#       every metric a shipped dashboard or PrometheusRule reads, by Grafana's
#       mimirtool (allowlist.py); metric-allowlist.py renders it into the agents
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
JB="github.com/jsonnet-bundler/jsonnet-bundler/cmd/jb@v0.6.0"
JSONNET="github.com/google/go-jsonnet/cmd/jsonnet@v0.21.0"
# mimirtool, pinned by its release asset's digest. linux/amd64: generation runs in CI.
MIMIRTOOL_URL="https://github.com/grafana/mimir/releases/download/mimir-3.2.3/mimirtool-linux-amd64"
MIMIRTOOL_SHA256="17a134ffb41e7953aa139e568e72c10629519593f9f93054fb1a75c1f456639d"
CHECK="${1:-}"

work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
(cd "$HERE" && go run "$JB" install)
mkdir -p "$work/dashboards"
go run "$JSONNET" -J "$HERE/vendor" -m "$work/dashboards" \
    -e '(import "'"$HERE"'/platform.libsonnet").grafanaDashboards' >/dev/null
go run "$JSONNET" -J "$HERE/vendor" \
    -e '(import "'"$HERE"'/platform.libsonnet").prometheusRules' > "$work/rules.json"

out="$work/out"
python3 "$HERE/render.py" "$work" "$out"

[[ "$(uname -s)-$(uname -m)" == "Linux-x86_64" ]] || { echo "generate: runs on linux/amd64 (CI); mimirtool is pinned for it" >&2; exit 1; }
curl -sSfL -o "$work/mimirtool" "$MIMIRTOOL_URL"
echo "$MIMIRTOOL_SHA256  $work/mimirtool" | sha256sum -c - >/dev/null
chmod +x "$work/mimirtool"
python3 "$HERE/allowlist.py" "$work/mimirtool" "$work" "$out/root" "$ROOT"

if [[ "$CHECK" == "--check" ]]; then
    status=0
    while IFS= read -r f; do
        rel="${f#"$out/root/"}"
        if ! cmp -s "$f" "$ROOT/$rel"; then
            echo "DRIFT $rel: the committed output differs from a regeneration; run $0" >&2
            status=1
        fi
    done < <(find "$out/root" -type f | sort)
    while IFS= read -r f; do
        rel="${f#"$ROOT/"}"
        [[ -f "$out/root/$rel" ]] || { echo "DRIFT $rel: no longer generated; run $0" >&2; status=1; }
    done < <(find "$ROOT/manifests/hub-core-services/platform-dashboards" -maxdepth 1 -type f -name 'dashboard-*.yaml' | sort)
    exit "$status"
fi

rm -f "$ROOT"/manifests/hub-core-services/platform-dashboards/dashboard-*.yaml
cp -R "$out/root/." "$ROOT/"
echo "generated $(find "$out/root" -type f | wc -l | tr -d ' ') file(s)"
