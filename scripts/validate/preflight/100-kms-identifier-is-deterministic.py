#!/usr/bin/env python3
"""ADR-100's acceptance criteria, enforced by structure rather than by test.

WHY THIS EXISTS

Both of ADR-100's acceptance criteria fail SILENTLY. A non-deterministic key
identifier does not lose data -- material written under the previous identifier still
unwraps -- it makes the identifier meaningless, and with it any ability to tell
whether a rotation has happened. A status path and a wrap path that observe the key
store separately disagree only during a rotation on a multi-node control plane, which
is the one moment nobody is watching a unit test.

Tests pin both. Tests can also be deleted, skipped, or quietly weakened, and neither
failure shows up as a failing test the day it is introduced -- it shows up as a
rotation that achieved nothing, months later. So the same two properties are also
asserted against the SHAPE of the code, where satisfying them is not optional:

  criterion 1   the identifier is derived, deterministic and durable. Enforced by
                refusing the package any import that could supply a clock, a random
                value, process state or a counter. A field cannot be read from if the
                package cannot import the thing that produces it.

  criterion 2   the status path and the wrap path never disagree. Enforced by refusing
                the service any direct call to the key store's Active() outside the
                refresh loop. Both paths must read the snapshot.

Emits tab-separated lines the shell wrapper classifies:
    OK<TAB><message>
    BAD<TAB><message>
    NONE
"""
import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve()
ZERO_OPS = HERE.parents[3]
PLUGIN = ZERO_OPS / "operators" / "kms-plugin"

KEYID = PLUGIN / "internal" / "keyid"
SERVICE = PLUGIN / "internal" / "service"

# Anything that can make the same key version render differently on another node or
# after a restart. `os` is included for Hostname and Getpid; `sync/atomic` for a
# locally held counter, which ADR-100 names explicitly.
FORBIDDEN_IN_KEYID = {
    "time": "a clock -- the same key version would render differently at a different moment",
    "math/rand": "a random value -- the identifier would differ per process",
    "crypto/rand": "a random value -- the identifier would differ per process",
    "os": "process state (Hostname, Getpid) -- the identifier would differ per node",
    "sync/atomic": "a locally held counter, which ADR-100 forbids by name",
    "net": "host state -- the identifier would differ per node",
}


def imports_of(path):
    """Import paths of one Go file, from its import block."""
    text = path.read_text(encoding="utf-8")
    out = set()
    m = re.search(r"^import\s*\(\s*(.*?)^\)", text, re.M | re.S)
    blocks = [m.group(1)] if m else []
    for single in re.findall(r'^import\s+(?:[\w.]+\s+)?"([^"]+)"', text, re.M):
        out.add(single)
    for b in blocks:
        for line in b.splitlines():
            line = line.strip()
            if not line or line.startswith("//"):
                continue
            q = re.search(r'"([^"]+)"', line)
            if q:
                out.add(q.group(1))
    return out


def main():
    if not PLUGIN.is_dir():
        print("NONE")
        return 0

    findings = []
    checked = 0

    # ── criterion 1 ──────────────────────────────────────────────────────────
    if not KEYID.is_dir():
        findings.append(
            f"BAD\t{KEYID.relative_to(ZERO_OPS)} is missing; the key identifier has no home, so "
            f"ADR-100's first acceptance criterion cannot be enforced anywhere"
        )
    else:
        for f in sorted(KEYID.glob("*.go")):
            if f.name.endswith("_test.go"):
                continue
            checked += 1
            for imp in sorted(imports_of(f)):
                if imp in FORBIDDEN_IN_KEYID:
                    findings.append(
                        f"BAD\t{f.relative_to(ZERO_OPS)} imports {imp!r}, which supplies "
                        f"{FORBIDDEN_IN_KEYID[imp]}. ADR-100 requires the identifier to be derived "
                        f"from the cluster, the key and the version and from nothing else: a "
                        f"changed identifier is read as a changed key, so the API server would "
                        f"establish new encryption state on every restart while nothing rotated"
                    )

    # ── criterion 2 ──────────────────────────────────────────────────────────
    if not SERVICE.is_dir():
        findings.append(
            f"BAD\t{SERVICE.relative_to(ZERO_OPS)} is missing; nothing implements the provider "
            f"interface, so the status/wrap agreement cannot be enforced"
        )
    else:
        for f in sorted(SERVICE.glob("*.go")):
            if f.name.endswith("_test.go"):
                continue
            checked += 1
            text = f.read_text(encoding="utf-8")
            # Which functions call the store's Active()? Only the refresh path may.
            for fn, body in re.findall(
                r"^func \([^)]*\) (\w+)\([^)]*\)[^{]*\{(.*?)^\}", text, re.M | re.S
            ):
                if ".Active(" not in body:
                    continue
                if fn != "Refresh":
                    findings.append(
                        f"BAD\t{f.relative_to(ZERO_OPS)}: {fn}() calls the key store's Active() "
                        f"directly. ADR-100 requires the status path and the wrap path to read ONE "
                        f"snapshot: two independent observations disagree during a rotation on a "
                        f"multi-node control plane, and the interface treats that disagreement as "
                        f"an unhealthy plugin"
                    )
            # Status and Encrypt must go through the snapshot.
            for fn in ("Status", "Encrypt"):
                m = re.search(r"^func \([^)]*\) " + fn + r"\([^)]*\)[^{]*\{(.*?)^\}", text, re.M | re.S)
                if not m:
                    continue
                if "Current()" not in m.group(1):
                    findings.append(
                        f"BAD\t{f.relative_to(ZERO_OPS)}: {fn}() does not read the active snapshot "
                        f"via Current(); it cannot be reporting the same key version the other path "
                        f"uses"
                    )

    if checked == 0 and not findings:
        print("NONE")
        return 0

    for line in findings:
        print(line)
    if not findings:
        print(
            f"OK\t{checked} file(s): the key identifier can import no clock, randomness, process "
            f"state or counter, and only the refresh loop reads the key store"
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
