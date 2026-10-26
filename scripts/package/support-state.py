#!/usr/bin/env python3
"""Say which published versions are still supported.

ADR-069 makes the support window a property of the version: "what any tenant is
entitled to is derivable from what it runs, and no agreement is negotiated per
tenant." Derivable, but nothing derived it. The window was declared at
publication and then never evaluated again, so "is what I am running still
supported" had no answer -- for a tenant asking, or for the platform being asked.

This is the evaluation. It reads the published releases and the policy, and
prints the state of each version today.

It is also the retroactive audit. Versions published before the window existed
carry none, and they are reported as such rather than assumed to be supported --
an unknown window is not a long one.

    support-state.py                  every published version
    support-state.py 0.1.14           one version, exit 1 if not supported

Reads releases with `gh`, or from --releases for a run with no network.
"""
import argparse
import datetime
import json
import subprocess
import sys

try:
    import yaml
except ImportError:
    sys.exit("PyYAML required")

POLICY = "manifests/architecture/support-policy.yaml"

SUPPORTED, DEPRECATED, UNSUPPORTED, UNKNOWN = (
    "supported", "deprecated", "unsupported", "window not declared")


def semver(v):
    """(major, minor, patch), prerelease dropped. Numeric so 0.1.9 sorts before
    0.1.10, which a string comparison gets backwards."""
    v = v.lstrip("v").split("-")[0].split("+")[0]
    out = []
    for part in (v.split(".") + ["0", "0", "0"])[:3]:
        out.append(int(part) if part.isdigit() else 0)
    return tuple(out)


def releases(path):
    if path:
        return json.load(open(path))
    try:
        raw = subprocess.run(
            ["gh", "release", "list", "--limit", "100", "--json",
             "tagName,publishedAt,isPrerelease"],
            capture_output=True, text=True, check=True).stdout
    except (subprocess.CalledProcessError, FileNotFoundError) as e:
        sys.exit(f"support-state: could not list releases: {e}")
    return json.loads(raw)


def state(published, newest_minor, minor, policy, today):
    """The version's state today, and what is holding it there.

    Both halves of the window are evaluated and the LONGER governs, which is
    what the policy says and what stops either half shortening the promise on
    its own: a burst of releases cannot expire a version adopted last week, and
    a quiet year cannot strand one six releases behind.

    Returns (state, until, holder). `until` is a date only when TIME is what
    governs. When the version half is holding a version supported there is no
    date -- it ends when enough minors are published, which has not happened
    yet. Reporting the elapsed time date there would print "supported until"
    followed by a date in the past, which reads as a bug in the tool and hides
    the actual reason.
    """
    if published is None:
        return UNKNOWN, None, None

    sup = policy["window"]["supported"]
    dep = policy["window"]["deprecated"]

    by_time = published + datetime.timedelta(days=sup["days"])
    in_time = today <= by_time
    in_versions = (newest_minor - minor) <= sup["minors"]

    if in_time and in_versions:
        return SUPPORTED, by_time, "both"
    if in_versions:
        return SUPPORTED, None, "versions"
    if in_time:
        return SUPPORTED, by_time, "time"

    deprecated_until = by_time + datetime.timedelta(days=dep["days"])
    if today <= deprecated_until:
        return DEPRECATED, deprecated_until, "time"
    return UNSUPPORTED, deprecated_until, "time"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("version", nargs="?")
    ap.add_argument("--policy", default=POLICY)
    ap.add_argument("--releases", help="a JSON file of releases, instead of gh")
    ap.add_argument("--today", help="evaluate as of this date, YYYY-MM-DD")
    args = ap.parse_args()

    policy = yaml.safe_load(open(args.policy)) or {}
    today = (datetime.date.fromisoformat(args.today) if args.today
             else datetime.datetime.now(datetime.timezone.utc).date())

    rows = []
    for r in releases(args.releases):
        # A prerelease is a distinct version consumed by testing (ADR-063) and
        # is never something a tenant is running under a support promise.
        if r.get("isPrerelease"):
            continue
        tag = r["tagName"]
        pub = r.get("publishedAt")
        date = datetime.date.fromisoformat(pub[:10]) if pub else None
        rows.append((tag, semver(tag), date))

    if not rows:
        sys.exit("support-state: no published releases")

    newest_minor = max(v[1] for _, v, _ in rows)
    rows.sort(key=lambda r: r[1], reverse=True)

    if args.version:
        want = semver(args.version)
        for tag, v, date in rows:
            if v == want:
                st, until, holder = state(date, newest_minor, v[1], policy, today)
                detail = f" until {until}" if until else (
                    f" while within {policy['window']['supported']['minors']} "
                    f"minor versions of the newest" if holder == "versions" else "")
                print(f"{tag}: {st}{detail}")
                return 0 if st in (SUPPORTED, DEPRECATED) else 1
        print(f"{args.version} is not a published version", file=sys.stderr)
        return 1

    width = max(len(t) for t, _, _ in rows)
    unknown = held_by_versions = 0
    minors = policy["window"]["supported"]["minors"]
    for tag, v, date in rows:
        st, until, holder = state(date, newest_minor, v[1], policy, today)
        if st == UNKNOWN:
            unknown += 1
            why = "published date unknown"
        elif until:
            why = f"until {until}"
        else:
            held_by_versions += 1
            why = f"while within {minors} minors of {newest_minor}"
        print(f"  {tag:<{width}}  {st:<20} {why}")

    if held_by_versions and newest_minor <= minors:
        # Worth saying plainly rather than leaving in the arithmetic. While the
        # platform has published only 0.1.x, "within 2 minor versions of the
        # newest" is true of EVERY version ever published, so the version half
        # expires nothing and the effective window is unbounded. The policy is
        # not wrong; it has simply not started applying yet, and a support
        # promise that is unbounded by accident is worth knowing about.
        print()
        print(f"{held_by_versions} version(s) are supported by the version half "
              f"alone.")
        print(f"Every published version is within {minors} minors of "
              f"{newest_minor}, so that half expires nothing yet and the window "
              f"is effectively unbounded.")
        print("It begins to bite at minor " + str(minors + 1) + ".")

    if unknown:
        print()
        print(f"{unknown} version(s) carry no declared window. ADR-069 makes the")
        print("window a property of the version, so these cannot be evaluated and")
        print("are not thereby supported -- an unknown window is not a long one.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
