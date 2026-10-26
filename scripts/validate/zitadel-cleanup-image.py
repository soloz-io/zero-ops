#!/usr/bin/env python3
"""The cleanup initContainer's image must match the Zitadel chart's appVersion.

`setupJob.initContainers` is a raw container list, so it cannot reference the
chart's own image values -- the tag has to be written out. That makes it the one
place in this deployment where a chart bump does NOT carry its image along, and a
cleanup run by a different binary than the setup it precedes is reasoning about a
set of migrations it may not know.

Checked without network by default: the chart version is read from the
ApplicationSet and the appVersion from the comment that records the mapping, so
the two must be edited together. Pass --online to verify that recorded mapping
against the registry itself.
"""
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
APPSET = ROOT / "manifests/argocd/environment-manager/templates/04-tenant-services-multi-appset.yaml"
VALUES = ROOT / "manifests/hub-core-services/identity/zitadel/values.yaml"

failures = []


def fail(msg):
    failures.append(msg)


appset = APPSET.read_text()
m = re.search(r"appName: 'zitadel'.*?targetRevision: '([^']+)'", appset, re.S)
if not m:
    fail(f"could not find the zitadel chart version in {APPSET.relative_to(ROOT)}")
    chart_version = None
else:
    chart_version = m.group(1)

values = VALUES.read_text()
m = re.search(r"#\s*chart\s+(\S+)\s*->\s*appVersion\s+(\S+)", values)
if not m:
    fail("values.yaml carries no `# chart <version> -> appVersion <tag>` note, so "
         "the pin records nothing about which chart it belongs to")
    noted_chart = noted_app = None
else:
    noted_chart, noted_app = m.group(1), m.group(2)

# EVERY pin, not the first. setupJob.initContainers carries more than one
# container on the same image, and a check that stopped at the first would pass
# while a second sat on a different version.
pins = re.findall(r"image:\s*ghcr\.io/zitadel/zitadel:(\S+)", values)
if not pins:
    fail("no pinned zitadel image found in values.yaml")
distinct = sorted(set(pins))
if len(distinct) > 1:
    fail(f"the zitadel initContainers are pinned at different versions {distinct}; "
         "they run the same binary against the same database and must match")
pinned = distinct[0] if distinct else None

if chart_version and noted_chart and chart_version != noted_chart:
    fail(f"the chart is pinned at {chart_version} but the cleanup image note still "
         f"says {noted_chart}; re-check the appVersion and update both")

if pinned and noted_app and pinned != noted_app:
    fail(f"the cleanup image is {pinned} but the note records appVersion {noted_app}")

if "--online" in sys.argv and chart_version:
    out = subprocess.run(
        ["helm", "show", "chart", "zitadel", "--repo",
         "https://charts.zitadel.com", "--version", chart_version],
        capture_output=True, text=True)
    if out.returncode != 0:
        print("zitadel-cleanup-image: could not reach the chart repository; "
              "offline checks still passed", file=sys.stderr)
    else:
        m = re.search(r"^appVersion:\s*(\S+)", out.stdout, re.M)
        real = m.group(1) if m else None
        if real and pinned != real:
            fail(f"chart {chart_version} has appVersion {real}, but the cleanup "
                 f"image is pinned at {pinned}")

for f in failures:
    print(f"error: {f}", file=sys.stderr)
print(f"zitadel-cleanup-image: chart={chart_version} image={pinned} "
      f"pins={len(pins)} failures={len(failures)}")
sys.exit(1 if failures else 0)
