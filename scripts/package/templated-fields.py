#!/usr/bin/env python3
"""Split a rendered component into verbatim content and templated objects.

The spoke catalogue is reconciled by every spoke, and a few of its fields must
differ per spoke: which Infisical path holds its credentials, which DNS zone it
may write, which backup prefix it owns. As a Kustomize source those were patches
on the Application. A published bundle makes the source a Helm chart, and ArgoCD
permits one source type per source, so the patches have nowhere to live and the
fields must come from chart values instead.

Objects named in the field declaration are emitted as Helm templates with those
paths substituted. Everything else -- the CRDs and the bulk of the catalogue --
stays verbatim under files/, because templating content that contains Go
delimiters of its own has silently blanked a credential here before.

An object named in the declaration that the render does not contain is an error,
not a no-op: a Kustomize patch whose target is missing fails the build, and
losing that would turn a stale declaration into a spoke reading another spoke's
secrets.

Delimiters already in a templated object are escaped before it becomes a
template. An ExternalSecret's template block legitimately contains `{{ .password }}`,
which is External Secrets Operator's syntax for the fetched value and not Helm's
-- rendered as Helm it becomes the empty string, and the credential is blanked
with no error anywhere. That is the whole reason verbatim content lives under
files/; an object that must be templated cannot, so its own delimiters are
protected instead.

Usage: templated-fields.py <rendered.yaml> <declaration.yaml> <chart-dir>
"""
import os
import re
import sys

try:
    import yaml
except ImportError:
    sys.exit("PyYAML required")


def protect(node):
    """Escape delimiters that belong to something other than Helm.

    Returned through Helm's own `{{ "..." }}` so the literal survives rendering.
    """
    if isinstance(node, dict):
        return {k: protect(v) for k, v in node.items()}
    if isinstance(node, list):
        return [protect(v) for v in node]
    if isinstance(node, str) and ("{{" in node or "}}" in node):
        # One pass: replacing "{{" first would insert a "}}" that the second
        # replacement then rewrites, nesting the escape inside itself.
        return re.sub(r"\{\{|\}\}", lambda m: '{{ "%s" }}' % m.group(0), node)
    return node


def assign(doc, path, value):
    """Set a nested path, indexing lists by integer, as the patches did."""
    node = doc
    for step in path[:-1]:
        node = node[step]
    node[path[-1]] = value


def main() -> int:
    if len(sys.argv) != 4:
        print(__doc__)
        return 2
    rendered, declaration, chart = sys.argv[1], sys.argv[2], sys.argv[3]

    with open(rendered) as handle:
        docs = [d for d in yaml.safe_load_all(handle) if d]
    with open(declaration) as handle:
        wanted = yaml.safe_load(handle) or []

    index = {}
    for doc in docs:
        index[(doc.get("kind"), (doc.get("metadata") or {}).get("name"))] = doc

    templated, verbatim, missing = [], [], []
    claimed = set()
    for spec in wanted:
        key = (spec["kind"], spec["name"])
        doc = index.get(key)
        if doc is None:
            missing.append(f"{spec['kind']}/{spec['name']}")
            continue
        # Escape first, then assign: the values being assigned are Helm
        # expressions and must stay live.
        for field_name in list(doc):
            doc[field_name] = protect(doc[field_name])
        for field in spec["fields"]:
            try:
                assign(doc, field["path"], field["value"])
            except (KeyError, IndexError, TypeError) as exc:
                missing.append(
                    f"{spec['kind']}/{spec['name']} path {'.'.join(map(str, field['path']))}: {exc}")
        claimed.add(key)
        templated.append(doc)

    if missing:
        print("the templated-field declaration names content this component does "
              "not render:", file=sys.stderr)
        for item in missing:
            print(f"  {item}", file=sys.stderr)
        print("A declaration that no longer matches is how a spoke ends up reading "
              "another spoke's secrets.", file=sys.stderr)
        return 1

    # By kind and name, not object identity: a document that was escaped is a
    # different object, and identity matching would leave it in both halves.
    for doc in docs:
        if (doc.get("kind"), (doc.get("metadata") or {}).get("name")) not in claimed:
            verbatim.append(doc)

    os.makedirs(os.path.join(chart, "templates"), exist_ok=True)
    with open(os.path.join(chart, "templates", "templated.yaml"), "w") as handle:
        handle.write(
            "{{- /*\n"
            "Objects whose content differs per spoke. Everything else in this\n"
            "component is emitted verbatim from files/ by content.yaml.\n"
            "*/ -}}\n"
        )
        for doc in templated:
            handle.write("---\n" + yaml.safe_dump(doc, sort_keys=False, width=10**6))

    with open(rendered, "w") as handle:
        yaml.safe_dump_all(verbatim, handle, sort_keys=False, width=10**6)

    print(f"  {len(templated)} object(s) templated, {len(verbatim)} verbatim")
    return 0


if __name__ == "__main__":
    sys.exit(main())
