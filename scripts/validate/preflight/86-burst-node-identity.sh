#!/usr/bin/env bash
# ADR-052 D4: the burst node's identity is defined in three places and all three
# must agree, exactly.
#
#   1. spokepool-burst-bootstrap-v1  register-with-taints / node-labels
#        — what the real node registers with
#   2. burst MachineDeployment       capacity.cluster-autoscaler.kubernetes.io/{taints,labels}
#        — what cluster-autoscaler simulates when the pool is at zero
#   3. ephemeral-job-operator        BurstTaintKey / BurstTaintValue in placement.go
#        — what the platform writes onto the workload
#
# Drift between (1) and (2) is a SILENT OUTAGE. With the pool at zero the
# autoscaler has no real node to inspect, so it builds a hypothetical one from
# the capacity annotations. If the real node carries a taint the annotation
# omits, the autoscaler simulates a node the pending pod cannot tolerate,
# concludes that scale-up would not help, declines to scale, and logs no error.
# The pod pends forever and every dashboard is green.
#
# Drift between (1) and (3) is the same failure with the polarity reversed: the
# workload tolerates a taint the node does not carry, or fails to tolerate one
# it does, and again nothing errors.
#
# Three hand-maintained strings for one taint is a silent-outage generator.
# This check is why it is not one.
validate_burst_node_identity() {
    section "ADR-052 D4 burst node identity (bootstrap / autoscaler / operator)"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import io, re, sys, yaml

CLUSTERCLASS = "manifests/providers/hetzner/base/spokepool-clusterclass-v1.yaml"
COMPOSITION  = "manifests/providers/hybrid/k8s/spokepool-hybrid-composition.yaml"
PLACEMENT    = "operators/ephemeral-job-operator/internal/controller/placement.go"

errors = []

def norm_taints(s):
    return sorted(t.strip() for t in s.split(",") if t.strip())

def norm_labels(s):
    out = {}
    for kv in s.split(","):
        kv = kv.strip()
        if not kv:
            continue
        k, _, v = kv.partition("=")
        out[k] = v
    return out

# ── 1. the real node ──────────────────────────────────────────────────────
node_taints = node_labels = None
for doc in yaml.safe_load_all(io.open(CLUSTERCLASS, encoding="utf-8")):
    if not doc or doc.get("kind") != "KubeadmConfigTemplate":
        continue
    if doc["metadata"]["name"] != "spokepool-burst-bootstrap-v1":
        continue
    args = doc["spec"]["template"]["spec"]["joinConfiguration"]["nodeRegistration"]["kubeletExtraArgs"]
    node_taints = args.get("register-with-taints", "")
    node_labels = args.get("node-labels", "")

if node_taints is None:
    errors.append("spokepool-burst-bootstrap-v1 not found in %s" % CLUSTERCLASS)
elif not node_taints:
    errors.append(
        "burst bootstrap has no register-with-taints: the burst pool is "
        "unreserved, so any pod with a hetzner nodeSelector can consume "
        "capacity the cell pays for by the minute")

# ── 2. what the autoscaler simulates ──────────────────────────────────────
comp = yaml.safe_load(io.open(COMPOSITION, encoding="utf-8"))
md = None
for r in comp["spec"]["pipeline"][0]["input"]["resources"]:
    if r["name"] != "capi-cluster":
        continue
    mds = r["base"]["spec"]["forProvider"]["manifest"]["spec"]["topology"]["workers"]["machineDeployments"]
    md = next((m for m in mds if m.get("class") == "burst-worker"), None)

if md is None:
    errors.append("no burst-worker machineDeployment in %s" % COMPOSITION)
else:
    if "replicas" in md:
        errors.append(
            "burst machineDeployment declares replicas. CAPI rejects replicas "
            "together with the autoscaler bounds annotations, and "
            "provider-kubernetes applies the Cluster with a full Update — one "
            "invalid field wedges the whole Object at Synced=False and blocks "
            "every subsequent Crossplane update to that Cluster (bac465a9).")

    ann = (md.get("metadata") or {}).get("annotations") or {}
    ca_taints = ann.get("capacity.cluster-autoscaler.kubernetes.io/taints", "")
    ca_labels = ann.get("capacity.cluster-autoscaler.kubernetes.io/labels", "")

    for req in ("cluster.x-k8s.io/cluster-api-autoscaler-node-group-min-size",
                "cluster.x-k8s.io/cluster-api-autoscaler-node-group-max-size"):
        if req not in ann:
            errors.append("burst machineDeployment missing %s: the autoscaler "
                          "will not discover this node group at all" % req)

    if node_taints is not None and norm_taints(ca_taints) != norm_taints(node_taints):
        errors.append(
            "TAINT DRIFT — the autoscaler would simulate a node the real one is not.\n"
            "        node registers with : %s\n"
            "        autoscaler simulates: %s\n"
            "        Effect: pending burst pods never trigger scale-up, and nothing errors."
            % (node_taints or "(none)", ca_taints or "(none)"))

    if node_labels is not None:
        nl, cl = norm_labels(node_labels), norm_labels(ca_labels)
        missing = {k: v for k, v in nl.items() if k not in cl}
        if missing:
            errors.append(
                "LABEL DRIFT — labels on the real node are absent from the "
                "autoscaler's simulated node: %s\n"
                "        Effect: a pod selecting them is judged unschedulable on "
                "this group and scale-up is declined." % sorted(missing))

# ── 3. what the platform writes onto the workload ─────────────────────────
src = io.open(PLACEMENT, encoding="utf-8").read()
def const(name):
    m = re.search(r'\b%s\s*=\s*"([^"]*)"' % name, src)
    return m.group(1) if m else None

op_key, op_val = const("BurstTaintKey"), const("BurstTaintValue")
if op_key is None or op_val is None:
    errors.append("could not read BurstTaintKey/BurstTaintValue from %s" % PLACEMENT)
elif node_taints:
    want = "%s=%s:NoSchedule" % (op_key, op_val)
    if want not in norm_taints(node_taints):
        errors.append(
            "OPERATOR/NODE DRIFT — the toleration the operator writes does not "
            "match the taint the node carries.\n"
            "        operator tolerates: %s\n"
            "        node registers    : %s\n"
            "        Effect: every burst pod is rejected by the taint it was "
            "supposed to tolerate." % (want, node_taints))

# ── 4. the priority class value, in the two places it is written ──────────
#
# Kyverno must set spec.priority alongside priorityClassName on sandbox pods:
# the upstream sandbox controller populates spec.priority, and the Priority
# admission plugin REJECTS a pod whose spec.priority disagrees with the value it
# computes from the class name. A disagreement here does not misplace the
# sandbox — it prevents the controller from creating the pod at all, and it
# retries forever.
PRIORITYCLASS = "manifests/spoke/spoke-catalog/infra/burst-priorityclass.yaml"
POLICY        = "manifests/spoke/spoke-catalog/infra/kyverno-burst-placement.yaml"

pc = yaml.safe_load(io.open(PRIORITYCLASS, encoding="utf-8"))
pc_name, pc_value = pc["metadata"]["name"], pc["value"]

pol = [d for d in yaml.safe_load_all(io.open(POLICY, encoding="utf-8")) if d]
mut = next((d for d in pol if d["metadata"]["name"] == "burst-placement-sandbox"), None)
if mut is None:
    errors.append("burst-placement-sandbox policy not found in %s" % POLICY)
else:
    spec = mut["spec"]["rules"][0]["mutate"]["patchStrategicMerge"]["spec"]
    if spec.get("priorityClassName") != pc_name:
        errors.append("mutation sets priorityClassName=%r but the PriorityClass is %r"
                      % (spec.get("priorityClassName"), pc_name))
    if "priority" not in spec:
        errors.append(
            "mutation sets priorityClassName without spec.priority. The upstream "
            "sandbox controller populates spec.priority, so the Priority admission "
            "plugin will reject every sandbox pod: 'the integer value of priority "
            "(0) must not be provided in pod spec'. The sandbox is then never "
            "created at all, not merely misplaced.")
    elif spec["priority"] != pc_value:
        errors.append("mutation sets priority=%r but PriorityClass %s has value %r"
                      % (spec["priority"], pc_name, pc_value))

    # The mutation's toleration must match the node's taint too.
    tols = spec.get("tolerations") or []
    mt = ["%s=%s:%s" % (t.get("key"), t.get("value"), t.get("effect")) for t in tols]
    if node_taints and node_taints not in mt:
        errors.append("KYVERNO/NODE DRIFT — mutation tolerates %s, node registers %s"
                      % (mt or "(none)", node_taints))

if errors:
    for e in errors:
        print("  FAIL: %s" % e)
    sys.exit(1)

print("  burst identity agrees across bootstrap, autoscaler, operator and Kyverno")
print("    taint : %s" % node_taints)
print("    labels: %s" % node_labels)
PY
    ) || { echo "$out"; hard_fail "burst identity drift (ADR-052 D4) — see FAIL lines above"; return 0; }

    echo "$out"
    pass "burst node identity consistent"
}
