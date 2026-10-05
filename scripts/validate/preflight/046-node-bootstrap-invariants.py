#!/usr/bin/env python3
"""Every Hetzner node template that re-asserts kubelet flags must set providerID.

WHY THIS EXISTS

CAPI matches Machines to Nodes BY `.spec.providerID`. Nothing else is consulted: a
Node that is Ready, scheduling pods and serving traffic but carrying no providerID
is, to CAPI, a Machine that never joined. The Machine keeps an empty NODENAME, the
MachineDeployment or KCP waits on it, and the roll reports
`NodeHealthy=False/NodeProvisioning` -- which names a provisioning step, not a
missing field, and sends everyone to the wrong place.

On this platform kubelet sets it itself, from the node's own metadata service,
because once `--node-ip` is a tailnet address the Hetzner CCM refuses to initialise
the node (it validates `alpha.kubernetes.io/provided-node-ip` against the addresses
the cloud reports) and so never writes the providerID. That makes the injection
block in `dynamic-node-ip.sh` the ONLY thing that sets it.

It was present in the hub control-plane template and in all six spoke worker
templates, and absent from the spoke control-plane template -- one copy out of
eight, in ~2500 lines of near-identical shell embedded in YAML. A replacement spoke
control-plane node could not join, and that cost a debugging session that started
at the CCM and the CAPI version skew before reaching the template.

TWO THINGS ARE CHECKED, because the block being present is not enough:

  presence   a template that ships dynamic-node-ip.sh and rewrites
             kubeadm-flags.env must contain the --provider-id injection. Missing it
             means a node that cannot ever be matched to its Machine.

  ORDER      the injection should come BEFORE the first tailscale guard. Those
             guards are `exit 0` -- not failures -- so a providerID block below them
             is skipped in silence on any node where tailscaled has not come up, and
             the script reports success. A postKubeadmCommand re-runs the script
             later, which is why this has been survivable; it is a dependency that
             should not exist, not an outage. The providerID needs no tailnet.

  delimiters  a `sed "s/../../"` whose pattern or replacement contains an
              unescaped `/` closes the expression early and fails at runtime with
              `sed: bad option in substitution expression`. Five copies of the
              --node-labels injection did exactly that, because the label value is
              `instance.hetzner.cloud/provided-by=cloud`. None of them had ever
              worked. Nothing noticed, because sed's failure is not the script's exit
              status and kubeadm sets the same label at join time anyway -- so the
              re-assertion that exists to survive a kubeadm upgrade would have been
              the thing that didn't.

ONLY TEMPLATES A CLUSTERCLASS REFERENCES ARE JUDGED. The spoke file still carries
the retired v2..v5 bootstrap chain that WS3 collapsed, and a template nothing
references cannot produce a node. They are counted and named in one line rather than
failing the build, because "fix the dead copy" is not work and silently dropping
them is the hidden gap this check exists to prevent.

SEVERITY IS BY ROLE, and deliberately not uniform:

  control plane   either defect is BAD. The spoke control-plane template is where a
                  missing block actually cost a roll, and a control plane that
                  cannot join is a cluster that cannot be repaired from inside.

  worker          a missing block is BAD; ORDER is a WARN. On this fleet the Hetzner
                  worker pools run zero replicas -- the dev workers are on-prem and
                  outside CAPI -- so fixing the order means a v7 of six templates
                  and a ClusterClass ref bump that rolls workers on stg and prod for
                  a robustness change. Recorded as open, not hidden.

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

# The source trees. `internal/platform/embedded` is a GENERATED copy of `manifests`
# (scripts/package/embed-platform-assets.sh), so checking it would double every
# finding and report a defect at a path nobody edits.
TREES = [
    ZERO_OPS / "manifests" / "providers" / "hetzner",
    ZERO_OPS / "internal" / "assets" / "manifests" / "classes",
]

SCRIPT_MARKER = "dynamic-node-ip.sh"
# What makes a block the providerID injection, rather than a comment about one. The
# comments around it mention providerID many times; the sed that writes the flag is
# the only thing that sets it.
INJECTION = re.compile(r"--provider-id=hcloud")
# The tailscale guards, which exit 0 and therefore silently skip everything below.
TS_GUARD = re.compile(r'^\s*(\[\s*-x\s*"\$TS_BIN"\s*\]|TS_IP=|\[\s*-n\s*"\$TS_IP"\s*\])')
# A template only needs the block if it rewrites kubelet's flag file at all.
REWRITES_FLAGS = re.compile(r"KUBELET_KUBEADM_ARGS")
# A sed substitution and its delimiter: `sed -i "s|a|b|g"` -> ('|', 'a|b|g'). The
# expression is taken to the closing quote, so a delimiter appearing inside it is
# visible to the count rather than hidden by a greedy match.
SED_EXPR = re.compile(r'sed\s+(?:-[a-zA-Z]+\s+)*"s(.)((?:[^"\\]|\\.)*)"')


def object_name(lines, idx):
    """The metadata.name of the YAML document containing line idx."""
    start = 0
    for i in range(idx, -1, -1):
        if lines[i].rstrip() == "---":
            start = i
            break
    for line in lines[start:idx]:
        m = re.match(r"^\s*name:\s*(\S+)", line)
        if m:
            return m.group(1)
    return "<unnamed>"


def referenced_templates(trees):
    """Template names a ClusterClass points at, mapped to the role they serve.

    A ClusterClass names its control-plane bootstrap under `controlPlane.ref` and each
    worker pool's under `workers.machineDeployments[].template.bootstrap.ref`, so the
    role is positional in the document rather than derivable from the name. Parsed by
    structure for that reason: `spokepool-burst-bootstrap-v6` is a worker template and
    nothing in its name says so.
    """
    roles = {}
    for tree in trees:
        if not tree.exists():
            continue
        for path in sorted(tree.rglob("*.yaml")):
            text = path.read_text(encoding="utf-8")
            if "kind: ClusterClass" not in text:
                continue
            lines = text.splitlines()
            # Which ClusterClass document each line belongs to, and whether we are
            # inside its controlPlane stanza or its workers stanza.
            section = None
            in_class = False
            for line in lines:
                if line.rstrip() == "---":
                    in_class = False
                    section = None
                    continue
                if re.match(r"^kind:\s*ClusterClass\s*$", line):
                    in_class = True
                    continue
                if not in_class:
                    continue
                if re.match(r"^  controlPlane:\s*$", line):
                    section = "control plane"
                    continue
                if re.match(r"^  workers:\s*$", line):
                    section = "worker"
                    continue
                if re.match(r"^  (infrastructure|patches|variables):\s*$", line):
                    section = None
                    continue
                m = re.match(r"^\s*name:\s*(\S+)\s*$", line)
                if m and section:
                    # First wins: a template referenced as both would be a different
                    # defect, and the stricter role is the one we want to judge by.
                    roles.setdefault(m.group(1), section)
    return roles


def check_file(path, roles, findings, unreferenced):
    text = path.read_text(encoding="utf-8")
    if SCRIPT_MARKER not in text:
        return 0

    lines = text.splitlines()
    rel = path.relative_to(ZERO_OPS)
    checked = 0

    # Each `- path: /usr/local/bin/dynamic-node-ip.sh` entry is one script. Its body
    # runs to the next `- path:` at the same or shallower indentation.
    starts = [i for i, l in enumerate(lines)
              if re.match(r"^\s*-\s*path:\s*/usr/local/bin/dynamic-node-ip\.sh\s*$", l)]
    for s in starts:
        indent = len(lines[s]) - len(lines[s].lstrip())
        end = len(lines)
        for j in range(s + 1, len(lines)):
            m = re.match(r"^(\s*)-\s*path:", lines[j])
            if m and len(m.group(1)) <= indent:
                end = j
                break
            if lines[j].rstrip() == "---":
                end = j
                break
        body = lines[s:end]
        blob = "\n".join(body)
        if not REWRITES_FLAGS.search(blob):
            continue

        name = object_name(lines, s)
        role = roles.get(name)
        if role is None:
            unreferenced.append(name)
            continue

        checked += 1

        for k, line in enumerate(body):
            for d, expr in SED_EXPR.findall(line):
                # An escaped delimiter is intentional; only bare ones close the
                # expression. `\/\/` in the hcloud:// replacement is the common case.
                bare = expr.replace("\\" + d, "")
                if bare.count(d) != 2:
                    findings.append(
                        f"BAD\t{rel}: {name} line {s + k + 1} has a sed substitution delimited by "
                        f"{d!r} whose pattern or replacement contains an unescaped {d!r}, so the "
                        f"expression closes early and sed fails at runtime with `bad option in "
                        f"substitution expression`. It never applies, and sed's failure is not the "
                        f"script's exit status, so nothing reports it. Use another delimiter"
                    )

        inj = next((k for k, l in enumerate(body) if INJECTION.search(l)), None)
        if inj is None:
            findings.append(
                f"BAD\t{rel}: {name} ({role}) ships dynamic-node-ip.sh and rewrites "
                f"kubeadm-flags.env but never injects --provider-id; a node from this template "
                f"joins the cluster and CAPI still reports its Machine as NodeProvisioning forever"
            )
            continue

        guard = next((k for k, l in enumerate(body) if TS_GUARD.match(l)), None)
        if guard is not None and guard < inj:
            sev = "BAD" if role == "control plane" else "WARN"
            findings.append(
                f"{sev}\t{rel}: {name} ({role}) injects --provider-id at line {s + inj + 1}, "
                f"AFTER the tailscale guard at line {s + guard + 1}. That guard is `exit 0`, so on "
                f"a node where tailscaled has not come up no providerID is set and the script "
                f"still succeeds. providerID needs no tailnet; move it above the guards"
            )

    return checked


def main():
    findings = []
    unreferenced = []
    checked = 0
    for tree in TREES:
        if not tree.exists():
            findings.append(
                f"BAD\t{tree.relative_to(ZERO_OPS)} does not exist; this check is looking in the "
                f"wrong place and would pass an unbootable template"
            )
    roles = referenced_templates(TREES)
    if not roles:
        print("BAD\tno ClusterClass named any node template; the reference map is empty, so this "
              "check would judge nothing and pass")
        return 0

    for tree in TREES:
        if not tree.exists():
            continue
        for path in sorted(tree.rglob("*.yaml")):
            checked += check_file(path, roles, findings, unreferenced)

    if checked == 0:
        print("NONE")
        return 0

    for line in findings:
        print(line)
    if unreferenced:
        print(
            f"OK\t{len(unreferenced)} template(s) ship dynamic-node-ip.sh and no ClusterClass "
            f"references them, so they cannot produce a node and were not judged: "
            f"{', '.join(sorted(set(unreferenced)))}"
        )
    if not any(l.startswith("BAD") for l in findings):
        print(f"OK\t{checked} referenced node template(s) inject --provider-id")
    return 0


if __name__ == "__main__":
    sys.exit(main())
