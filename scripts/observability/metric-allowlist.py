#!/usr/bin/env python3
"""The metrics this platform sends to the box owner's stack (ADR-078 add.3 §7).

Every series sent costs the stack owner, and the stack enforces an active-series
limit: rc.167 sent everything kubelet, cAdvisor, node-exporter and
kube-state-metrics expose, and the stack refused most of it (HTTP 429,
err-mimir-max-active-series, limit 15000). So the agents send only what a shipped
dashboard or rule reads.

The list is scripts/observability/mixin/metrics-allowlist.json, produced by
Grafana's mimirtool (`analyze dashboard` and `analyze rule-file`, real PromQL
parsing) over every shipped dashboard and PrometheusRule -- see
scripts/observability/mixin/generate.sh. This script only renders it into the
agents' configs, between markers, as one regex.

    scripts/observability/metric-allowlist.py          write it into the agents
    scripts/observability/metric-allowlist.py --check  fail if the agents differ
"""
import json, os, re, sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "../.."))
LIST = os.path.join(ROOT, "scripts/observability/mixin/metrics-allowlist.json")
AGENTS = ("manifests/hub-core-services/grafana-alloy/deployment.yaml",
          "manifests/spoke/spoke-catalog/infra/grafana-alloy.yaml")
BEGIN = "// BEGIN generated metric allowlist (scripts/observability/metric-allowlist.py)"
END = "// END generated metric allowlist"
NAME = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")


def allowlist():
    items = json.load(open(LIST))["metrics"]
    bad = [m for m in items if not NAME.fullmatch(m)]
    if bad:
        sys.exit(f"metric-allowlist: {LIST} holds names that are not metric names: {bad}")
    if items != sorted(set(items)):
        sys.exit(f"metric-allowlist: {LIST} is not sorted and unique; regenerate it")
    return items


def block(indent, items):
    pad = " " * indent
    return (f"{pad}{BEGIN}\n"
            f"{pad}// {len(items)} metric names: every one a shipped dashboard or rule reads.\n"
            f"{pad}rule {{\n"
            f'{pad}  source_labels = ["__name__"]\n'
            f'{pad}  regex         = "{"|".join(items)}"\n'
            f'{pad}  action        = "keep"\n'
            f"{pad}}}\n"
            f"{pad}{END}\n")


def render(text, items):
    pat = re.compile(r"^([ ]*)" + re.escape(BEGIN) + r".*?" + re.escape(END) + r"\n", re.M | re.S)
    if not pat.search(text):
        sys.exit("metric-allowlist: an agent config carries no allowlist markers")
    return pat.sub(lambda m: block(len(m.group(1)), items), text)


def main():
    items = allowlist()
    check = "--check" in sys.argv
    status = 0
    for rel in AGENTS:
        path = os.path.join(ROOT, rel)
        text = open(path).read()
        new = render(text, items)
        if new != text:
            if check:
                print(f"DRIFT {rel}: its metric allowlist differs from {os.path.relpath(LIST, ROOT)}; run {sys.argv[0]}")
                status = 1
            else:
                open(path, "w").write(new)
                print(f"wrote {len(items)} metric names into {rel}")
    if check and status == 0:
        print(f"OK {len(items)} metric names; every agent sends exactly these")
    sys.exit(status)


if __name__ == "__main__":
    main()
