#!/usr/bin/env python3
"""Fail a release whose bundle names a chart the release does not publish.

A released bundle resolves every platform-owned Application from the registry.
An Application naming a chart that was never pushed is not a build failure and
not a portability failure -- the reference is to the right registry, and there
is simply nothing there. It becomes visible only when a cluster tries to
reconcile it, which is after the release has been declared good.

That is how v0.1.1 shipped: six Applications resolved to charts nothing
packaged, because the packager iterates component descriptors and those six are
declared inline in the boundary templates, where the environment and provider
dimensions they need are in scope. Nothing connected the two lists.

Reads the released render on stdin and the names the release produced as
arguments. Exit 0 complete, 1 incomplete.

Usage: helm template ... | bundle-completeness.py <chart-dir> [<chart-dir> ...]
"""
import sys
import os
try:
    import yaml
except ImportError:
    sys.exit("PyYAML required")


def subchart_names(chart_dir):
    """Components the distribution carries, which an Application enables.

    A component is no longer its own published chart (ADR-063): it is a subchart
    of the one distribution, so a reference is satisfied by the subchart
    existing, not by a chart of that name being published.
    """
    charts = os.path.join(chart_dir, "charts")
    if not os.path.isdir(charts):
        return set()
    return {n for n in os.listdir(charts)
            if os.path.exists(os.path.join(charts, n, "Chart.yaml"))}


def chart_names(dirs):
    """What the release actually publishes, by Chart.yaml name.

    By name rather than by directory, because helm pushes what Chart.yaml
    declares: the bundle is built in a directory called platform-bundle and
    published as environment-manager, so comparing directory names would report
    a chart present that no tenant can pull.
    """
    found = {}
    for d in dirs:
        for entry in sorted(os.listdir(d)):
            meta = os.path.join(d, entry, "Chart.yaml")
            if not os.path.exists(meta):
                continue
            with open(meta) as handle:
                name = (yaml.safe_load(handle) or {}).get("name")
            if name:
                found[name] = entry
    return found


def referenced(docs):
    """Every chart a released bundle expects to pull, with what names it."""
    want = set()
    for doc in docs:
        if not doc or doc.get("kind") != "ApplicationSet":
            continue
        appset = doc["metadata"]["name"]
        for gen in doc["spec"].get("generators", []):
            for el in (gen.get("list") or {}).get("elements", []) or []:
                app = el.get("appName")
                if not app:
                    continue
                # An element carrying its own repoURL names a third-party chart
                # and is not ours to publish. An empty one falls back to the
                # bundle registry, where the chart is named for the app.
                if el.get("repoURL"):
                    continue
                want.add((el.get("chart") or app, appset))
    return want


def main() -> int:
    if len(sys.argv) < 2:
        print(__doc__)
        return 2
    published = chart_names(sys.argv[1:])
    # A component enabled inside the distribution is satisfied by its subchart.
    for directory in sys.argv[1:]:
        for name in published.copy():
            if name == "platform":
                for sub in subchart_names(os.path.join(directory, name)):
                    published.setdefault(sub, f"platform/charts/{sub}")
    want = referenced(yaml.safe_load_all(sys.stdin))
    missing = sorted((c, a) for c, a in want if c not in published)

    if not missing:
        print(f"bundle completeness: {len(want)} referenced chart(s), all published")
        return 0

    print(f"bundle completeness: {len(missing)} referenced chart(s) are not published")
    for chart, appset in missing:
        print(f"  {chart}  (named by {appset})")
    print("A cluster reconciling this bundle would fail to resolve them.")
    return 1


if __name__ == "__main__":
    sys.exit(main())
