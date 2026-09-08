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

Usage: verify-equivalence.py <descriptor.yaml> <chart-dir>
Exit 0 identical, 1 differing, 2 unusable input.
"""
import sys, subprocess, fnmatch, pathlib
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


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__)
        return 2
    descriptor, chart = pathlib.Path(sys.argv[1]), sys.argv[2]
    text = descriptor.read_text()
    app, src = field(text, "appName"), field(text, "path")
    # A component already consuming its published chart has no `path`: that field
    # names the chart, and the content it was built from is declared as
    # sourcePath. Reading only `path` made this silently SKIP exactly the
    # components whose equivalence matters most -- the ones already published.
    if not src:
        src = field(text, "sourcePath")
    if not app or not src:
        print(f"  SKIP       {descriptor.name}: no appName or no path")
        return 0

    root = pathlib.Path(src)
    if (root / "kustomization.yaml").exists():
        # --enable-helm matches how the packager builds: several components
        # inflate a chart from their kustomization, and without it the build
        # fails with "trouble configuring builtin HelmChartInflationGenerator",
        # which reads as a malformed config rather than a missing flag.
        built = subprocess.run(["kustomize", "build", "--enable-helm", src], capture_output=True, text=True)
        if built.returncode:
            print(f"  ERROR      {app}: kustomize build failed: {built.stderr.strip()[:120]}")
            return 2
        expected = list(yaml.safe_load_all(built.stdout))
    else:
        pattern = field(text, "directoryInclude") or "*.yaml"
        candidates = root.rglob("*") if field(text, "directoryRecurse") == "true" else root.glob("*")
        expected = []
        for f in sorted(p for p in candidates if p.is_file() and fnmatch.fnmatch(p.name, pattern)):
            expected.extend(yaml.safe_load_all(f.read_text()))

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
    print(f"  DIFFERS    {app}  expected={len(exp_keys)} actual={len(act_keys)}")
    for missing in sorted(exp_keys - act_keys):
        print(f"               only in source: {missing}")
    for extra in sorted(act_keys - exp_keys):
        print(f"               only in chart:  {extra}")
    return 1


if __name__ == "__main__":
    sys.exit(main())
