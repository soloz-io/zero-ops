#!/usr/bin/env bash
# ADR-078 §3: the platform-namespace telemetry filter must be able to see the
# namespace's labels.
#
# The filter keeps a pod only when __meta_kubernetes_namespace_label_* says its
# namespace is the platform's. Pod discovery carries namespace labels only with
# `attach_metadata { namespace = true }`, which exists from Alloy v1.12. Without
# it the label is always empty, the keep rule drops every pod, and Alloy reports
# no error: logs and metrics simply stop (2026-10-24: no platform pod log had
# been shipped from hub or spoke since the filter was introduced).
#
#   078-1  every discovery.relabel reading __meta_kubernetes_namespace_label_*
#          takes its targets from a discovery.kubernetes block that sets
#          attach_metadata { namespace = true };
#   078-2  every grafana/alloy image in a manifest carrying such a block is
#          v1.12 or later;
#   078-3  a Grafana Cloud writer appends its push path to the URL secret, which
#          holds the URL as Grafana Cloud shows it (Loki: the host; Prometheus:
#          …/api/prom). Used bare, every write is refused (405);
#   078-4  pod logs reach the box's own store (loki.write "platform"), never
#          only an external destination (§7.1);
#   078-5  the spoke agent's CELL_ID and TENANT_ID (the cluster and tenant
#          labels, add.1 §7) are literals the spoke catalogue's templated-fields
#          substitute at their exact env index. A value read from anywhere else
#          (2026-10-24: a pod label nothing set) leaves the label empty. Checked
#          for every spoke agent (node DaemonSet, cluster Deployment);
#   078-6  every series-level tenant-exclusion list in one manifest is the same
#          list, so no agent admits a namespace another excludes;
#   078-7  every node-agent DaemonSet tolerates the control-plane taint (in its
#          manifest, or by its catalogue's kustomization patch), so no node goes
#          unwatched (2026-10-24: the hub's control plane had no agent).

