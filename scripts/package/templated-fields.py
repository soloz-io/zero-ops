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

A field either assigns a whole value (`value:`), substitutes a literal inside
one (`replace: {from:, with:}`) for values buried in an opaque blob no path can
address, or substitutes it throughout the object (`replaceAll: {from:, with:}`)
for objects that carry the same per-instance value in every field.

A component may render into several files. All of them are passed at once and a
declared object must appear in exactly one of them: checked per file, a
declaration would have to name objects that live in a different file, so every
multi-file component would fail on its first entry.

Usage: templated-fields.py <declaration.yaml> <chart-dir> <rendered.yaml>...
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


def resolve(doc, path):
    """Return (container, key) for a nested path, indexing lists by integer."""
    node = doc
    for step in path[:-1]:
        node = node[step]
    return node, path[-1]


def assign(doc, path, value):
    """Set a nested path, indexing lists by integer, as the patches did."""
    node, key = resolve(doc, path)
    node[key] = value


def substitute_all(node, literal, replacement):
    """Replace a literal in every string under a node, returning a count.

    Some objects carry a per-instance value in every field rather than in one:
    a Certificate's dnsNames are all the same domain, and so are a ClusterIssuer's
    solver zones. Naming each index would make the declaration a list of numbers
    that silently goes stale when an entry is inserted -- the opposite of the
    reviewable contract this file is meant to be. Naming the object and the
    literal says the same thing and survives the edit.
    """
    if isinstance(node, dict):
        # apiVersion and kind identify the TYPE, never the instance, and a group
        # that contains the vendor's domain is still a group: ops.nutgraf.in is
        # the API this platform serves, exactly as cluster.x-k8s.io is upstream's.
        # Substituting there rewrote apiVersion to ops.<tenant domain>/v1alpha1
        # and ArgoCD refused the object -- "could not find version v1alpha1 of
        # ops./HubEnvironment" -- after the bundle had published and six
        # boundaries had deployed.
        #
        # metadata.name for the same reason the autoscaler's name was left alone:
        # renaming an object by value makes the resource ArgoCD prunes depend on
        # that value, so a change deletes and recreates rather than updates.
        return sum(substitute_all_in(node, k, literal, replacement)
                   for k in list(node) if k not in ("apiVersion", "kind"))
    if isinstance(node, list):
        return sum(substitute_all_in(node, i, literal, replacement) for i in range(len(node)))
    return 0


def substitute_all_in(container, key, literal, replacement):
    if key == "metadata" and isinstance(container.get(key), dict):
        meta = container[key]
        return sum(substitute_all_in(meta, k, literal, replacement)
                   for k in list(meta) if k != "name")
    v = container[key]
    if isinstance(v, str):
        if literal in v:
            container[key] = v.replace(literal, replacement)
            return 1
        return 0
    return substitute_all(v, literal, replacement)


def substitute(doc, path, literal, replacement):
    """Replace a literal inside the string at a path, leaving the rest verbatim.

    Some per-instance values are not addressable as a field. agentgateway's
    config is one opaque `config.yaml` string inside a ConfigMap, and the hub's
    domain appears three times inside it -- as the OIDC issuer, the required
    audience, and the JWKS URL. Assigning the whole blob would mean carrying a
    two-hundred-line config in the declaration, which stops being reviewable and
    so stops being the contract this file exists to be.

    Refuses a literal that is not present. A declaration that silently matches
    nothing is how a tenant's cluster keeps pointing at the platform's identity
    provider while the packager reports success -- which is the defect this was
    added for.
    """
    node, key = resolve(doc, path)
    text = node[key]
    if not isinstance(text, str):
        raise TypeError(f"not a string, cannot substitute: {type(text).__name__}")
    if literal not in text:
        raise KeyError(f"literal {literal!r} not present")
    node[key] = text.replace(literal, replacement)


def main() -> int:
    if len(sys.argv) < 4:
        print(__doc__)
        return 2
    declaration, chart, rendered_files = sys.argv[1], sys.argv[2], sys.argv[3:]

    by_file = {}
    docs = []
    for rendered in rendered_files:
        with open(rendered) as handle:
            these = [d for d in yaml.safe_load_all(handle) if d]
        by_file[rendered] = these
        docs.extend(these)
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
                if "replaceAll" in field:
                    # No path: the literal is replaced wherever it appears in the
                    # object. Refused when absent, like the addressed form.
                    n = substitute_all(doc, field["replaceAll"]["from"],
                                       field["replaceAll"]["with"])
                    if n == 0:
                        raise KeyError(
                            f"literal {field['replaceAll']['from']!r} not present anywhere")
                elif "replace" in field:
                    substitute(doc, field["path"],
                               field["replace"]["from"], field["replace"]["with"])
                else:
                    assign(doc, field["path"], field["value"])
            except (KeyError, IndexError, TypeError) as exc:
                # A replaceAll field has no path, so describing one by its path
                # raised a second KeyError inside the handler and the packager
                # died with a traceback instead of naming the declaration.
                where = ('.'.join(map(str, field["path"]))
                         if "path" in field else "anywhere in the object")
                missing.append(f"{spec['kind']}/{spec['name']} {where}: {exc}")
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
    remaining = {}
    for rendered, these in by_file.items():
        keep = [d for d in these
                if (d.get("kind"), (d.get("metadata") or {}).get("name")) not in claimed]
        remaining[rendered] = keep
        verbatim.extend(keep)

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

    for rendered, keep in remaining.items():
        with open(rendered, "w") as handle:
            yaml.safe_dump_all(keep, handle, sort_keys=False, width=10**6)

    print(f"  {len(templated)} object(s) templated, {len(verbatim)} verbatim")
    return 0


if __name__ == "__main__":
    sys.exit(main())
