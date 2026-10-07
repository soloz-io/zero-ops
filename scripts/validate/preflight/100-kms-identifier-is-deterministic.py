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

  criterion 1   the identifier is derived, deterministic and durable. The identifier
                is now the Cloud KMS CryptoKeyVersion RESOURCE NAME plus an
                operator-supplied suffix. An earlier design composed one from the
                cluster name, key name and version number in its own package
                (internal/keyid); that package was deleted because the resource name
                already carries project, location, key ring, key and version, and
                composing a second identifier only added a second thing that could be
                wrong.

                So the check moved with it: the package that derives the identifier may
                not import a clock, randomness, process state or a counter, and the
                deriving function itself may reference nothing but its argument and the
                configured suffix.

  QoS           the static pod's requests equal its limits, which is what makes it
                Guaranteed. kubelet reclaims BestEffort first, Burstable next and
                Guaranteed last, and this process is on the API server's encryption
                path: while it is gone, nothing uncached decrypts.

                Checked because the failure was a COMMENT DRIFTING FROM ITS MANIFEST.
                The file set 25m/200m and 64Mi/128Mi while the comment beside it said
                they were equal, so the pod was Burstable and the prose said
                otherwise. A reviewer reading the comment would conclude Guaranteed.
                Prose cannot be asserted; two numbers can.

  criterion 2   the status path and the wrap path never disagree. Three structural
                properties now hold this, and each one of them is a defect the upstream
                plugin actually has:

                (a) EXACT-VERSION ENCRYPTION. Encrypt must address the wrap to the
                    version it observed, not to the parent CryptoKey. Changing a key's
                    primary version is eventually consistent, so encrypting against the
                    parent lets the status path and the encrypt path resolve DIFFERENT
                    versions -- and the API server requires the key_id from Status to
                    equal the key_id from Encrypt (k8s.io/apiserver
                    encryptionconfig/config.go:424), marking the provider unhealthy
                    otherwise. This regresses silently: pass the parent and everything
                    works until the next rotation.

                (b) ORDERING. Status must install its observation BEFORE it reads the
                    identifier it is about to report. Upstream reads its cache, then
                    probes, then updates the cache -- so its first Status after a
                    rotation returns the old key_id while Encrypt has already moved, and
                    (a)'s equality check fails for a poll interval on every rotation.
                    Pure sequence, so checked as sequence.

                (c) PROVENANCE. The reported identifier must come from the installed
                    observation in both paths, never from a fresh store response.

                An earlier version of this check forbade Encrypt() from calling
                Active() at all, and required the KeyId to come from a variable
                literally named `snap` -- it fired on a correct Encrypt that reported
                from `reported`. Matching the name tested the spelling. What matters is
                where the value came from.

  criterion 3   unwrapping must stay addressed to the PARENT key. Cloud KMS resolves
                the version from the ciphertext and retains non-destroyed versions,
                which is what lets data written before any number of rotations keep
                decrypting. If Unwrap ever gained a version parameter, every object
                written before the last rotation would become unreadable -- the exact
                loss ADR-100 exists to prevent, and it would show up only on a cold
                read after a rotation.

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
PLUGIN = ZERO_OPS / "operators" / "kms-plugin"

SERVICE = PLUGIN / "internal" / "service"
KEYSTORE = PLUGIN / "internal" / "keystore"
# The reconciler's view of the same key. ADR-100 "Declarative delivery".
KMS_MANIFESTS = ZERO_OPS / "manifests" / "hub-core-services" / "crossplane" / "kms"
STATIC_POD = PLUGIN / "deploy" / "static-pod.yaml"

