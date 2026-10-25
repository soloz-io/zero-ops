#!/usr/bin/env python3
"""The metrics this platform sends to the box owner's stack (ADR-078 add.3 §7).

Every series sent costs the stack owner, and the stack enforces an active-series
limit: rc.167 sent everything kubelet, cAdvisor, node-exporter and
kube-state-metrics expose, and the stack refused most of it (HTTP 429,
err-mimir-max-active-series, limit 15000). So the agents send only what a shipped
dashboard or rule reads.

The list is DERIVED, never written by hand: it is every metric name read by a
committed platform dashboard or PrometheusRule, plus the series a scrape itself
produces (`up`). A hand list drifts -- a dashboard gains a panel and renders
empty -- which is the defect ADR-078 add.2 §2 forbids.

    scripts/observability/metric-allowlist.py          write it into the agents
    scripts/observability/metric-allowlist.py --check  fail if the agents differ

It is written between markers in each agent's config, as one regex.
"""
import glob, json, os, re, sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "../.."))
DASHBOARDS = "manifests/hub-core-services/platform-dashboards/dashboard-*.yaml"
RULES = ("manifests/hub-core-services/platform-alerts/*.yaml",
         "manifests/spoke/spoke-catalog/infra/platform-alerts/*.yaml")
AGENTS = ("manifests/hub-core-services/grafana-alloy/deployment.yaml",
          "manifests/spoke/spoke-catalog/infra/grafana-alloy.yaml")
BEGIN = "// BEGIN generated metric allowlist (scripts/observability/metric-allowlist.py)"
END = "// END generated metric allowlist"
# Always kept: a scrape's own health, which the pipeline-health rules and the
# collection-floor check read whether or not a dashboard does.
ALWAYS = {"up"}

KEYWORDS = {"by", "without", "on", "ignoring", "group_left", "group_right", "bool",
            "and", "or", "unless", "offset", "inf", "nan"}

sys.path.insert(0, ROOT)
import yaml  # noqa: E402


def exprs():
    out = []
    for f in sorted(glob.glob(os.path.join(ROOT, DASHBOARDS))):
        board = json.loads(yaml.safe_load(open(f))["spec"]["json"])
        stack = [board]
        while stack:
            o = stack.pop()
            if isinstance(o, dict):
                for k, v in o.items():
                    if k in ("expr", "query") and isinstance(v, str):
                        out.append(v)
                    elif k == "query" and isinstance(v, dict) and isinstance(v.get("query"), str):
                        out.append(v["query"])
                    else:
                        stack.append(v)
            elif isinstance(o, list):
                stack.extend(o)
    for pattern in RULES:
        for f in sorted(glob.glob(os.path.join(ROOT, pattern))):
            for d in yaml.safe_load_all(open(f)):
                if d and d.get("kind") == "PrometheusRule":
                    for g in d["spec"]["groups"]:
                        out.extend(r["expr"] for r in g["rules"])
    return out


def names(expr):
    e = re.sub(r'"(?:[^"\\]|\\.)*"', '""', expr)                    # string literals
    e = re.sub(r"\{[^{}]*\}", "", e)                                 # label matchers
    e = re.sub(r"\[[^\]]*\]", "", e)                                 # ranges
    e = re.sub(r"\$\{?[A-Za-z_][A-Za-z0-9_]*\}?", "", e)             # dashboard variables
    e = re.sub(r"label_values\(\s*([^,()]*)\s*,[^)]*\)", r"\1", e)   # keep the metric, not the label
    e = re.sub(r"\b(by|without|on|ignoring|group_left|group_right)\s*\([^)]*\)", "", e)
    found = set()
    for m in re.finditer(r"[A-Za-z_:][A-Za-z0-9_:]*", e):
        tok, rest = m.group(0), e[m.end():].lstrip()
        if rest.startswith("("):            # a function
            continue
        if tok.lower() in KEYWORDS or re.fullmatch(r"\d.*", tok):
            continue
        if ":" in tok:                       # a recording rule's output: produced in the ruler
            continue
        found.add(tok)
    return found


def allowlist():
    s = set(ALWAYS)
    for x in exprs():
        s |= names(x)
    return sorted(s)


def block(indent, items):
    pad = " " * indent
    regex = "|".join(items)
    return (f"{pad}{BEGIN}\n"
            f"{pad}// {len(items)} metric names: every one a shipped dashboard or rule reads.\n"
            f"{pad}rule {{\n"
            f'{pad}  source_labels = ["__name__"]\n'
            f'{pad}  regex         = "{regex}"\n'
            f'{pad}  action        = "keep"\n'
            f"{pad}}}\n"
            f"{pad}{END}\n")


def render(text, items):
    pat = re.compile(r"^([ ]*)" + re.escape(BEGIN) + r".*?" + re.escape(END) + r"\n", re.M | re.S)
    hits = list(pat.finditer(text))
    if not hits:
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
                print(f"DRIFT {rel}: its metric allowlist differs from what the shipped dashboards and rules read; run {sys.argv[0]}")
                status = 1
            else:
                open(path, "w").write(new)
                print(f"wrote {len(items)} metric names into {rel}")
    if check and status == 0:
        print(f"OK {len(items)} metric names; every agent sends exactly these")
    sys.exit(status)


if __name__ == "__main__":
    main()
