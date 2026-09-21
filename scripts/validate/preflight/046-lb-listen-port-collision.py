#!/usr/bin/env python3
"""A Hetzner load balancer cannot carry two services on one listen_port.

`controlPlaneEndpoint.port` becomes the LB's listen port for the kube-apiserver.
Any `controlPlaneLoadBalancer.extraServices` entry declaring the same
`listenPort` is therefore silently dropped by CAPH — no error, no condition,
`LoadBalancerReady=True` throughout (ADR-046 §25.1).

That is exactly how spoke HTTPS ingress was lost: the ClusterClass declared
443->443, the endpoint was also 443, and the LB served the apiserver on 443 for
weeks while every status object read healthy.

Emits tab-separated lines the shell wrapper classifies:
    OK<TAB><message>
    BAD<TAB><name><TAB><message>
"""
import os
import sys
import glob
import yaml

# Every file that can declare a HetznerClusterTemplate.
SOURCES = [
    "manifests/providers/hetzner/base/*.yaml",
    "manifests/providers/*/*.yaml",
    "internal/assets/manifests/classes/*.yaml",
]

# Templates whose collision is REAL and KNOWN, recorded so the rule is enforceable
# for everything else while the debt stays visible — the same idiom
# 96-spoke-secret-authz.py and 99-tenant-identifiers.sh use.
#
# Remove an entry the moment its template is versioned and the collision is gone.
# The list only shrinks; a NEW template with a collision still fails hard.
BASELINE = {
    # spokepool-cluster-v1 is superseded by v2 (ADR-046 §25.5) and will be
    # pruned from the file after the spoke is re-provisioned on v2. Kept here
    # while the live resource still exists so the preflight does not fail on
    # stale state.
    "spokepool-cluster-v1",
}

found = 0
clean = True
baselined = []

# De-duplicate by real path before scanning. The Hetzner infrastructure layer used
# to sit at `manifests/providers/_shared/`, where `providers/*/*.yaml` matched it a
# second time and one defect reported twice. At `hetzner/base/` it is one level
# deeper and only the explicit pattern matches, but the de-duplication stays: the
# patterns are meant to overlap, and a future layer could collide again.
paths = sorted({os.path.realpath(p)
                for pattern in SOURCES for p in glob.glob(pattern)})

for path in paths:
    try:
        docs = list(yaml.safe_load_all(open(path)))
    except yaml.YAMLError as exc:
        print(f"BAD\t{path}\tunparseable YAML: {exc}")
        clean = False
        continue

    for doc in docs:
        if not doc or doc.get("kind") != "HetznerClusterTemplate":
            continue
        name = doc.get("metadata", {}).get("name", path)
        spec = ((doc.get("spec") or {}).get("template") or {}).get("spec") or {}

        endpoint_port = (spec.get("controlPlaneEndpoint") or {}).get("port")
        lb = spec.get("controlPlaneLoadBalancer") or {}
        extras = lb.get("extraServices") or []
        if endpoint_port is None:
            continue
        found += 1

        # The apiserver's listen port is controlPlaneEndpoint.port.
        collisions = [e for e in extras if e.get("listenPort") == endpoint_port]
        if collisions:
            ports = ", ".join(str(e.get("listenPort")) for e in collisions)
            msg = (f"controlPlaneEndpoint.port={endpoint_port} collides with "
                   f"extraServices listenPort {ports} — CAPH silently drops the "
                   f"extraService and the apiserver answers on that port")
            if name in BASELINE:
                baselined.append(f"{name}: {msg} (ADR-046 §25.1, pending §25.5 versioning)")
                continue
            clean = False
            print(f"BAD\t{name}\t{msg} (ADR-046 §25.1)")
            continue

        # Duplicate listenPorts among the extras themselves fail the same way.
        seen = {}
        for e in extras:
            lp = e.get("listenPort")
            seen[lp] = seen.get(lp, 0) + 1
        dupes = [str(pt) for pt, n in seen.items() if n > 1]
        if dupes:
            clean = False
            print(f"BAD\t{name}\tduplicate extraServices listenPort(s) "
                  f"{', '.join(dupes)} — only one survives")

if found == 0:
    print("BAD\t-\tno HetznerClusterTemplate found — the check scanned nothing")
    sys.exit(0)

if clean:
    n = found - len(baselined)
    print(f"OK\t{n} HetznerClusterTemplate(s) collision-free; no NEW listen-port "
          f"collision introduced")
for b in baselined:
    print(f"NOTE\t  known collision (ADR-046 §25.1 debt): {b}")
