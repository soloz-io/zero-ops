#!/usr/bin/env bash
# DEPRECATED — superseded by provision-home-worker.sh (single entry point).
# Joining + reboot resilience are phases [5/7] and [6/7] of the orchestrator.
# The node's systemd timer re-runs home-worker-join.sh autonomously.
# New usage: ./provision-home-worker.sh [--node N]
exec "$(cd "$(dirname "$0")" && pwd)/provision-home-worker.sh" "$@"
