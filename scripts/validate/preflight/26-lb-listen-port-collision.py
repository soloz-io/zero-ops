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
    "manifests/providers/_shared/*.yaml",
    "manifests/providers/*/*.yaml",
    "internal/assets/manifests/classes/*.yaml",
]

found = 0
clean = True

# `manifests/providers/_shared/` is matched by two of the patterns above, so
# resolve and de-duplicate before scanning — otherwise one defect reports twice
# and the "N templates" count is inflated.
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
            clean = False
            ports = ", ".join(str(e.get("listenPort")) for e in collisions)
            print(f"BAD\t{name}\tcontrolPlaneEndpoint.port={endpoint_port} collides "
                  f"with extraServices listenPort {ports} — CAPH will silently drop "
                  f"the extraService and the apiserver will answer on that port "
                  f"(ADR-046 §25.1)")
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
    print(f"OK\t{found} HetznerClusterTemplate(s): apiserver listen port does not "
          f"collide with any extraServices listenPort")
