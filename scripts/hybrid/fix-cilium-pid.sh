#!/usr/bin/env bash
# DEPRECATED — superseded by the WSL2 watchdog. tailscale-watchdog.sh's
# heal_cilium_pid() removes stale /var/run/cilium/cilium.pid autonomously
# every 60s. No standalone action required.
echo "DEPRECATED: stale Cilium pidfile clearing is handled automatically by"
echo "            tailscale-watchdog.sh (heal_cilium_pid) on each node."
echo "            See: ./provision-home-worker.sh"
exit 0
