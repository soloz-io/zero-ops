#!/usr/bin/env python3
"""State the support window a published version carries.

ADR-069 makes the window a property of the version, declared when it is
published. Before this it was declared nowhere: a tenant asking "is what I am
running still supported" had no answer, and neither did the platform, which
makes a maintenance promise something nobody can check.

The dates printed here are the TIME half of the window, which is the half that
can be computed at publication. The version half -- within N minors of the
newest -- depends on releases that do not exist yet, so it is stated as a rule
rather than a date. Whichever is longer governs, so what is printed is a floor
and never an expiry the version can fall short of.

Usage: support-window.py <version> [--published <YYYY-MM-DD>] [--policy <path>]
Writes markdown rows on stdout.
"""
import argparse
import datetime
import sys

try:
    import yaml
except ImportError:
    sys.exit("PyYAML required")

DEFAULT_POLICY = "manifests/architecture/support-policy.yaml"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("version")
    ap.add_argument("--published", default=None,
                    help="publication date, YYYY-MM-DD (default: today, UTC)")
    ap.add_argument("--policy", default=DEFAULT_POLICY)
    args = ap.parse_args()

    with open(args.policy) as fh:
        policy = yaml.safe_load(fh) or {}
    window = policy.get("window") or {}
    supported = window.get("supported") or {}
    deprecated = window.get("deprecated") or {}

    for name, block in (("supported", supported), ("deprecated", deprecated)):
        if not isinstance(block.get("days"), int):
            sys.exit(f"support-window: {args.policy} declares no integer "
                     f"`days` for {name}; the window would have no floor")

    if args.published:
        published = datetime.date.fromisoformat(args.published)
    else:
        published = datetime.datetime.now(datetime.timezone.utc).date()

    supported_until = published + datetime.timedelta(days=supported["days"])
    deprecated_until = supported_until + datetime.timedelta(days=deprecated["days"])
    minors = supported.get("minors")

    print()
    print("## Support window")
    print()
    print("| | |")
    print("|---|---|")
    print(f"| Published | {published.isoformat()} |")
    print(f"| Supported until | {supported_until.isoformat()} |")
    print(f"| Then deprecated until | {deprecated_until.isoformat()} |")
    if isinstance(minors, int):
        print(f"| Or while within | {minors} minor versions of the newest "
              f"published version |")
    print()
    # Said plainly, because the thing most likely to be misread is that an
    # expiring window takes something away. It does not: ADR-065 leaves the
    # tenant holding a working box, and ADR-069 withdraws an obligation rather
    # than a capability.
    print("Whichever of the two is longer governs, so these dates are a floor.")
    print("After the second date this version is **unsupported**: the platform")
    print("answers on a best-effort basis and makes no undertaking about outcome.")
    print("Nothing stops running at any point — a cluster reconciles what it")
    print("reconciled yesterday. What lapses is an obligation, not a capability.")
    print()
    print("Declining a proposal is not a breach. Support follows the version, so")
    print("a tenant that stays put moves through the states above on the same")
    print("schedule as anyone else (ADR-069).")


if __name__ == "__main__":
    main()