# Anything that can make the same key version render differently on another node or
# after a restart. `os` is included for Hostname and Getpid; `sync/atomic` for a
# locally held counter, which ADR-100 names explicitly.
FORBIDDEN_IN_IDENTIFIER_PACKAGE = {
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

    # PyYAML IS RESOLVED ONCE, HERE, BEFORE ANY CHECK THAT NEEDS IT.
    #
    # It used to be imported inside the static-pod section, which is near the bottom of
    # this function. A later check guarded on `yaml is not None`, ran BEFORE that import,
    # and therefore skipped silently -- passing while asserting nothing. That is the exact
    # failure this file exists to catch in other people's code, so it is worth the comment:
    # a guard whose condition can never be true is indistinguishable from a check that
    # passes.
    try:
        import yaml
    except ImportError:
        yaml = None

    findings = []
    checked = 0

    # ── criteria 1, 2 and 3 live in internal/service and internal/keystore ──
    if not SERVICE.is_dir():
        findings.append(
            f"BAD\t{SERVICE.relative_to(ZERO_OPS)} is missing; nothing implements the provider "
            f"interface, so none of ADR-100's acceptance criteria can be enforced"
        )
    else:
        for f in sorted(SERVICE.glob("*.go")):
            if f.name.endswith("_test.go"):
                continue
            checked += 1
            text = f.read_text(encoding="utf-8")

            # criterion 1, at package granularity: the package that derives the
            # identifier cannot read a clock, randomness, process state or a counter if
            # it cannot import the thing that produces them.
            for imp in sorted(imports_of(f)):
                if imp in FORBIDDEN_IN_IDENTIFIER_PACKAGE:
                    findings.append(
                        f"BAD\t{f.relative_to(ZERO_OPS)} imports {imp!r}, which supplies "
                        f"{FORBIDDEN_IN_IDENTIFIER_PACKAGE[imp]}. ADR-100 requires the key "
                        f"identifier to be derived from the Cloud KMS CryptoKeyVersion name and "
                        f"the configured suffix and from nothing else: a changed identifier is "
                        f"read as a changed key, so the API server would establish new encryption "
                        f"state on every restart while nothing rotated"
                    )

            bodies = {
                fn: body
                for fn, body in re.findall(
                    r"^func \([^)]*\) (\w+)\([^)]*\)[^{]*\{(.*?)^\}", text, re.M | re.S
                )
            }
            if "Status" not in bodies:
                continue

            # criterion 1, at function granularity. The deriving function may reference
            # its argument and the configured suffix. Nothing else can vary.
            derive = bodies.get("deriveKeyID")
            if derive is None:
                findings.append(
                    f"BAD\t{f.relative_to(ZERO_OPS)}: no deriveKeyID(); the one place the "
                    f"identifier is produced cannot be located, so criterion 1 cannot be checked "
                    f"at all"
                )
            else:
                for bad in ("time.", "rand.", "os.", "atomic.", "Getpid", "Hostname"):
                    if bad in derive:
                        findings.append(
                            f"BAD\t{f.relative_to(ZERO_OPS)}: deriveKeyID() references {bad!r}. "
                            f"The identifier must be a pure function of the CryptoKeyVersion name "
                            f"and the configured suffix"
                        )

            # criterion 2(a): Encrypt names the version it observed.
            enc = bodies.get("Encrypt", "")
            wrap_calls = re.findall(r"\.Wrap\(([^)]*)\)", enc)
            if not wrap_calls:
                findings.append(
                    f"BAD\t{f.relative_to(ZERO_OPS)}: Encrypt() does not call Wrap()"
                )
            for args in wrap_calls:
                parts = [a.strip() for a in args.split(",")]
                if len(parts) < 3:
                    findings.append(
                        f"BAD\t{f.relative_to(ZERO_OPS)}: Encrypt() calls Wrap({args}) with no "
                        f"version argument. Encryption must name the CryptoKeyVersion it observed "
                        f"-- letting Cloud KMS select the primary again lets the status path and "
                        f"the encrypt path resolve different versions, which the API server "
                        f"treats as an unhealthy provider (encryptionconfig/config.go:424)"
                    )
                elif not parts[2].endswith(".Name"):
                    findings.append(
                        f"BAD\t{f.relative_to(ZERO_OPS)}: Encrypt() wraps against {parts[2]!r}, "
                        f"which is not an observed CryptoKeyVersion name. Passing the PARENT key "
                        f"here reinstates the propagation race and regresses silently: it works "
                        f"until the next rotation"
                    )

            # criterion 2(b): Status probes BEFORE it reads what it will report.
            status = bodies["Status"]
            probe = re.search(r"s\.Refresh\(", status)
            read = re.search(r"s\.keyID", status)
            if probe is None:
                findings.append(
                    f"BAD\t{f.relative_to(ZERO_OPS)}: Status() never refreshes the observation, "
                    f"so healthz reports on a key it has not checked and a rotation is only "
                    f"noticed by some other path"
                )
            elif read is None:
                findings.append(
                    f"BAD\t{f.relative_to(ZERO_OPS)}: Status() does not read s.keyID, so the "
                    f"identifier it reports cannot be the installed one"
                )
            elif probe.start() > read.start():
                findings.append(
                    f"BAD\t{f.relative_to(ZERO_OPS)}: Status() reads s.keyID at offset "
                    f"{read.start()} but refreshes at {probe.start()} -- it reports the "
                    f"PRE-PROBE identifier. That is the upstream plugin's defect: the first "
                    f"Status after a rotation returns the old key_id while Encrypt has already "
                    f"moved, and the API server marks the provider unhealthy because the two "
                    f"disagree (encryptionconfig/config.go:424)"
                )

            # criterion 2(c): both paths report the INSTALLED identifier.
            #
            # Provenance, not spelling. The roots are a direct read of s.keyID and the
            # accessor that returns it; anything copied from one of those is equally
            # fine.
            for fn in ("Status", "Encrypt"):
                body = bodies.get(fn, "")
                if not body:
                    continue
                origins = set(re.findall(r"(\w+)\s*:?=\s*s\.observed,\s*s\.keyID", body))
                origins |= set(re.findall(r",\s*(\w+)\s*:?=\s*s\.observed,\s*s\.keyID", body))
                origins |= set(re.findall(r"[\w,\s]*?(\w+)(?:,\s*\w+)?\s*:?=\s*s\.current\(\)", body))
                origins |= set(re.findall(r"(\w+)\s*:?=\s*s\.keyID", body))
                # Multi-assignment forms: capture every name on the left of an
                # assignment whose right-hand side is one of the two origins.
                for lhs, rhs in re.findall(r"^\s*([\w,\s]+?)\s*:?=\s*(s\.current\(\)|s\.keyID|s\.observed,\s*s\.keyID)", body, re.M):
                    for name in (n.strip() for n in lhs.split(",")):
                        if name and name != "_":
                            origins.add(name)

                for assigned in re.findall(r"KeyId:\s*([A-Za-z_][\w.]*)", body):
                    root = assigned.split(".")[0]
                    if root not in origins:
                        findings.append(
                            f"BAD\t{f.relative_to(ZERO_OPS)}: {fn}() reports KeyId from "
                            f"{assigned!r}, which does not originate from the installed "
                            f"observation (s.keyID or s.current()). Reporting a value read "
                            f"straight from the key authority makes the status and the wrap path "
                            f"two independent observations, which is what ADR-100's second "
                            f"criterion forbids. Origins seen here: {sorted(origins) or 'none'}"
                        )

    # ── criterion 4: the plugin calls nothing its IAM role does not grant ───
    #
    # The identity holds roles/cloudkms.cryptoKeyEncrypterDecrypter bound at the
    # CryptoKey, which carries cryptoKeyVersions.useToEncrypt and useToDecrypt and
    # NOTHING ELSE. Every call below needs a permission that role does not have, so
    # adding one means either a custom role nobody has written or a plugin that fails on
    # a real project with an IAM error. This was a real defect: the client read
    # CryptoKey.Primary with GetCryptoKey, needing cloudkms.cryptoKeys.get, while
    # ADR-100 listed that permission under this role.
    FORBIDDEN_CALLS = {
        "GetCryptoKey": "cloudkms.cryptoKeys.get. The active version is discovered from "
        "an Encrypt against the parent key, whose response names the version that "
        "performed it",
        "ListCryptoKeyVersions": "cloudkms.cryptoKeyVersions.list",
        "UpdateCryptoKeyPrimaryVersion": "cloudkms.cryptoKeys.update -- and rotation is "
        "a separate identity's action by design",
        "CreateCryptoKey": "cloudkms.cryptoKeys.create",
        "CreateCryptoKeyVersion": "cloudkms.cryptoKeyVersions.create",
        "DestroyCryptoKeyVersion": "cloudkms.cryptoKeyVersions.destroy",
        "UpdateCryptoKeyVersion": "cloudkms.cryptoKeyVersions.update",
        "SetIamPolicy": "IAM administration",
        "GetIamPolicy": "IAM read",
    }
    if KEYSTORE.is_dir():
        for f in sorted(KEYSTORE.glob("*.go")):
            if f.name.endswith("_test.go"):
                continue
            checked += 1
            text = f.read_text(encoding="utf-8")
            for call, perm in FORBIDDEN_CALLS.items():
                # `g.client.<Call>(` -- the call itself, not a mention in a comment.
                if re.search(r"\bclient\.%s\(" % re.escape(call), text):
                    findings.append(
                        f"BAD\t{f.relative_to(ZERO_OPS)} calls {call}(), which needs {perm}. "
                        f"roles/cloudkms.cryptoKeyEncrypterDecrypter does not carry it, so this "
                        f"needs a custom role or a second binding that nobody has written -- and "
                        f"the failure appears on the first real project, as an IAM error, on the "
                        f"API server's encryption path"
                    )

    # ── criterion 3: unwrapping stays addressed to the parent key ────────────
    if KEYSTORE.is_dir():
        iface = KEYSTORE / "keystore.go"
        if not iface.is_file():
            findings.append(
                f"BAD\t{iface.relative_to(ZERO_OPS)} is missing; the key-authority contract has "
                f"no single definition"
            )
        else:
            checked += 1
            text = iface.read_text(encoding="utf-8")
            m = re.search(r"^\tUnwrap\(([^)]*)\)", text, re.M)
            if m is None:
                findings.append(
                    f"BAD\t{iface.relative_to(ZERO_OPS)}: the Store interface declares no "
                    f"Unwrap()"
                )
            elif len(m.group(1).split(",")) > 2:
                findings.append(
                    f"BAD\t{iface.relative_to(ZERO_OPS)}: Unwrap({m.group(1)}) takes a version. "
                    f"Decryption must be addressed to the PARENT key and let Cloud KMS resolve "
                    f"the version from the ciphertext -- naming a version makes every object "
                    f"written before the last rotation unreadable, and only on a cold read"
                )
            if re.search(r"^\tWrap\(([^)]*)\)", text, re.M) is None:
                findings.append(
                    f"BAD\t{iface.relative_to(ZERO_OPS)}: the Store interface declares no Wrap()"
                )
            elif len(re.search(r"^\tWrap\(([^)]*)\)", text, re.M).group(1).split(",")) < 3:
                findings.append(
                    f"BAD\t{iface.relative_to(ZERO_OPS)}: Wrap() takes no version argument, so "
                    f"encryption cannot name the version it observed"
                )

    # ── the reconciler may observe and update the key; never create or destroy ──
    #
    # WHY THIS IS A STRUCTURAL CHECK AND NOT A REVIEW CONVENTION. These two policies
    # carry the whole safety argument for putting the cluster's root encryption key under
    # a reconciler, and both fail silently in opposite directions:
    #
    #   Create    Crossplane cannot create the key the cluster it runs in depends on --
    #             the hub's API server cannot decrypt until the plugin reaches it. A
    #             resource that gained Create would appear to work on every box where the
    #             key already exists, and fail only on a rebuild.
    #
    #   Delete    a CryptoKey removed from Git must not schedule key destruction.
    #             Destroying a version makes every backup taken under it unreadable, and
    #             nothing about the day it is deleted reveals that -- the loss surfaces
    #             the next time somebody restores.
    if KMS_MANIFESTS.is_dir() and yaml is not None:
        for f in sorted(KMS_MANIFESTS.glob("*.yaml")):
            checked += 1
            try:
                docs = [d for d in yaml.safe_load_all(f.read_text(encoding="utf-8")) if d]
            except Exception as exc:  # noqa: BLE001
                findings.append(f"BAD\t{f.relative_to(ZERO_OPS)} could not be parsed: {exc}")
                continue
            for d in docs:
                kind = d.get("kind", "?")
                name = (d.get("metadata") or {}).get("name", "?")
                spec = d.get("spec") or {}
                where = f"{f.relative_to(ZERO_OPS)}: {kind}/{name}"

                policies = spec.get("managementPolicies")
                if policies is None:
                    findings.append(
                        f"BAD\t{where} sets no managementPolicies, so it defaults to the FULL "
                        f"lifecycle including Create and Delete on the key that encrypts etcd"
                    )
                    continue
                for forbidden, why in (
                    ("Create", "Crossplane cannot create the key the cluster it runs in "
                               "depends on; bootstrap does, and this adopts it"),
                    ("Delete", "removing this object from Git must not schedule key "
                               "destruction -- a destroyed version makes every backup taken "
                               "under it unreadable"),
                    ("Update", "Update needs cloudkms.cryptoKeys.update on the key that "
                               "encrypts THIS cluster's etcd, held by a workload inside it. "
                               "It cannot decrypt, but it can change the rotation period and "
                               "the destroy window. Remediation is an operator action from "
                               "outside the cluster"),
                ):
                    if forbidden in policies or "*" in policies:
                        findings.append(
                            f"BAD\t{where} allows {forbidden} (managementPolicies={policies}). "
                            f"{why}"
                        )
                if spec.get("deletionPolicy") != "Orphan":
                    findings.append(
                        f"BAD\t{where} has deletionPolicy={spec.get('deletionPolicy')!r}, want "
                        f"'Orphan'. Without it, deleting the Kubernetes object deletes the "
                        f"cloud key"
                    )
                # THE VERSION FLOOR MUST EXIST, because without it the drift check has
                # nothing to measure a regression against and passes forever. It is the
                # durable half of the no-reactivation rule: the plugin cannot hold that
                # invariant (its memory does not survive a restart, ADR-100 Amendment 2),
                # so Git holds it and a regression becomes a diff somebody approves.
                if kind == "CryptoKey":
                    floor = ((d.get("metadata") or {}).get("annotations") or {}).get(
                        "kms.soloz.io/primary-version-floor"
                    )
                    if floor is None:
                        findings.append(
                            f"BAD\t{where} carries no kms.soloz.io/primary-version-floor. "
                            f"`soloz kms drift` would have nothing to compare the live "
                            f"primary version against, so a reactivated key version would "
                            f"pass unnoticed -- which is the case the floor exists for"
                        )
                    else:
                        try:
                            if int(str(floor).strip()) < 1:
                                raise ValueError
                        except ValueError:
                            findings.append(
                                f"BAD\t{where} has primary-version-floor={floor!r}, which is "
                                f"not a positive integer"
                            )

                # The rotation period is ADR-100's cryptoperiod, and a reconciler that
                # held a different one would quietly overwrite what the CLI set.
                if kind == "CryptoKey":
                    rp = (spec.get("forProvider") or {}).get("rotationPeriod")
                    if rp != "7776000s":
                        findings.append(
                            f"BAD\t{where} sets rotationPeriod={rp!r}, want '7776000s' (90 days, "
                            f"assets.EncryptionRotationWindow). A reconciler holding a different "
                            f"cryptoperiod from the one the CLI sets is a second policy"
                        )

    # ── the static pod's QoS class ───────────────────────────────────────────
    #
    # A MISSING DEPENDENCY IS NOT A FINDING, and conflating the two is how a check stops
    # being believed. This half once reported
    #
    #     BAD  deploy/static-pod.yaml could not be parsed: No module named 'yaml'
    #
    # which reads as the manifest being broken. The person who hit it worked around it by
    # finding an interpreter that had PyYAML rather than reporting it -- the rational
    # response to a check that cries wolf, and also how a real finding in that file would
    # have been skipped past next time.
    if yaml is None:
        # SKIP ON A LAPTOP, FAIL IN CI, and the difference is the whole point. A skip is
        # right for a developer: the identifier and IAM checks still ran. It is wrong in
        # CI, where this file is the only thing asserting the static pod is Guaranteed,
        # non-root, digest-pinned and mounting leaves -- and where CI installs pre-commit
        # and yamllint and, until this was found, not PyYAML.
        if os.environ.get("CI"):
            findings.append(
                "BAD\tPyYAML is not installed, so the static pod and reconciler-policy "
                "checks did not run. In CI that is a gap in coverage rather than a local "
                "inconvenience: add `pip install pyyaml` to the workflow."
            )
        else:
            print(
                "SKIP\tthe static pod and reconciler-policy checks need PyYAML, which this "
                "interpreter does not have (`pip install pyyaml`, or run under `uv run "
                "--with pyyaml`). Everything above DID run; this is a missing dependency "
                "and not a finding about any manifest."
            )

    if STATIC_POD.is_file() and yaml is not None:
        checked += 1
        try:
            pod = yaml.safe_load(STATIC_POD.read_text(encoding="utf-8"))
            containers = ((pod or {}).get("spec") or {}).get("containers") or []
            if not containers:
                findings.append(
                    f"BAD\t{STATIC_POD.relative_to(ZERO_OPS)} defines no containers"
                )
            for c in containers:
                res = c.get("resources") or {}
                req, lim = res.get("requests") or {}, res.get("limits") or {}
                for field in ("cpu", "memory"):
                    if field not in req or field not in lim:
                        findings.append(
                            f"BAD\t{STATIC_POD.relative_to(ZERO_OPS)}: container "
                            f"{c.get('name')!r} does not set both a {field} request and a "
                            f"{field} limit, so the pod is not Guaranteed. It is on the API "
                            f"server's encryption path and should be the LAST thing kubelet "
                            f"reclaims, not the middle"
                        )
                    elif str(req[field]) != str(lim[field]):
                        findings.append(
                            f"BAD\t{STATIC_POD.relative_to(ZERO_OPS)}: container "
                            f"{c.get('name')!r} has {field} request {req[field]} and limit "
                            f"{lim[field]}. Unequal means BURSTABLE, which kubelet reclaims "
                            f"before Guaranteed -- and this process is on the API server's "
                            f"encryption path"
                        )
                sc = c.get("securityContext") or {}
                if sc.get("allowPrivilegeEscalation") is not False:
                    findings.append(
                        f"BAD\t{STATIC_POD.relative_to(ZERO_OPS)}: container "
                        f"{c.get('name')!r} does not set allowPrivilegeEscalation: false"
                    )
                if (sc.get("capabilities") or {}).get("drop") != ["ALL"]:
                    findings.append(
                        f"BAD\t{STATIC_POD.relative_to(ZERO_OPS)}: container "
                        f"{c.get('name')!r} does not drop ALL capabilities"
                    )
            # THE METRICS LISTENER MUST STAY ON LOOPBACK.
            #
            # This pod runs with hostNetwork: true, so a metrics endpoint bound to
            # 0.0.0.0 is published on every control-plane interface. The contents are not
            # secret -- latency and failure counts -- but they describe the encryption
            # path of the control plane to anything that can reach the node, and nothing
            # off-node needs them: a node-local collector scrapes 127.0.0.1.
            #
            # Checked because the failure is invisible: the plugin works identically
            # either way, and the only symptom is a port nobody looked for.
            for c in containers:
                for arg in c.get("args") or []:
                    if not str(arg).startswith("--metrics-addr"):
                        continue
                    value = str(arg).split("=", 1)[-1]
                    host = value.rsplit(":", 1)[0]
                    if host not in ("127.0.0.1", "localhost", "[::1]"):
                        findings.append(
                            f"BAD\t{STATIC_POD.relative_to(ZERO_OPS)}: container "
                            f"{c.get('name')!r} binds its metrics endpoint to {value!r}. "
                            f"The pod uses hostNetwork, so anything but loopback publishes "
                            f"the control plane's encryption telemetry on every interface"
                        )

            # The image must be pinned by digest. A tag would let a node resolve a
            # different binary on restart than the one that was tested, on the
            # component the API server cannot start without.
            for c in containers:
                img = str(c.get("image", ""))
                if "@sha256:" not in img:
                    findings.append(
                        f"BAD\t{STATIC_POD.relative_to(ZERO_OPS)}: container "
                        f"{c.get('name')!r} image {img!r} is not pinned by digest"
                    )
            # THE PLUGIN CONTAINER MUST NOT MOUNT THE CLUSTER PKI.
            #
            # ca.key can mint ANY identity in the cluster: a kubelet, an API server
            # client, an admin. The initContainer needs it for the seconds it takes to
            # sign one certificate, as root, with no network. The long-running process
            # that talks to Google must not be able to read it even if it is compromised,
            # and the plugin works identically either way -- so nothing about a wrong
            # mount would ever surface.
            for c in containers:
                for m in c.get("volumeMounts") or []:
                    path = str(m.get("mountPath", ""))
                    if path.startswith("/etc/kubernetes/pki"):
                        findings.append(
                            f"BAD\t{STATIC_POD.relative_to(ZERO_OPS)}: container "
                            f"{c.get('name')!r} mounts {path!r}. The cluster CA private key "
                            f"can mint any identity in the cluster; only the initContainer "
                            f"may see it, and only to sign the plugin's own certificate"
                        )

            # hostPath mounts must be leaves. /etc/kubernetes or /var/run would hand a
            # process that can request unwrapping of every DEK the control plane's PKI.
            for v in ((pod or {}).get("spec") or {}).get("volumes") or []:
                hp = (v.get("hostPath") or {}).get("path")
                if hp in ("/etc/kubernetes", "/var/run", "/", "/etc", "/var"):
                    findings.append(
                        f"BAD\t{STATIC_POD.relative_to(ZERO_OPS)}: volume {v.get('name')!r} "
                        f"mounts {hp!r}, which is a parent directory rather than a leaf"
                    )
        except Exception as exc:  # noqa: BLE001 - a malformed manifest is a finding
            findings.append(
                f"BAD\t{STATIC_POD.relative_to(ZERO_OPS)} could not be parsed: {exc}"
            )

    if checked == 0 and not findings:
        print("NONE")
        return 0

    for line in findings:
        print(line)
    if not findings:
        print(
            f"OK\t{checked} file(s): the identifier is derived from the CryptoKeyVersion name "
            f"and the configured suffix only, encryption names the observed version, Status "
            f"probes before it reports, unwrapping is addressed to the parent key, and no "
            f"call needs a permission the plugin's one IAM role lacks"
            # ONLY CLAIMED WHEN IT WAS ACTUALLY CHECKED. The static-pod half is skipped
            # without PyYAML, and an OK line that listed it anyway would be the same
            # false signal one layer up -- a pass that reads as covering more than it did.
            + (
                "; the static pod is Guaranteed and digest-pinned"
                if yaml is not None
                else "; the static pod was NOT checked (see SKIP above)"
            )
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
