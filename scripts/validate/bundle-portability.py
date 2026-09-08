#!/usr/bin/env python3
"""Fail a release whose bundle still fetches from the platform's repository.

Reads a rendered bundle on stdin.

ADR-063 requires every artefact a tenant's runtime needs to be mirrored into
infrastructure the tenant controls before that runtime is supported, and ADR-065
that revoking the platform's access stops proposals arriving and stops nothing
running. Both are false for anything a released cluster still reaches into the
platform's repository for: the bundle is then portable only in appearance, and
the gap stays invisible until the day that repository is not reachable.

Exits non-zero when any such reference remains, so a release cannot ship one.
"""
import sys
import yaml

PLATFORM_REPO = "zero-ops"


def main() -> int:
    refs = set()
    for doc in yaml.safe_load_all(sys.stdin):
        if not doc or doc.get("kind") != "ApplicationSet":
            continue
        name = doc["metadata"]["name"]

        def note(kind: str, url: str) -> None:
            if url and PLATFORM_REPO in url and not url.startswith("oci://"):
                refs.add((name, kind))

        for gen in doc["spec"].get("generators", []):
            for key, value in gen.items():
                if key == "git":
                    note("generator", value.get("repoURL", ""))
                elif key == "matrix":
                    for sub in value.get("generators", []):
                        if "git" in sub:
                            note("generator", sub["git"].get("repoURL", ""))
        spec = doc["spec"]["template"]["spec"]
        for src in [spec["source"]] if "source" in spec else spec.get("sources", []):
            note("source", src.get("repoURL", ""))

    if not refs:
        print("bundle portability: nothing reaches the platform repository")
        return 0
    print(f"bundle portability: {len(refs)} reference(s) still reach the platform repository")
    for name, kind in sorted(refs):
        print(f"  {name} ({kind})")
    print()
    print("Each is content a released cluster cannot obtain from the bundle alone,")
    print("so this bundle cannot be mirrored into a tenant's own infrastructure and")
    print("the custody ADR-063 states would be asserted and untrue for every tenant")
    print("that received it. A release carrying one of these is refused.")
    return 1


if __name__ == "__main__":
    sys.exit(main())
