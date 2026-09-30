#!/usr/bin/env python3
"""Every cross-application dependency must be declared by BOTH sides.

ADR-088's amendment introduced the declaration and named this gap explicitly:

    "It does not discover dependencies, and it does not verify that the callee
     agreed: a dependsOn naming an app whose chart declares no matching ingress
     renders a valid policy and a call that hangs."

This is that verification. A caller declaring `identity.backendDependencies:
[waypoint]` gets egress and an audience scope; the target declaring
`identity.allowedConsumers: [oranger]` gets ingress and admits the caller's azp.
Either alone is a half-built path, and the two failure shapes are both quiet:

  caller declares, target does not
      egress opens, ingress does not. Cilium DROPS denied ingress without an RST
      (ADR-046 section 30), so the call HANGS to its timeout rather than being
      refused. The token is minted correctly, the receiver is healthy, and
      nothing in either application reports a policy.

  target declares, caller does not
      the target admits a caller that never arrives, and the platform renders no
      audience scope for it -- so the day someone adds the calling code, the
      exchange fails with invalid_target naming an audience rather than a missing
      declaration.

Checked against the tenant GitOps repository, where both halves are declared.

Emits tab-separated lines the shell wrapper classifies:
    OK<TAB><message>
    BAD<TAB><message>
    NONE
"""
import os
import sys
from pathlib import Path

import yaml

REPO = os.environ.get("GITOPS_DIR", ".local-e2e/nutgraf-gitops")


def declarations(root: Path):
    """Every app's two dependency declarations, keyed (environment, appId)."""
    out = {}
    envs = root / "environments"
    if not envs.is_dir():
        return out
    for env_dir in sorted(p for p in envs.iterdir() if p.is_dir()):
        for app_dir in sorted(p for p in env_dir.iterdir() if p.is_dir()):
            values = app_dir / "values.yaml"
            if not values.is_file():
                continue
            try:
                doc = yaml.safe_load(values.read_text()) or {}
            except yaml.YAMLError as exc:
                print(f"BAD\t{values}: not parseable as YAML ({exc})")
                continue
            identity = doc.get("identity") or {}
            out[(env_dir.name, app_dir.name)] = (
                list(identity.get("backendDependencies") or []),
                list(identity.get("allowedConsumers") or []),
            )
    return out


def main() -> int:
    root = Path(REPO)
    if not root.is_dir():
        print("NONE")
        return 0

    decls = declarations(root)
    if not decls:
        print("NONE")
        return 0

    pairs = 0
    bad = False
    for (env, app), (deps, _) in sorted(decls.items()):
        for target in deps:
            pairs += 1
            key = (env, target)
            if key not in decls:
                print(
                    f"BAD\t{env}/{app} declares a dependency on {target!r}, which is not an "
                    f"application of this tenant in {env}. A dependency names a SIBLING "
                    f"application (ADR-088); cross-tenant gains no mechanism."
                )
                bad = True
                continue
            _, consumers = decls[key]
            if app not in consumers:
                print(
                    f"BAD\t{env}/{app} -> {target}: {target} does not list {app!r} in "
                    f"identity.allowedConsumers. Egress would open and ingress would not, "
                    f"and Cilium drops denied ingress WITHOUT an RST -- so the call hangs "
                    f"to its timeout instead of being refused."
                )
                bad = True
            else:
                print(f"OK\t{env}/{app} -> {target}: both halves declared")

    # The reverse direction: an admission nobody asked for.
    for (env, app), (_, consumers) in sorted(decls.items()):
        for caller in consumers:
            key = (env, caller)
            deps = decls.get(key, ([], []))[0]
            if app not in deps:
                print(
                    f"BAD\t{env}/{app} admits {caller!r}, but {caller} declares no dependency "
                    f"on {app}. The platform renders no audience scope for a call nobody "
                    f"declared, so the exchange would fail with invalid_target."
                )
                bad = True

    if pairs == 0 and not bad:
        print("OK\tno cross-application dependencies declared")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
