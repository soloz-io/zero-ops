#!/usr/bin/env python3
"""Validate YAML embedded inside stringData block scalars of Kubernetes Secrets.

yamllint validates only the outer document and cannot see inside a block
scalar (e.g. `stringData.cilium.yaml: |`). Broken embedded content passes
yamllint but breaks kustomize build / ArgoCD manifests exactly like the
"MalformedYAMLError: could not find expected ':'" failures seen in
manifests/providers/hybrid/k8s/cilium-addon-hybrid.yaml.

Usage: validate-embedded-yaml.py FILE...
Exit 1 on any parse error with file/key/line context.
"""
import re
import pathlib
import sys
import yaml

# A Go text/template action anywhere at the start of a value, e.g.
#   name: {{.ClusterName}}-addons
# makes the file invalid plain YAML: the parser reaches `{` where it expects a
# scalar and reports a syntax error at that column. These assets are rendered by
# the CLI before they are ever applied, so parsing them here tests a form that
# never reaches a cluster and fails on every one of them.
#
# .yamllint.yaml already skips the same files by name. Detecting the templating
# instead keeps the two from drifting apart, which is what happened here:
# crs.yaml was listed there and not here, so one hook passed and the other
# failed on the identical file.
GO_TEMPLATE = re.compile(r"{{[-\s]*[.$a-zA-Z]")


def validate_file(path: str) -> bool:
    ok = True
    with open(path) as f:
        text = f.read()

    if GO_TEMPLATE.search(text):
        return True

    try:
        docs = list(yaml.safe_load_all(text))
    except yaml.YAMLError as e:
        print(f"ERROR {path}: outer document parse failed: {e}")
        return False

    for doc in docs:
        if not isinstance(doc, dict):
            continue
        string_data = doc.get("stringData")
        if not isinstance(string_data, dict):
            continue
        for key, value in string_data.items():
            if not isinstance(value, str):
                continue
            if "kind:" not in value and "apiVersion:" not in value:
                continue
            try:
                inner = list(yaml.safe_load_all(value))
                count = sum(1 for d in inner if d)
                if count == 0:
                    print(f"ERROR {path}: stringData.{key} contains no YAML documents")
                    ok = False
            except yaml.YAMLError as e:
                print(f"ERROR {path}: stringData.{key} embedded YAML parse failed: {e}")
                ok = False
    return ok


# The paths this validates, as the pre-commit hook selects them. Kept here so the
# script can run without arguments and still check something: returning 0 on an
# empty argument list made an unconditional hook a vacuous pass, which reads in the
# output exactly like a real one.
PATTERNS = (
    "manifests/*/*/k8s/*.yaml",
    "manifests/*/k8s/*.yaml",
    "manifests/*/spoke-bootstrap/*.yaml",
    "manifests/*/*/spoke-bootstrap/*.yaml",
    "internal/assets/manifests/addons/*.yaml",
)


def discover() -> list:
    """Every file the hook would have passed, found from the repository root."""
    root = pathlib.Path(__file__).resolve().parent.parent
    found = []
    for pattern in PATTERNS:
        found.extend(str(p) for p in root.glob(pattern) if p.is_file())
    return sorted(set(found))


def main() -> int:
    files = sys.argv[1:] or discover()
    if not files:
        print("ERROR: no files matched; the validator would pass without checking anything")
        return 1
    failed = False
    for path in files:
        if not validate_file(path):
            failed = True
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())