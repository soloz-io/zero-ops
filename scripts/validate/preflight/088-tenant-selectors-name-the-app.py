#!/usr/bin/env python3
"""Every pod selector a tenant composition renders names the application.

A tenant namespace holds one application (ADR-088), but it can still hold pods
the composition no longer renders: renaming a composed object makes
provider-kubernetes create the new one and leave the old one running. A selector
that matches on the component alone picks up that leftover.

On 2026-10-15 that is what lost wake-ups. The ADR-088 rename left
nutgraf-waypoint-cache beside waypoint-cache in tenant-nutgraf-waypoint. Both
Services selected `app.kubernetes.io/component: cache`, so each fronted two
independent Redis servers and every connection landed on either. The SDK
published to one; browsers subscribed on the other; nothing errored.

So each Service selector, workload selector and pod template a tenant
composition renders must carry `app-id`, patched from spec.appId.

Emits tab-separated lines the shell wrapper classifies:
    OK<TAB><message>
    BAD<TAB><message>
"""
from pathlib import Path

import yaml

COMPOSITIONS = Path("manifests/hub-core-services/crossplane/tenant-platform/compositions")

# kind -> the label maps that must carry app-id, as patch target paths.
SELECTORS = {
    "Service": ["spec.selector"],
    "Deployment": ["spec.selector.matchLabels", "spec.template.metadata.labels"],
    "StatefulSet": ["spec.selector.matchLabels", "spec.template.metadata.labels"],
}


def dig(doc, dotted):
    for key in dotted.split("."):
        if not isinstance(doc, dict):
            return None
        doc = doc.get(key)
    return doc


def main():
    files = sorted(COMPOSITIONS.glob("*.yaml"))
    if not files:
        print(f"BAD\tno tenant compositions found under {COMPOSITIONS}")
        return
    for path in files:
        for doc in yaml.safe_load_all(path.read_text()):
            if not doc or doc.get("kind") != "Composition":
                continue
            for res in (doc.get("spec") or {}).get("resources") or []:
                manifest = dig(res, "base.spec.forProvider.manifest") or {}
                paths = SELECTORS.get(manifest.get("kind"))
                if not paths:
                    continue
                patched = {
                    p.get("toFieldPath")
                    for p in res.get("patches") or []
                    if p.get("fromFieldPath") == "spec.appId"
                }
                name = f"{path.name}: {res.get('name')} ({manifest['kind']})"
                missing = []
                for sel in paths:
                    labels = dig(manifest, sel)
                    target = f"spec.forProvider.manifest.{sel}.app-id"
                    if not isinstance(labels, dict) or "app-id" not in labels or target not in patched:
                        missing.append(sel)
                if missing:
                    print(f"BAD\t{name}: {', '.join(missing)} must carry app-id patched from spec.appId")
                else:
                    print(f"OK\t{name}: selects by app-id")


if __name__ == "__main__":
    main()
