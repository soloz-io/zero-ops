#!/usr/bin/env python3
"""Every Application must receive the same globals, released or not.

The environment-manager assembles Applications two ways: from the git tree
(unreleased) and from the published distribution (released). Both must hand a
component the same `global:` block, because a global is a fact about the box and
which code path built the Application cannot change what is true of it.

They drifted. `dns` and `gitOrgURL` were added to `globalValues` and not to the
released path's copy, so every released box rendered external-dns with an empty
`--provider` and crash-looped on

    flag parsing error: enum value must be one of akamai,...,webhook, got ''

while the unreleased path -- the one a developer tests -- was correct throughout.

This refuses a second `global:` literal anywhere in the templates. One emitter
means the two paths cannot disagree.
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
TPL = ROOT / "manifests/argocd/environment-manager/templates"
CANONICAL = "_global-values.tpl"

failures = []
emitters = []
for path in sorted(TPL.glob("*.tpl")) + sorted(TPL.glob("*.yaml")):
    for i, line in enumerate(path.read_text().split("\n"), 1):
        # A `global:` at the start of an emitted block, not a reference to one.
        if re.match(r"^\s*global:\s*$", line):
            emitters.append((path.name, i))

for name, line in emitters:
    if name != CANONICAL:
        failures.append(
            f"{name}:{line} emits its own `global:` block. Globals are defined "
            f"once, in {CANONICAL}, and included -- two copies drift and the "
            f"released path is the one nobody tests.")

if not emitters:
    failures.append(f"no `global:` emitter found; {CANONICAL} should hold one")

# The other half of the same drift: a component that reads a global nobody emits
# renders it EMPTY and fails at runtime, not at render time. external-dns got
# `--provider=` this way and crash-looped on "enum value must be one of ...,
# got ''" -- a component cannot tell an unset global from an empty one.
block = re.search(r'define "environment-manager\.globalValues"(.*?)\n\{\{-\s*end\s*-\}\}',
                  (TPL / CANONICAL).read_text(), re.S)
emitted = set(re.findall(r"^\s{2}([A-Za-z][A-Za-z0-9]*):", block.group(1), re.M)) if block else set()

referenced = set()
for path in (ROOT / "manifests").rglob("templated-fields*.yaml"):
    for name in re.findall(r"\.Values\.global\.([A-Za-z0-9_]+)", path.read_text()):
        referenced.add(name)

# The include must not chomp. `{{- include ... }}` swallows the newline that
# separates the component's own values from `global:`, so they run together as
# `enabled: trueglobal:` -- and helm reports it four layers away as
# "error converting YAML to JSON: yaml: line 2: mapping values are not allowed
# in this context", naming neither the template nor the tag.
for path in TPL.glob("*.tpl"):
    for i, line in enumerate(path.read_text().split("\n"), 1):
        if "include \"environment-manager.globalValues\"" in line and line.lstrip().startswith("{{-"):
            failures.append(
                f"{path.name}:{i} includes globalValues with a chomping tag "
                f"`{{{{-`. That removes the newline before `global:` and the "
                f"values document stops parsing. Use `{{{{ include ... }}}}`.")

for name in sorted(referenced - emitted):
    failures.append(
        f"components read `global.{name}` but {CANONICAL} does not emit it; it "
        f"renders as an empty string and the component fails at runtime")

for f in failures:
    print(f"error: {f}", file=sys.stderr)
print(f"one-globals-emitter: emitters={len(emitters)} failures={len(failures)}")
sys.exit(1 if failures else 0)
