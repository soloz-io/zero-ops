#!/usr/bin/env bash
# DEPRECATED — superseded by provision-home-worker.sh (single entry point).
# Hardening is now phase [2/7] of the orchestrator, run automatically.
# New usage: ./provision-home-worker.sh [--node N] [--ts-authkey KEY]
exec "$(cd "$(dirname "$0")" && pwd)/provision-home-worker.sh" "$@"
