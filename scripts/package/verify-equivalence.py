#!/usr/bin/env python3
"""Assert a packaged component chart renders exactly what ArgoCD applies today.

Packaging a component is only safe if the objects do not change, so this is the
gate on the conversion rather than a convenience. It compares the rendered chart
against the component's current source resolved the way the boundary
ApplicationSet resolves it: `kustomize build` when the path carries a
kustomization, otherwise the files matching directoryInclude at the depth
directoryRecurse allows.

Objects are compared as parsed and sorted YAML, not as text, because Helm's
document separators and key ordering differ from the source without any object
differing. An earlier version of this check concatenated the source files and
parsed the result, which silently merged the trailing document of one file into
the leading document of the next wherever a file did not begin with `---`, and
reported two false differences.

Some charts do not render the same twice. Infisical's vendored chart stamps
`{{ now }}` into an annotation, so any two renders differ by the seconds between
them and a comparison of one render against another can never succeed. Fields
like that are found by rendering the source a second time and taking what moved,
rather than by naming them here -- naming them would mean this check stops
looking at a field for every component, including ones where a change in it
would be real. The unstable fields are excluded from the comparison and printed,
so an ignored field stays visible rather than becoming a silent exemption.

That second render waits out a clock tick first. Without the wait it usually
lands in the same second as the first, `now` formats identically, and a
timestamp that moves on every real render is judged stable -- so the check fails
or passes depending on which second the runner happened to be in. A gate that
depends on timing is worse than no gate, because it teaches people to re-run it.

Usage: verify-equivalence.py <descriptor.yaml> <chart-dir>
Exit 0 identical, 1 differing, 2 unusable input.
"""
import sys, time, subprocess, fnmatch, pathlib
try:
    import yaml
except ImportError:
    sys.exit("PyYAML required")


def field(text, name):
    for line in text.splitlines():
        if line.startswith(name + ":"):
            return line.split(":", 1)[1].strip().strip("'\"")
    return ""


def key(doc):
    return doc.get("kind", ""), (doc.get("metadata") or {}).get("name", "")


def normalise(docs):
    docs = [d for d in docs if d]
    docs.sort(key=key)
    return yaml.safe_dump_all(docs, sort_keys=True), {key(d) for d in docs}


def flatten(docs):
    """Every leaf as object/dotted.path -> value, so differences can be named."""
    out = {}

    def walk(prefix, node):
        if isinstance(node, dict):
            for k, v in node.items():
                walk(f"{prefix}.{k}", v)
        elif isinstance(node, list):
            for i, v in enumerate(node):
                walk(f"{prefix}[{i}]", v)
        else:
            out[prefix] = node

    for d in docs:
        if not d:
            continue
        kind, name = key(d)
        walk(f"{kind}/{name}", d)
    return out


def render_source(descriptor_text, src):
    """Resolve the component the way the boundary ApplicationSet resolves it."""
    root = pathlib.Path(src)
    if (root / "kustomization.yaml").exists():
        # --enable-helm matches how the packager builds: several components
        # inflate a chart from their kustomization, and without it the build
        # fails with "trouble configuring builtin HelmChartInflationGenerator",
        # which reads as a malformed config rather than a missing flag.
        built = subprocess.run(["kustomize", "build", "--enable-helm", src],
                               capture_output=True, text=True)
        if built.returncode:
            return None, f"kustomize build failed: {built.stderr.strip()[:120]}"
        return list(yaml.safe_load_all(built.stdout)), None

    pattern = field(descriptor_text, "directoryInclude") or "*.yaml"
    recurse = field(descriptor_text, "directoryRecurse") == "true"
    candidates = root.rglob("*") if recurse else root.glob("*")
    docs = []
    for f in sorted(p for p in candidates if p.is_file() and fnmatch.fnmatch(p.name, pattern)):
        docs.extend(yaml.safe_load_all(f.read_text()))
    return docs, None


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__)
        return 2
    descriptor, chart = pathlib.Path(sys.argv[1]), sys.argv[2]
    text = descriptor.read_text()
    app, src = field(text, "appName"), field(text, "path")
    if not app or not src:
        print(f"  SKIP       {descriptor.name}: no appName or no path")
        return 0

    expected, err = render_source(text, src)
    if err:
        print(f"  ERROR      {app}: {err}")
        return 2

    rendered = subprocess.run(["helm", "template", app, chart], capture_output=True, text=True)
    if rendered.returncode:
        print(f"  ERROR      {app}: helm template failed: {rendered.stderr.strip()[:120]}")
        return 2
    actual = list(yaml.safe_load_all(rendered.stdout))

    exp_text, exp_keys = normalise(expected)
    act_text, act_keys = normalise(actual)
    if exp_text == act_text:
        print(f"  IDENTICAL  {app}  ({len(exp_keys)} objects)")
        return 0

    # Something differs. Before reporting it, find what this source renders
    # differently from itself -- only the remainder is a difference the
    # packaging caused. The second render happens here rather than always, so
    # components that match pay nothing for it.
    # The wait guarantees the second render falls in a later second, so a
    # timestamp formatted to seconds is certain to move if it is time-derived.
    time.sleep(1.1)
    again, err = render_source(text, src)
    unstable = set()
    if not err:
        first, second = flatten(expected), flatten(again)
        unstable = {p for p in first.keys() & second.keys() if first[p] != second[p]}

    exp_flat, act_flat = flatten(expected), flatten(actual)
    differing = sorted(
        p for p in exp_flat.keys() | act_flat.keys()
        if p not in unstable and exp_flat.get(p) != act_flat.get(p)
    )

    if not differing and exp_keys == act_keys:
        note = f", ignoring {len(unstable)} field(s) the source does not render the same twice"
        print(f"  IDENTICAL  {app}  ({len(exp_keys)} objects{note})")
        for p in sorted(unstable)[:4]:
            print(f"               unstable: {p}")
        return 0

    print(f"  DIFFERS    {app}  expected={len(exp_keys)} actual={len(act_keys)}")
    for missing in sorted(exp_keys - act_keys):
        print(f"               only in source: {missing}")
    for extra in sorted(act_keys - exp_keys):
        print(f"               only in chart:  {extra}")
    for p in differing[:8]:
        print(f"               {p}: source={exp_flat.get(p)!r} chart={act_flat.get(p)!r}")
    if len(differing) > 8:
        print(f"               ... and {len(differing) - 8} more field(s)")
    return 1


if __name__ == "__main__":
    sys.exit(main())
