#!/usr/bin/env python3
"""ADR-096: the spoke's publicHosts aggregate must EQUAL the app declarations.

The shared :443 Gateway has one owner, and its listener list comes from a
generated aggregate rather than from each app's own Application. That removes
the ownership fight that flapped public DNS -- and replaces it with a new way to
be wrong: the aggregate can drift from what the apps actually declare.

Drift is silent in both directions, which is why this is checked rather than
trusted:

    missing    an app declares a host, the aggregate omits it
               -> no listener, the hostname does not resolve, and nothing
                  anywhere reports an error. This is the failure the whole
                  redesign was triggered by, arriving through a different door.

    stale      the aggregate carries a host no app declares any more
               -> a listener referencing a Certificate that will not be renewed,
                  terminating TLS for a name nothing serves.

    duplicate  two apps on one spoke declare the same hostname
               -> two Certificates for one name, and one listener whose
                  certificateRef is whichever app the producer happened to emit
                  last. Rejected outright: a hostname has one owner.

So the invariant is exact set equality, not containment.

Run from a checkout that also has the instance (gitops) repository, since the
declarations live there. With no instance repository to read, this exits 0 and
says so -- a platform-only checkout has nothing to compare.
"""
import os
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    print("096-public-hosts-aggregate: pyyaml unavailable; skipping", file=sys.stderr)
    sys.exit(0)


def load(path):
    try:
        with open(path) as fh:
            return yaml.safe_load(fh) or {}
    except (OSError, yaml.YAMLError) as exc:
        print(f"  cannot read {path}: {exc}", file=sys.stderr)
        return None


def declared_hosts(instance_root):
    """host -> [app paths declaring it], from every app's own values.yaml."""
    out = {}
    env_root = instance_root / "environments"
    if not env_root.is_dir():
        return out
    for values in sorted(env_root.glob("*/*/values.yaml")):
        doc = load(values)
        if doc is None:
            continue
        hosts = ((doc.get("public") or {}).get("hosts")) or []
        for host in hosts:
            out.setdefault(str(host), []).append(str(values.relative_to(instance_root)))
    return out


def aggregate_hosts(instance_root, spoke):
    """host -> tlsSecret, from the generated aggregate for one spoke."""
    path = (instance_root / "registry" / "clusters" / spoke /
            "generated" / "values" / "public-hosts.yaml")
    if not path.is_file():
        return None, path
    doc = load(path)
    if doc is None:
        return None, path
    entries = doc.get("publicHosts") or []
    out = {}
    for entry in entries:
        if not isinstance(entry, dict) or not entry.get("host"):
            print(f"  malformed publicHosts entry in {path}: {entry!r}", file=sys.stderr)
            return {}, path
        out[str(entry["host"])] = entry.get("tlsSecret")
    return out, path


def main():
    instance = os.environ.get("INSTANCE_REPO") or os.environ.get("GITOPS_DIR")
    if not instance:
        for candidate in (Path(".local-e2e/nutgraf-gitops"),
                          Path("../nutgraf-gitops")):
            if (candidate / "registry" / "clusters").is_dir():
                instance = str(candidate)
                break
    if not instance:
        print("096-public-hosts-aggregate: no instance repository found; nothing to compare")
        return 0

    root = Path(instance)
    clusters = root / "registry" / "clusters"
    if not clusters.is_dir():
        print(f"096-public-hosts-aggregate: {clusters} is not a directory; skipping")
        return 0

    declared = declared_hosts(root)
    failures = []

    # A hostname belongs to exactly one app. Checked before the per-spoke
    # comparison, because a duplicate makes "the expected set" ambiguous.
    for host, sources in sorted(declared.items()):
        if len(sources) > 1:
            failures.append(
                f"hostname {host} is declared by {len(sources)} apps "
                f"({', '.join(sources)}); a public hostname has one owner, and "
                f"two would race for one listener's certificateRef")

    spokes = [d.name for d in sorted(clusters.iterdir()) if d.is_dir()]
    # Every declaration must land on some spoke's aggregate. With one spoke this
    # is exact equality; with several, a host may legitimately belong to another
    # spoke, so the union is what must account for every declaration.
    covered = set()
    for spoke in spokes:
        agg, path = aggregate_hosts(root, spoke)
        if agg is None:
            continue
        covered |= set(agg)
        for host, secret in sorted(agg.items()):
            if host not in declared:
                failures.append(
                    f"{path}: carries {host}, which no app declares. A stale entry "
                    f"leaves a listener on a certificate nothing renews")
            if not secret:
                failures.append(f"{path}: {host} has no tlsSecret; the listener cannot terminate TLS")

    for host, sources in sorted(declared.items()):
        if host not in covered:
            failures.append(
                f"{host} is declared by {sources[0]} but appears in no spoke's "
                f"generated/values/public-hosts.yaml. The Gateway renders no "
                f"listener for it and the hostname will not resolve")

    if failures:
        print("ADR-096: the generated publicHosts aggregate does not match the "
              "app declarations:", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        print("\nThe aggregate has ONE producer and is never hand-edited; "
              "re-run the aggregation rather than editing it.", file=sys.stderr)
        return 1

    print(f"096-public-hosts-aggregate: {len(declared)} declared host(s) across "
          f"{len(spokes)} spoke(s), aggregate exact")
    return 0


if __name__ == "__main__":
    sys.exit(main())
