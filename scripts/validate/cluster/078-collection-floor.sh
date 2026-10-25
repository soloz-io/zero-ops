#!/usr/bin/env bash
# ADR-078 addendum 2 §15, addendum 3: the collection floor is accepted on
# end-to-end evidence, never on a green sync. LIVE checks, read-only, against
# the box owner's Grafana Cloud stack -- where every dashboard and rule reads.
#
#   C1  each expected job has targets for this cluster, and every target is up
#   C2  node-local jobs (kubelet, cadvisor, node-exporter) have one up target
#       per node -- a node with no agent is a node nobody is watching
#   C3  a known series from each job is queryable in the stack
#   C4  that series carries the cluster's own `cluster` label (the agent's
#       configured identity), never empty and never a placeholder
#   C5  this cluster's pod logs are queryable in the stack
#   C6  this cluster's platform rules are in the stack's ruler, under its prefix
#
# Credentials (ADR-078 add.3 §6), read from the environment or, failing that,
# from k8-secrets/grafana-cloud/ in this checkout -- the same two sources Day-0
# reads:
#   GRAFANA_CLOUD_PROMETHEUS_URL / _USER, GRAFANA_CLOUD_LOKI_URL / _USER
#   GRAFANA_CLOUD_READ_TOKEN   metrics:read, logs:read (validation only)
#   GRAFANA_CLOUD_RULES_TOKEN  rules:read (C6)

_c078_cred() {
    local var="$1" file="$2" v="${!1:-}"
    if [[ -z "$v" && -f "$VALIDATE_ROOT/k8-secrets/grafana-cloud/$file" ]]; then
        v="$(tr -d '\n' < "$VALIDATE_ROOT/k8-secrets/grafana-cloud/$file")"
    fi
    printf '%s' "$v"
}

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

    out=$(C078_JOBS="$jobs" C078_CLUSTER="$expected" C078_NODES="$nodes" python3 - <<'PY'
import base64, json, os, time, urllib.error, urllib.parse, urllib.request
import yaml

cluster = os.environ["C078_CLUSTER"]
jobs = os.environ["C078_JOBS"].split()
nodes = sorted(n for n in os.environ["C078_NODES"].split() if n)
prom, puser = os.environ["C078_PROM_URL"], os.environ["C078_PROM_USER"]
loki, luser = os.environ["C078_LOKI_URL"], os.environ["C078_LOKI_USER"]
read, rules = os.environ["C078_READ_TOKEN"], os.environ["C078_RULES_TOKEN"]
NODE_LOCAL = {"kubelet", "cadvisor", "node-exporter"}
KNOWN = {
    "kubelet": "kubelet_running_pods",
    "cadvisor": "container_cpu_usage_seconds_total",
    "node-exporter": "node_cpu_seconds_total",
    "kube-state-metrics": "kube_node_info",
    "cnpg": "cnpg_collector_up",
}

def get(url, user, token, parse=json.load):
    req = urllib.request.Request(url)
    req.add_header("Authorization", "Basic " + base64.b64encode(f"{user}:{token}".encode()).decode())
    with urllib.request.urlopen(req, timeout=30) as r:
        return parse(r)

def q(expr):
    return get(prom + "/api/v1/query?" + urllib.parse.urlencode({"query": expr}), puser, read)["data"]["result"]

try:
    q("vector(1)")
except urllib.error.HTTPError as e:
    print(f"FAIL C3 the stack refused a query ({e.code}): GRAFANA_CLOUD_READ_TOKEN needs metrics:read")
    raise SystemExit

c = f'cluster="{cluster}"'
for job in jobs:
    up = q(f'up{{job="{job}",{c}}}')
    if not up:
        print(f"FAIL C1 {job}: no targets for {cluster} in the stack -- not scraped, or not delivered")
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
        print(f"FAIL C3 {job}: {m} is not in the stack although the job is scraped")

try:
    now = int(time.time())
    res = get(loki + "/loki/api/v1/query_range?" + urllib.parse.urlencode({
        "query": f'sum(count_over_time({{{c}}}[10m]))', "start": now - 900, "end": now, "step": 300}),
        luser, read)["data"]["result"]
    print(f"PASS C5 logs from {cluster} are queryable in the stack" if res
          else f"FAIL C5 no log lines from {cluster} in the stack in the last 15 minutes")
except urllib.error.HTTPError as e:
    print(f"FAIL C5 the stack refused a log query ({e.code}): GRAFANA_CLOUD_READ_TOKEN needs logs:read")

try:
    # The ruler's configuration API answers in YAML (namespace -> groups).
    groups = get(prom + "/config/v1/rules", puser, rules, parse=yaml.safe_load)
    names = [ns for ns in (groups or {}) if ns.startswith(cluster + "/")]
    print(f"PASS C6 {len(names)} rule namespace(s) under {cluster}/ in the ruler" if names
          else f"FAIL C6 no rules under {cluster}/ in the ruler -- the cluster agent's rule sync has not run or is failing")
except urllib.error.HTTPError as e:
    print(f"FAIL C6 the ruler refused a read ({e.code}): GRAFANA_CLOUD_RULES_TOKEN needs rules:read")
except yaml.YAMLError:
    print("FAIL C6 the ruler answered with something that is not its rule configuration; check GRAFANA_CLOUD_PROMETHEUS_URL")
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
    section "ADR-078: the collection floor lands in the box owner's Grafana Cloud stack"

    local k missing=()
    C078_PROM_URL="$(_c078_cred GRAFANA_CLOUD_PROMETHEUS_URL prometheus-url)"
    C078_PROM_USER="$(_c078_cred GRAFANA_CLOUD_PROMETHEUS_USER prometheus-user)"
    C078_LOKI_URL="$(_c078_cred GRAFANA_CLOUD_LOKI_URL loki-url)"
    C078_LOKI_USER="$(_c078_cred GRAFANA_CLOUD_LOKI_USER loki-user)"
    C078_READ_TOKEN="$(_c078_cred GRAFANA_CLOUD_READ_TOKEN read-token)"
    C078_RULES_TOKEN="$(_c078_cred GRAFANA_CLOUD_RULES_TOKEN rules-token)"
    for k in C078_PROM_URL:PROMETHEUS_URL C078_PROM_USER:PROMETHEUS_USER C078_LOKI_URL:LOKI_URL \
             C078_LOKI_USER:LOKI_USER C078_READ_TOKEN:READ_TOKEN C078_RULES_TOKEN:RULES_TOKEN; do
        local var="${k%%:*}"
        [[ -n "${!var}" ]] || missing+=("GRAFANA_CLOUD_${k#*:}")
    done
    if (( ${#missing[@]} )); then
        hard_fail "Grafana Cloud credentials missing: ${missing[*]} (environment, or k8-secrets/grafana-cloud/)"
        return 0
    fi
    export C078_PROM_URL C078_PROM_USER C078_LOKI_URL C078_LOKI_USER C078_READ_TOKEN C078_RULES_TOKEN

    _c078_check hub "$HUB_KUBECONFIG" CLUSTER_NAME kubelet cadvisor node-exporter kube-state-metrics

    if ensure_spoke_kubeconfig; then
        _c078_check spoke "$SPOKE_KUBECONFIG" CELL_ID kubelet cadvisor node-exporter kube-state-metrics cnpg
    else
        soft_fail "no spoke kubeconfig; the spoke collection floor was not checked"
    fi
}