validate_pod_discovery_attaches_namespace_labels() {
    section "ADR-078: pod discovery attaches the namespace labels its filter reads"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 - <<'PY'
import pathlib, re

ROOTS = ["manifests", "internal/platform/embedded/manifests"]
BLOCK = re.compile(r'discovery\.kubernetes\s+"([^"]+)"\s*\{')
RELABEL = re.compile(r'discovery\.relabel\s+"([^"]+)"\s*\{')
IMAGE = re.compile(r'grafana/alloy:v(\d+)\.(\d+)\.\d+')

def body(text, start):
    """The text of the brace block opening at or after start."""
    i = text.index("{", start)
    depth = 0
    for j in range(i, len(text)):
        if text[j] == "{":
            depth += 1
        elif text[j] == "}":
            depth -= 1
            if depth == 0:
                return text[i + 1:j]
    return text[i + 1:]

checked = 0
for root in ROOTS:
    for path in sorted(pathlib.Path(root).rglob("*.yaml")):
        text = path.read_text()
        if "__meta_kubernetes_namespace_label_" not in text:
            continue
        blocks = {m.group(1): body(text, m.end() - 1) for m in BLOCK.finditer(text)}
        for m in RELABEL.finditer(text):
            rb = body(text, m.end() - 1)
            if "__meta_kubernetes_namespace_label_" not in rb:
                continue
            src = re.search(r'targets\s*=\s*discovery\.kubernetes\.([A-Za-z0-9_]+)\.targets', rb)
            if not src:
                continue
            checked += 1
            disc = blocks.get(src.group(1), "")
            am = re.search(r'attach_metadata\s*\{([^}]*)\}', disc)
            if not am or not re.search(r'namespace\s*=\s*true', am.group(1)):
                print(f'FAIL {path}: discovery.relabel "{m.group(1)}" reads namespace labels, but '
                      f'discovery.kubernetes "{src.group(1)}" has no attach_metadata {{ namespace = true }}; '
                      f'its keep rule drops every pod')
        for var, suffix in (("LOKI_URL", "/loki/api/v1/push"), ("PROMETHEUS_URL", "/push")):
            for u in re.finditer(r'url\s*=\s*sys\.env\("' + var + r'"\)([^\n]*)', text):
                if f'+ "{suffix}"' not in u.group(1):
                    print(f'FAIL {path}: url = sys.env("{var}") must append "{suffix}"; the secret holds the URL Grafana Cloud shows, not its write endpoint')
        for s in re.finditer(r'loki\.source\.file\s+"([^"]+)"\s*\{', text):
            fb = body(text, s.end() - 1)
            if "loki.write.platform.receiver" not in fb:
                print(f'FAIL {path}: loki.source.file "{s.group(1)}" does not forward to loki.write.platform; the in-box store must always receive pod logs (ADR-078 §7.1)')
        for im in IMAGE.finditer(text):
            if (int(im.group(1)), int(im.group(2))) < (1, 12):
                print(f"FAIL {path}: {im.group(0)} predates attach_metadata namespace (Alloy v1.12)")

import yaml
spoke = pathlib.Path("manifests/spoke/spoke-catalog/infra/grafana-alloy.yaml")
fields = [e for doc in yaml.safe_load_all(pathlib.Path("manifests/spoke/spoke-catalog/templated-fields.yaml").read_text())
          for e in (doc if isinstance(doc, list) else [doc])]
AGENTS = {("DaemonSet", "grafana-alloy"), ("Deployment", "grafana-alloy-cluster")}
paths = {(d.get("kind"), d.get("name")): [f["path"] for f in d.get("fields", []) if "path" in f]
         for d in fields if d}
seen = set()
for d in yaml.safe_load_all(spoke.read_text()):
    if not d or (d.get("kind"), d["metadata"]["name"]) not in AGENTS:
        continue
    key = (d["kind"], d["metadata"]["name"])
    seen.add(key)
    env = d["spec"]["template"]["spec"]["containers"][0].get("env", [])
    for var in ("CELL_ID", "TENANT_ID"):
        idx = next((i for i, e in enumerate(env) if e["name"] == var), None)
        want = ["spec", "template", "spec", "containers", 0, "env", idx, "value"]
        if idx is None or "value" not in env[idx] or want not in paths.get(key, []):
            print(f"FAIL {spoke}: {key[0]}/{key[1]} {var} must be a literal that templated-fields substitutes at env[{idx}]; otherwise its label is empty or constant on every spoke")
for key in sorted(AGENTS - seen):
    print(f"FAIL {spoke}: {key[0]}/{key[1]} is missing; the spoke has no agent for its targets")
lists = set(re.findall(r'prometheus\.relabel\s+"platform_namespaces_only"\s*\{.*?regex\s*=\s*"([^"]*)"', spoke.read_text(), re.S))
if len(lists) != 1:
    print(f"FAIL {spoke}: the platform-namespace lists differ between agents ({len(lists)} variants); one would admit a namespace another excludes")

CP = {"key": "node-role.kubernetes.io/control-plane", "operator": "Exists", "effect": "NoSchedule"}
kust = pathlib.Path("manifests/spoke/spoke-catalog/infra/kustomization.yaml").read_text()
spoke_patched = bool(re.search(r'kind:\s*DaemonSet\s*\n\s*patch:[\s\S]*?/spec/template/spec/tolerations[\s\S]*?node-role\.kubernetes\.io/control-plane', kust))
for path, patched in ((pathlib.Path("manifests/hub-core-services/grafana-alloy/deployment.yaml"), False), (spoke, spoke_patched)):
    for d in yaml.safe_load_all(path.read_text()):
        if d and d.get("kind") == "DaemonSet" and d["metadata"]["name"] == "grafana-alloy":
            if not patched and CP not in (d["spec"]["template"]["spec"].get("tolerations") or []):
                print(f"FAIL {path}: DaemonSet/grafana-alloy does not tolerate the control-plane taint; that node gets no agent and no collection")

print(f"PASS {checked} namespace-label filter(s) read labels their discovery attaches; spoke cluster and tenant labels are substituted; node agents reach every node")
PY
    )

    local line failed=0
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        case "$line" in
            FAIL\ *) hard_fail "${line#FAIL }"; failed=1 ;;
            PASS\ *) [[ $failed -eq 0 ]] && pass "${line#PASS }" ;;
            *)       note "$line" ;;
        esac
    done <<< "$out"
}
