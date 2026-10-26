#!/usr/bin/env python3
"""A pinned image digest must be newer than the code it was built from.

Every operator and service is deployed by DIGEST, and nothing bumps that digest
automatically: CI builds the image and a human commits the new digest. When the
two drift, a release ships code that is present in the repository and absent
from the cluster -- and says nothing, because both halves are individually
valid.

That is not hypothetical. rc.116 shipped the chart and XRD that ACCEPT
`identity.backendDependencies` together with a hub-operator image built before
the code that resolves them. A tenant declaring a dependency would have had it
reach the XR and stop there: no audience scope, no allowed-caller list, no error
anywhere. The declaration would have read as supported and done nothing.

DERIVED, NOT ENUMERATED. The workflow that builds each image already declares
which paths trigger it, and the kustomization that pins it already names the
image. This reads both and matches them on the image name, so a new component
is covered the day its workflow exists. Three separate lists in this repository
have gone stale exactly because they were lists -- the yamllint ignore, the
release packaging's chart pair, and the promotion's version references.

The comparison is git, not the registry: no network, no credentials, and the
question is about this checkout anyway. For each image, the newest commit
touching its trigger paths is compared with the newest commit touching the file
that pins it. The pin file is excluded from its own trigger paths -- bumping a
digest is not a source change, and counting it as one would make every pin look
permanently current.
"""
import re
import subprocess
import sys
from pathlib import Path

WORKFLOWS = Path(".github/workflows")
IMAGE_RE = re.compile(r"ghcr\.io/[\w./-]+?/([\w.-]+)[:@]")


def git(*args):
    return subprocess.run(["git", *args], capture_output=True, text=True).stdout.strip()


def last_commit(paths, exclude=None):
    """Newest commit touching any of paths, ignoring `exclude`.

    Returns (sha, unix_seconds, human). The comparison uses %ct -- Unix seconds,
    which carry no timezone. %cI does, and comparing those as STRINGS is wrong in
    a way that looks right: "2026-09-28T11:52:37+05:30" sorts after
    "2026-09-28T06:25:57Z" while being three minutes EARLIER. This check reported
    four false stale pins before that was fixed, which would have taught everyone
    to ignore it.
    """
    if not paths:
        return None, None, None
    args = ["log", "-1", "--format=%H %ct %cI", "--"] + list(paths)
    if exclude:
        args += [f":(exclude){exclude}"]
    out = git(*args)
    if not out:
        return None, None, None
    sha, ts, human = out.split(" ", 2)
    return sha, int(ts), human


def workflow_images():
    """image name -> (trigger paths, workflow file), from the workflows themselves."""
    found = {}
    for wf in sorted(WORKFLOWS.glob("*.yml")):
        text = wf.read_text()
        if "docker/build-push-action" not in text:
            continue
        # paths: block under the push trigger
        paths = []
        in_paths = False
        for line in text.splitlines():
            if re.match(r"^\s*paths:\s*$", line):
                in_paths = True
                continue
            if in_paths:
                m = re.match(r"^\s*-\s+(\S+)\s*$", line)
                if m:
                    paths.append(m.group(1).replace("/**", ""))
                    continue
                in_paths = False
        names = {m.group(1) for m in IMAGE_RE.finditer(text)}
        for n in names:
            if paths:
                found.setdefault(n, (paths, str(wf)))
    return found


def pin_files():
    """image name -> kustomization that pins it by digest."""
    out = {}
    for k in list(Path("operators").rglob("kustomization.yaml")) + \
             list(Path("manifests").rglob("kustomization.yaml")):
        text = k.read_text()
        if "digest: sha256:" not in text:
            continue
        m = re.search(r"newName:\s*ghcr\.io/[\w./-]+?/([\w.-]+)\s*$", text, re.M)
        if m:
            out[m.group(1)] = str(k)
    return out


def main() -> int:
    images, pins = workflow_images(), pin_files()
    problems, checked = [], 0

    for name, pin in sorted(pins.items()):
        if name not in images:
            # A pinned image no image-building workflow claims. Reported rather
            # than skipped: it means nothing rebuilds it, which is worth knowing.
            problems.append(f"{name}: pinned in {pin} but no workflow builds it")
            continue
        paths, wf = images[name]
        src_sha, src_ts, src_when = last_commit(paths, exclude=pin)
        pin_sha, pin_ts, pin_when = last_commit([pin])
        if not src_sha or not pin_sha:
            continue
        checked += 1
        if src_ts > pin_ts:
            problems.append(
                f"{name}: source changed after the digest was pinned\n"
                f"      source {src_sha[:8]} {src_when}  ({', '.join(paths)})\n"
                f"      pin    {pin_sha[:8]} {pin_when}  ({pin})\n"
                f"      A release built now ships that code's manifests with the previous image.")

    if problems:
        print("ADR-063: a pinned image digest is older than the code it deploys:", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        print("\nRebuild the image and commit the new digest before releasing.", file=sys.stderr)
        return 1

    print(f"097-image-pins-are-current: {checked} pinned image(s), all newer than their source")
    return 0


if __name__ == "__main__":
    sys.exit(main())
