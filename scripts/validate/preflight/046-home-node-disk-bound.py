#!/usr/bin/env python3
"""Static validation: home-lab node disk bound (ADR-046).

Checks that:
1. scripts/hybrid/kubelet-host-disk.conf declares KubeletConfiguration v1beta1
   with all 6 GC/eviction keys (image GC 50%/40%, evictionHard 40% nodefs/imagefs,
   5% inodes, 100Mi memory).
2. scripts/hybrid/provision-flatcar-worker.sh includes
   --config-dir=/etc/kubernetes/kubelet.conf.d in both kubelet ExecStart
   definitions (Ignition and Cloud-Config), references kubelet-host-disk.conf,
   and does not hardcode threshold values.
3. scripts/hybrid/free-spoke.sh delivers kubelet-host-disk.conf to guests,
   ensures --config-dir is present on kubelet.service, and does not hardcode
   threshold values.
"""

import re
import sys
from pathlib import Path
import yaml


def check_conf(conf_path: Path, findings: list[str]) -> bool:
    if not conf_path.is_file():
        findings.append(f"BAD\t{conf_path} does not exist")
        return False

    try:
        data = yaml.safe_load(conf_path.read_text(encoding="utf-8"))
    except Exception as exc:
        findings.append(f"BAD\t{conf_path.name} failed to parse as YAML: {exc}")
        return False

    if not isinstance(data, dict):
        findings.append(f"BAD\t{conf_path.name} root is not a YAML mapping")
        return False

    if data.get("kind") != "KubeletConfiguration":
        findings.append(f"BAD\t{conf_path.name} kind is '{data.get('kind')}', expected 'KubeletConfiguration'")
        return False

    if data.get("apiVersion") != "kubelet.config.k8s.io/v1beta1":
        findings.append(f"BAD\t{conf_path.name} apiVersion is '{data.get('apiVersion')}', expected 'kubelet.config.k8s.io/v1beta1'")
        return False

    ok = True
    if data.get("imageGCHighThresholdPercent") != 50:
        findings.append(f"BAD\t{conf_path.name} imageGCHighThresholdPercent is {data.get('imageGCHighThresholdPercent')}, expected 50")
        ok = False

    if data.get("imageGCLowThresholdPercent") != 40:
        findings.append(f"BAD\t{conf_path.name} imageGCLowThresholdPercent is {data.get('imageGCLowThresholdPercent')}, expected 40")
        ok = False

    eviction = data.get("evictionHard")
    if not isinstance(eviction, dict):
        findings.append(f"BAD\t{conf_path.name} evictionHard is missing or not a mapping")
        return False

    expected_eviction = {
        "nodefs.available": "40%",
        "imagefs.available": "40%",
        "nodefs.inodesFree": "5%",
        "memory.available": "100Mi",
    }
    for key, expected_val in expected_eviction.items():
        val = eviction.get(key)
        if val != expected_val:
            findings.append(f"BAD\t{conf_path.name} evictionHard['{key}'] is '{val}', expected '{expected_val}'")
            ok = False

    if ok:
        findings.append(
            f"OK\t{conf_path.name} declares KubeletConfiguration with all 6 early GC and evictionHard keys (50%/40%, 40% nodefs/imagefs, 5% inodes, 100Mi memory)"
        )
    return ok


def check_provisioner(provision_path: Path, findings: list[str]) -> bool:
    if not provision_path.is_file():
        findings.append(f"BAD\t{provision_path} does not exist")
        return False

    content = provision_path.read_text(encoding="utf-8")
    ok = True

    # Count kubelet ExecStart lines with --config-dir
    execstart_matches = re.findall(
        r"ExecStart=[^\n]*kubelet[^\n]*--config-dir=/etc/kubernetes/kubelet\.conf\.d",
        content,
    )
    if len(execstart_matches) != 2:
        findings.append(
            f"BAD\t{provision_path.name} has {len(execstart_matches)} kubelet ExecStart line(s) with --config-dir=/etc/kubernetes/kubelet.conf.d (expected 2: Ignition and Cloud-Config)"
        )
        ok = False

    # Check reference to kubelet-host-disk.conf
    if "kubelet-host-disk.conf" not in content:
        findings.append(f"BAD\t{provision_path.name} does not reference kubelet-host-disk.conf")
        ok = False

    # Check that thresholds are not hardcoded in the script
    # Look for code lines (excluding comments) defining imageGC thresholds
    code_lines = [
        line.strip()
        for line in content.splitlines()
        if not line.strip().startswith("#") and not line.strip().startswith("//")
    ]
    code_text = "\n".join(code_lines)
    if (
        "imageGCHighThresholdPercent" in code_text
        or "imageGCLowThresholdPercent" in code_text
    ):
        findings.append(f"BAD\t{provision_path.name} hardcodes imageGC threshold keys instead of referencing kubelet-host-disk.conf")
        ok = False

    if ok:
        findings.append(
            f"OK\t{provision_path.name} configures --config-dir in both ExecStart definitions and references kubelet-host-disk.conf without hardcoded thresholds"
        )
    return ok


def check_free_spoke(free_path: Path, findings: list[str]) -> bool:
    if not free_path.is_file():
        findings.append(f"BAD\t{free_path} does not exist")
        return False

    content = free_path.read_text(encoding="utf-8")
    ok = True

    if "kubelet-host-disk.conf" not in content:
        findings.append(f"BAD\t{free_path.name} does not reference kubelet-host-disk.conf")
        ok = False

    if "/etc/kubernetes/kubelet.conf.d/50-host-disk.conf" not in content:
        findings.append(f"BAD\t{free_path.name} does not reference /etc/kubernetes/kubelet.conf.d/50-host-disk.conf")
        ok = False

    if "--config-dir=/etc/kubernetes/kubelet.conf.d" not in content:
        findings.append(f"BAD\t{free_path.name} does not reference --config-dir=/etc/kubernetes/kubelet.conf.d")
        ok = False

    # Check that thresholds are not hardcoded in the script code
    code_lines = [
        line.strip()
        for line in content.splitlines()
        if not line.strip().startswith("#")
    ]
    code_text = "\n".join(code_lines)
    if (
        "imageGCHighThresholdPercent" in code_text
        or "imageGCLowThresholdPercent" in code_text
    ):
        findings.append(f"BAD\t{free_path.name} hardcodes imageGC threshold keys instead of referencing kubelet-host-disk.conf")
        ok = False

    if ok:
        findings.append(
            f"OK\t{free_path.name} applies kubelet-host-disk.conf to /etc/kubernetes/kubelet.conf.d/50-host-disk.conf and updates ExecStart without hardcoded thresholds"
        )
    return ok


def main() -> int:
    repo_root = Path(__file__).resolve().parents[3]
    hybrid_dir = repo_root / "scripts" / "hybrid"

    conf_path = hybrid_dir / "kubelet-host-disk.conf"
    provision_path = hybrid_dir / "provision-flatcar-worker.sh"
    free_path = hybrid_dir / "free-spoke.sh"

    findings: list[str] = []
    check_conf(conf_path, findings)
    check_provisioner(provision_path, findings)
    check_free_spoke(free_path, findings)

    for line in findings:
        print(line)

    if any(line.startswith("BAD") for line in findings):
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
