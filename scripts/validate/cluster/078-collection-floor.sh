#!/usr/bin/env bash
# ADR-078 addendum 2 §15, addendum 5: the collection floor is accepted on
# end-to-end evidence, never on a green sync. LIVE checks, read-only, against each
# cluster's OWN stores -- where its dashboards and rules read.
#
#   C1  each expected job has targets for this cluster, and every target is up
#   C2  node-local jobs (kubelet, cadvisor, node-exporter) have one up target
#       per node -- a node with no agent is a node nobody is watching
#   C3  a known series from each job is queryable
#   C4  that series carries the cluster's own `cluster` label (the agent's
#       configured identity), never empty and never a placeholder
#   C5  this cluster's pod logs are queryable in its Loki
#   C6  this cluster's platform rules are loaded by its platform VMAlert
#   C7  both VMSingles hold this cluster's series (addendum 5 §3: every sample
#       is written to both)
#
# Every query goes through the Kubernetes API server's service proxy with the
# kubeconfig's own identity: no credential of its own, nothing published.

# _c078_check <label> <kubeconfig> <identity env name> <jobs...>
_c078_check() {
    local label="$1" kubeconfig="$2" idvar="$3"; shift 3
    local jobs="$*" expected nodes out line

    expected=$(kubectl --kubeconfig="$kubeconfig" -n platform-observability get ds grafana-alloy \
        -o jsonpath="{.spec.template.spec.containers[0].env[?(@.name==\"$idvar\")].value}" 2>/dev/null)
    if [[ -z "$expected" || "$expected" == "PLACEHOLDER" ]]; then
        hard_fail "C4 $label: the node agent's $idvar is '${expected:-<empty>}'; templated-fields did not substitute the cluster's name"
        return 0
    fi
    nodes=$(kubectl --kubeconfig="$kubeconfig" get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null)

    out=$(C078_KUBECONFIG="$kubeconfig" C078_JOBS="$jobs" C078_CLUSTER="$expected" C078_NODES="$nodes" python3 - <<'PY'
import json, os, subprocess, time, urllib.parse

cluster = os.environ["C078_CLUSTER"]
jobs = os.environ["C078_JOBS"].split()
nodes = sorted(n for n in os.environ["C078_NODES"].split() if n)
kubeconfig = os.environ["C078_KUBECONFIG"]
NS = "/api/v1/namespaces/platform-observability/services"
STORES = ("vmsingle-platform-a:8429", "vmsingle-platform-b:8429")
NODE_LOCAL = {"kubelet", "cadvisor", "node-exporter"}
KNOWN = {
    "kubelet": "kubelet_running_pods",
    "cadvisor": "container_cpu_usage_seconds_total",
    "node-exporter": "node_cpu_seconds_total",
    "kube-state-metrics": "kube_node_info",
    # The series the CNPG recovery alerts read (cnpg-backup-alerts).
    "cnpg": "cnpg_collector_pg_wal_archive_status",
}
# Each probe must be a metric the agents send: a probe for a series the
# allowlist drops fails for a reason that is not a fault.
allowed = set(json.load(open(os.environ["C078_ALLOWLIST"]))["metrics"])
for job, m in KNOWN.items():
    if m not in allowed:
        print(f"FAIL C3 {job}: this check probes {m}, which the metric allowlist does not send; probe an allowlisted series")

class Unreachable(Exception):
    pass

def raw(path):
    r = subprocess.run(["kubectl", f"--kubeconfig={kubeconfig}", "get", "--raw", path],
                       capture_output=True, text=True, timeout=60)
    if r.returncode != 0:
        raise Unreachable(r.stderr.strip().splitlines()[-1] if r.stderr.strip() else "no answer")
    return json.loads(r.stdout)

def q(expr, store=STORES[0]):
    return raw(f"{NS}/{store}/proxy/api/v1/query?" + urllib.parse.urlencode({"query": expr}))["data"]["result"]

try:
    q("vector(1)")
except Unreachable as e:
    print(f"FAIL C3 {STORES[0].split(':')[0]} did not answer a query through the service proxy: {e}")
    raise SystemExit

c = f'cluster="{cluster}"'
for job in jobs:
    up = q(f'up{{job="{job}",{c}}}')
    if not up:
        print(f"FAIL C1 {job}: no targets for {cluster} -- not scraped, or not delivered")
        continue
    down = [r["metric"].get("instance", "?") for r in up if r["value"][1] != "1"]
    print(f"FAIL C1 {job}: {len(down)}/{len(up)} target(s) down: {', '.join(down)}" if down
          else f"PASS C1 {job}: {len(up)} target(s) up")
    if job in NODE_LOCAL:
        have = {r["metric"].get("node", "") for r in up if r["value"][1] == "1"}
        missing = [n for n in nodes if n not in have]
        print(f"FAIL C2 {job}: no up target on node(s) {', '.join(missing)}" if missing
              else f"PASS C2 {job}: one up target on each of {len(nodes)} node(s)")
    m = KNOWN[job]
    if q(f'count({m}{{job="{job}",{c}}})'):
        print(f"PASS C3 {job}: {m} is queryable with {c}")
    elif q(f'count({m}{{job="{job}"}})'):
        got = sorted({r["metric"].get("cluster", "") for r in q(f'{m}{{job="{job}"}}')})
        print(f"FAIL C4 {job}: {m} carries cluster={got}, expected {cluster!r}")
    else:
        print(f"FAIL C3 {job}: {m} is not stored although the job is scraped")

for store in STORES:
    try:
        n = q(f'count(up{{{c}}})', store)
        print(f"PASS C7 {store.split(':')[0]} holds {cluster}'s series" if n
              else f"FAIL C7 {store.split(':')[0]} holds none of {cluster}'s series: one write path is not delivering")
    except Unreachable as e:
        print(f"FAIL C7 {store.split(':')[0]} did not answer: {e}")

try:
    now = int(time.time())
    res = raw(f"{NS}/loki:3100/proxy/loki/api/v1/query_range?" + urllib.parse.urlencode({
        "query": f'sum(count_over_time({{{c}}}[10m]))', "start": now - 900, "end": now, "step": 300}))["data"]["result"]
    print(f"PASS C5 logs from {cluster} are queryable in its Loki" if res
          else f"FAIL C5 no log lines from {cluster} in its Loki in the last 15 minutes")
except Unreachable as e:
    print(f"FAIL C5 Loki did not answer a query through the service proxy: {e}")

try:
    groups = raw(f"{NS}/vmalert-platform:8080/proxy/api/v1/rules")["data"]["groups"]
    print(f"PASS C6 the platform VMAlert has {len(groups)} rule group(s) loaded" if groups
          else "FAIL C6 the platform VMAlert has no rule groups: PrometheusRule conversion or selection is not working")
except (Unreachable, KeyError) as e:
    print(f"FAIL C6 the platform VMAlert did not report its rules: {e}")
PY
)
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        case "$line" in
            PASS\ *) pass "$label ${line#PASS }" ;;
            FAIL\ *) hard_fail "$label ${line#FAIL }" ;;
            *)       note "$line" ;;
        esac
    done <<< "$out"
}

validate_adr078_collection_floor() {
    section "ADR-078: the collection floor lands in each cluster's own stores"

    C078_ALLOWLIST="$VALIDATE_ROOT/scripts/observability/mixin/metrics-allowlist.json"
    export C078_ALLOWLIST

    _c078_check hub "$HUB_KUBECONFIG" CLUSTER_NAME kubelet cadvisor node-exporter kube-state-metrics

    if ensure_spoke_kubeconfig; then
        _c078_check spoke "$SPOKE_KUBECONFIG" CELL_ID kubelet cadvisor node-exporter kube-state-metrics cnpg
    else
        soft_fail "no spoke kubeconfig; the spoke collection floor was not checked"
    fi
}
