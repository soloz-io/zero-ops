#!/usr/bin/env python3
"""The Hyper-V PowerShell is built as a bash DOUBLE-QUOTED string.

Inside one, a literal `"` ends the string early and a backtick runs a command on
the machine running the script. Neither is a syntax error: `bash -n` passes, the
file stays valid, and the damage happens at run time.

That is not hypothetical. A comment added to the payload mentioning
  "Failed to finish reserving resources"
and `local-e2e.sh clean` ended the string at the first quote and executed
local-e2e.sh on the Mac mid-provision:

    provision-flatcar-worker.sh: line 1292: local-e2e.sh: command not found
    provision-flatcar-worker.sh: line 1292: local: `Reserve in the capacity ...

The VM was never created and the phase reported state 'absent'.

So the rule this enforces: inside a multi-line PowerShell payload, no unescaped
double quote and no backtick. PowerShell single quotes are unaffected and are
what the payload already uses for its own strings; rationale belongs in bash
comments ABOVE the payload, where quoting is not a hazard.
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
TARGETS = ["scripts/hybrid/provision-flatcar-worker.sh"]

OPEN = re.compile(r'^\s*local\s+(\w+)="\s*$')


def offending(line):
    q = b = 0
    for n, c in enumerate(line):
        prev = line[n - 1] if n else ""
        if c == '"' and prev != "\\":
            q += 1
        if c == "`" and prev != "\\":
            b += 1
    return q, b


def main():
    failures = []
    checked = 0
    for rel in TARGETS:
        path = ROOT / rel
        if not path.exists():
            failures.append(f"{rel}: not found")
            continue
        lines = path.read_text().split("\n")
        for i, line in enumerate(lines):
            m = OPEN.match(line)
            if not m:
                continue
            j = i + 1
            while j < len(lines) and lines[j].rstrip() != '"':
                j += 1
            checked += 1
            for k in range(i + 1, min(j, len(lines))):
                q, b = offending(lines[k])
                if q or b:
                    what = []
                    if q:
                        what.append(f"{q} unescaped double quote(s)")
                    if b:
                        what.append(f"{b} backtick(s) — these RUN COMMANDS")
                    failures.append(
                        f"{rel}:{k + 1} in ${m.group(1)}: " + " and ".join(what)
                        + f"\n      {lines[k].strip()[:88]}"
                    )

    if failures:
        print("  ❌ PowerShell payload quoting")
        for f in failures:
            print(f"      {f}")
        print("      Move prose into bash comments above the payload; inside it use")
        print("      PowerShell single quotes only.")
        return 1
    print(f"  ✅ {checked} PowerShell payload(s): no unescaped quotes or backticks")
    return 0


if __name__ == "__main__":
    sys.exit(main())
