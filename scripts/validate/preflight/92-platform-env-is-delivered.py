#!/usr/bin/env python3
"""Every platform env name a shipped validator REQUIRES must be delivered.

WHY THIS EXISTS

`zero-ops-auth`'s surface validators hard-fail when a name is absent -- that is
deliberate, and `PlatformConfigError` names every missing value at once. But the
value is delivered by the APPLICATION's own Helm chart, one `valueFrom` per name,
copied from the onboarding guide into each repository:

    - name: OIDC_ORG_ID
      valueFrom: {secretKeyRef: {name: <app>-platform-identity, key: OIDC_ORG_ID}}

So the set a validator requires lives in this repository and the set an
application delivers lives in that one, and nothing compares them. When 0.15.0
added `OIDC_ORG_ID` to `browserSessionValidator` -- so the tenant could be
COMPARED rather than merely required (ADR-094 invariant 3) -- every application
already deployed became one upgrade away from a pod that will not start, and the
platform had no way to know which.

It was not theoretical. On 2026-09-30 this check, on its first run, found
waypoint's BFF chart wiring neither OIDC_CLIENT_ID nor OIDC_ORG_ID. Nothing was
failing, because waypoint was still on 0.14.0; the failure was waiting for the
upgrade the platform was about to ask it to make.

WHY A PREFLIGHT AND NOT A RUNTIME ERROR

The runtime error is good and is not the problem. It arrives at pod start, on the
box, after a release -- which is the most expensive moment to learn that a chart
in another repository needed a line. Here it costs a diff.

WHAT IT DOES NOT CHECK

That the delivered VALUE is right; only that the name is wired to something. A
wrong value fails loudly at the issuer, which is a failure that names itself.

Emits tab-separated lines the shell wrapper classifies:
    OK<TAB><message>
    BAD<TAB><message>
    NONE
"""
import os
import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve()
ZERO_OPS = HERE.parents[3]
VALIDATORS = ZERO_OPS / "packages/auth/src/platform/validators.ts"
ENV_MAP = ZERO_OPS / "packages/auth/src/platform/env.ts"

# Applications are separate repositories checked out beside this one. An absent
# one is not a failure: this repository is the platform and must build alone.
APP_CHART_GLOBS = [
    ("oranger", "packages/bff/charts/*/values.yaml"),
    ("waypoint", "packages/bff/charts/*/values.yaml"),
]
APP_ROOT = Path(os.environ.get("APP_REPOS_DIR", ZERO_OPS.parent))

WORKLOAD_IDENTITY_TPL = (
    ZERO_OPS
    / "manifests/tenants/charts/universal-tenant/templates/workload-platform-identity.yaml"
)


def platform_identity_keys() -> set:
    """The keys `<app>-platform-identity` carries, read from the template.

    Listed nowhere by hand: the template that renders the object is the only
    place they are decided, so a key added there is delivered here without this
    file changing.
    """
    if not WORKLOAD_IDENTITY_TPL.exists():
        return set()
    text = WORKLOAD_IDENTITY_TPL.read_text()
    # Only the first ExternalSecret in the file is `<app>-platform-identity`;
    # the others carry credentials and the caller map, which are mounted (or
    # not) on their own terms.
    head = text.split("---")[0] if "---" in text else text
    for block in text.split("---"):
        if "platform-identity" in block and "secretKey:" in block:
            head = block
            break
    return set(re.findall(r"^\s*-\s*secretKey:\s*([A-Z][A-Z0-9_]*)", head, re.M))


PLATFORM_IDENTITY_KEYS = platform_identity_keys()


def env_names() -> dict:
    """PLATFORM_ENV's mapping: the library's key -> the env var it reads."""
    src = ENV_MAP.read_text()
    body = src[src.index("export const PLATFORM_ENV"):]
    return dict(re.findall(r'(\w+):\s*"(OIDC_[A-Z_]+|[A-Z_]+)"', body))


def required_by_surface() -> dict:
    """Each exported validator -> the env vars it refuses to build without.

    Read from `requirePlatformEnv([...])`, which is the single place a surface
    states its hard requirement, so this cannot drift from the code it describes.
    """
    src = VALIDATORS.read_text()
    names = env_names()
    out = {}
    for m in re.finditer(
        r"export function (\w+)\([^)]*\)[^{]*\{(.*?)\n\}", src, re.S
    ):
        fn, body = m.group(1), m.group(2)
        req = re.search(r"requirePlatformEnv\(\s*\[(.*?)\]", body, re.S)
        if not req:
            continue
        keys = re.findall(r'"(\w+)"', req.group(1))
        missing_map = [k for k in keys if k not in names]
        if missing_map:
            print(f"BAD\t{fn} requires {missing_map}, which PLATFORM_ENV does not name")
            continue
        out[fn] = [names[k] for k in keys]
    return out


def chart_deliveries() -> set:
    """Every platform env name a WORKLOAD can reference from the chart.

    Two shapes count, and only two:

      secretKey: X          an ExternalSecret key, mounted into a workload Secret
      X: <value>            a key of a ConfigMap the workload reads by name

    A gateway container's own `env:` entry does NOT count, and that distinction is
    the whole point. OIDC_BACKEND_PROJECT_ID_<TARGET> was mounted there and
    nowhere else: correct while the GATEWAY performed the exchange, wrong the
    moment ADR-097 moved cross-application calling into the workload. Counting a
    gateway env entry as delivery would have passed that gap.
    """
    tpl_dir = ZERO_OPS / "manifests/tenants/charts/universal-tenant/templates"
    if not tpl_dir.is_dir():
        return set()
    names = set()
    for t in sorted(tpl_dir.glob("*.yaml")):
        text = t.read_text()
        names |= set(re.findall(r"^\s*-\s*secretKey:\s*([A-Z][A-Z0-9_]*)", text, re.M))
        # ConfigMap data keys: two-space indented `NAME: value` under `data:`.
        names |= set(re.findall(r"^  ([A-Z][A-Z0-9_]*):\s+\S", text, re.M))
        # Rendered suffixes (`..._{{ . | upper ... }}`) are matched by prefix.
        names |= set(re.findall(r"^\s*-\s*secretKey:\s*([A-Z][A-Z0-9_]*_)\{\{", text, re.M))
    return names


