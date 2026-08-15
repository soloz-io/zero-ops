#!/usr/bin/env bash
# DEPRECATED — superseded by setup-wsl2-node.sh (which installs the
# cilium-host-prep.service systemd unit) and the tailscale-watchdog.sh
# (which re-creates the mounts each cycle). No standalone action required.
echo "DEPRECATED: cilium cgroup2/BPF prep is installed by setup-wsl2-node.sh"
echo "            (cilium-host-prep.service) and maintained by tailscale-watchdog.sh."
echo "            See: ./provision-home-worker.sh"
exit 0
