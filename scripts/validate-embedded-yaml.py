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
import sys
import yaml


def validate_file(path: str) -> bool:
    ok = True
    try:
        with open(path) as f:
            text = f.read()
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


def main() -> int:
    files = sys.argv[1:]
    if not files:
        return 0
    failed = False
    for path in files:
        if not validate_file(path):
            failed = True
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())