def chart_gaps(required: set) -> dict:
    """Names a shipped surface requires that no workload can reference.

    Scoped to what the factories actually require, plus the per-dependency key
    `serviceTokenSource` derives. Deliberately NOT every OIDC_ name in the
    library: PLATFORM_ENV also names the gateway's own credentials and one entry
    nothing reads, and reporting those is noise. A gate that cries wolf is one
    people learn to skip, which is how the comment-satisfiable version of this
    same check nearly shipped.
    """
    delivered = chart_deliveries()
    gaps = {}
    for name in sorted(required):
        if name in delivered:
            continue
        # A derived name is delivered when its prefix is rendered with a range.
        if any(d.endswith("_") and name.startswith(d) for d in delivered):
            continue
        gaps[name] = "no secretKey or ConfigMap key in universal-tenant"
    return gaps


def wired_names(text: str, app: str) -> set:
    """The env names this chart actually delivers to the container.

    COMMENTS ARE STRIPPED FIRST, and that is not tidiness. The first version of
    this check matched the whole file, so the moment waypoint's chart was fixed
    the comment EXPLAINING the fix satisfied it -- the check would have passed on
    prose describing the names rather than on the names being wired. A gate that
    a comment can satisfy is worse than no gate, because it reports green.

    Two shapes count:

      - name: OIDC_ORG_ID              an explicit entry, whatever it reads from
      envFrom: [secretRef: <app>-platform-identity]
                                       the platform's identity object mounted
                                       whole, whose KEYS are the env names
                                       (PLATFORM_ENV) -- so mounting it delivers
                                       every name it carries, including ones
                                       added after this chart was last edited

    The second shape is the one worth having. A chart that restates keys one by
    one has to be edited every time the platform adds a required value, which is
    exactly how the gap this gate exists for was created.
    """
    stripped = "\n".join(
        re.sub(r"(?<!\S)#.*$", "", line) for line in text.splitlines()
    )

    names = set(re.findall(r"^\s*-\s*name:\s*([A-Z][A-Z0-9_]*)", stripped, re.M))

    # `envFrom: - secretRef: {name: <app>-platform-identity}` delivers the keys
    # the platform renders into that object. Those keys are stated once, by the
    # chart that renders them, and read here rather than repeated.
    if re.search(rf"secretRef:\s*\{{?\s*name:\s*{app}-platform-identity", stripped):
        names |= PLATFORM_IDENTITY_KEYS
    return names


def app_charts():
    for app, pattern in APP_CHART_GLOBS:
        repo = APP_ROOT / app
        if not repo.is_dir():
            continue
        for values in sorted(repo.glob(pattern)):
            yield app, values


def main() -> int:
    if not VALIDATORS.exists():
        print("NONE")
        return 0

    surfaces = required_by_surface()
    if not surfaces:
        print("BAD\tno surface validator declared a requirePlatformEnv set")
        return 0

    for fn, vars_ in surfaces.items():
        print(f"OK\t{fn} requires {' '.join(vars_)}")

    # Only the browser-session surface is checked against app charts: it is the
    # one every application mounts. consumerApiValidator is mounted on
    # declaration, and an app that has not declared allowedConsumers correctly
    # does not build it at all.
    browser = surfaces.get("browserSessionValidator")
    if not browser:
        print("BAD\tbrowserSessionValidator no longer declares its required env")
        return 0

    # THE PLATFORM'S OWN HALF, checked before the applications'.
    #
    # An app chart can only wire a value the platform actually mounts. This gap
    # recurred inside the chart on 2026-09-30: OIDC_BACKEND_PROJECT_ID_<TARGET>
    # had been published since ADR-095 but mounted only on the GATEWAY, because
    # the gateway was going to perform the exchange. ADR-097 moved cross-
    # application calling into the workload, and the key did not follow -- so
    # `serviceTokenSource` read a value the chart never delivered, and the oranger
    # team found it by writing the calling code.
    # What serviceTokenSource needs beyond the factories' own lists: the target's
    # project id, whose name is DERIVED from the dependency (ADR-097).
    service_required = {"OIDC_SERVICE_CLIENT_ID", "OIDC_SERVICE_CLIENT_SECRET",
                        "OIDC_BACKEND_PROJECT_ID_WAYPOINT"}
    required = {v for vs in surfaces.values() for v in vs} | service_required
    for name, why in chart_gaps(required).items():
        print(f"BAD\t{name} is required by a shipped surface but {why}")

    checked = 0
    for app, values in app_charts():
        rel = values.relative_to(APP_ROOT)
        wired = wired_names(values.read_text(), app)
        missing = [v for v in browser if v not in wired]
        checked += 1
        if missing:
            print(
                f"BAD\t{rel} does not wire {' '.join(missing)}, which "
                f"browserSessionValidator requires -- this BFF will not start "
                f"once {app} upgrades zero-ops-auth"
            )
        else:
            print(f"OK\t{rel} wires every value browserSessionValidator requires")

    if checked == 0:
        print("NONE")
    return 0


if __name__ == "__main__":
    sys.exit(main())
