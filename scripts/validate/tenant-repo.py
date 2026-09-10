#!/usr/bin/env python3
"""Assert a tenant repository declares something the platform can maintain.

ADR-064 states its constraints as validators, so a promotion or a grouping that
violates one fails its pull request rather than reaching a cluster. This runs
against a tenant's repository -- as a check on the proposal Renovate opens, and
on anything the tenant writes itself.

What it checks, and why each is not obvious from reading the file:

  A cluster names exactly one owning tenant. Inferring it from the repository
  name would make the check assert a fact it had itself supplied.

  A cluster's pinned version exists as a published chart. A version that does
  not resolve leaves the Application unable to load its source, which ArgoCD
  reports as a sync failure on the cluster rather than as a bad merge.

  A pinned version is not below the minimum its own release declares. Renovate
  proposes the newest; a tenant editing by hand, or reverting, can land on a
  version the newest may not be taken from.

  The chart source and the values source are distinct, and the values source is
  the tenant's own repository. A cluster resolving the platform's repository at
  runtime is the dependency ADR-063 forbids, and it is invisible on a rendered
  bundle that is otherwise correct.

Three of ADR-064's constraints are not here and cannot be, yet:
  - that a cluster advances only from a version validated in the preceding
    environment, which needs environment state the platform does not collect;
  - that a proposal carries a pre-flight verdict (ADR-067), which needs telemetry
    that is not implemented;
  - that a proposal's recorded component versions resolve from the bundle version,
    which needs the resolved set a proposal does not yet carry.
They are listed so their absence is a known gap rather than an oversight.

Usage: tenant-repo.py <repo-dir> [--offline]
Exit 0 valid, 1 invalid.
"""
import os
import re
import subprocess
import sys

try:
    import yaml
except ImportError:
    sys.exit("PyYAML required")

CHART_REVISION = re.compile(r"^\s*targetRevision:\s*(\S+)\s*$")


def clusters(repo):
    root = os.path.join(repo, "clusters")
    if not os.path.isdir(root):
        return []
    return sorted(n for n in os.listdir(root)
                  if os.path.isdir(os.path.join(root, n)))


def bundle_sources(path):
    """The sources a cluster's bundle declares, and whether it declares both."""
    with open(path) as handle:
        doc = yaml.safe_load(handle) or {}
    spec = doc.get("spec") or {}
    return spec.get("source"), spec.get("sources") or []


def chart_published(registry, chart, version):
    """Whether a version resolves, asked of the registry rather than assumed."""
    ref = f"oci://{registry}/{chart}" if not registry.startswith("oci://") else f"{registry}/{chart}"
    done = subprocess.run(["helm", "show", "chart", ref, "--version", version],
                          capture_output=True, text=True)
    return done.returncode == 0, done.stderr.strip()[:160]


def main() -> int:
    if len(sys.argv) < 2:
        print(__doc__)
        return 2
    repo = sys.argv[1]
    offline = "--offline" in sys.argv

    names = clusters(repo)
    if not names:
        print(f"tenant repo: {repo} declares no clusters")
        return 1

    problems = []
    checked = 0

    for name in names:
        cdir = os.path.join(repo, "clusters", name)
        bundle = os.path.join(cdir, "bundle.yaml")
        values = os.path.join(cdir, "values.yaml")
        if not os.path.exists(bundle):
            continue
        checked += 1

        single, multi = bundle_sources(bundle)

        # ArgoCD's GetSources returns spec.sources whenever it is non-empty and
        # never consults spec.source, so an Application holding both resolves the
        # array and silently ignores the chart.
        if single and multi:
            problems.append((name, "declares both spec.source and spec.sources; "
                                   "ArgoCD resolves the array and ignores the source"))
            continue
        if not multi:
            problems.append((name, "declares no sources"))
            continue

        chart_src = next((s for s in multi if s.get("chart")), None)
        if chart_src is None:
            problems.append((name, "no source names a chart; the bundle is a "
                                   "published distribution, not a path (ADR-063)"))
            continue

        registry = chart_src.get("repoURL", "")
        if "://" in registry:
            problems.append((name, f"the registry carries a scheme ({registry}); "
                                   f"ArgoCD would fetch it as a classic Helm repository"))

        for s in multi:
            if s is chart_src:
                continue
            url = s.get("repoURL", "")
            if "zero-ops" in url and url.startswith(("http://", "https://")):
                problems.append((name, f"resolves the platform's repository at "
                                       f"runtime ({url}); revoking the platform's "
                                       f"access would stop this cluster reconciling"))

        vals = {}
        if os.path.exists(values):
            with open(values) as handle:
                vals = yaml.safe_load(handle) or {}

        tenant = vals.get("tenantId")
        if not tenant:
            problems.append((name, "names no owning tenant; ADR-064 requires a "
                                   "cluster instance to name exactly one"))

        version = str(chart_src.get("targetRevision", ""))
        if not version or version.startswith("<"):
            problems.append((name, f"pins no bundle version (got {version!r})"))
            continue

        if offline or not registry:
            continue

        ok, err = chart_published(registry.replace("oci://", ""),
                                  chart_src["chart"], version)
        if not ok:
            problems.append((name, f"pins {version}, which does not resolve as a "
                                   f"published chart: {err}"))

    if problems:
        print(f"tenant repo: {len(problems)} problem(s) across {checked} cluster(s)")
        for name, detail in problems:
            print(f"  {name}: {detail}")
        return 1

    mode = " (offline: versions not probed)" if offline else ""
    print(f"tenant repo: {checked} cluster(s), all declarations valid{mode}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
