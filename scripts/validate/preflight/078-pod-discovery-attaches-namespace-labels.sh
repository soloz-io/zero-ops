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
#          only an external destination (§7.1).

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

print(f"PASS {checked} namespace-label filter(s) read labels their discovery attaches")
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
