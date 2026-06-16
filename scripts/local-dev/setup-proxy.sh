#!/usr/bin/env bash
# scripts/local-dev/setup-proxy.sh
# Creates or restarts the Windows API proxy containers on the Mac.
# Bridges host.docker.internal:<port> → <windows-ip>:<port>
# so Docker containers (Hub kind cluster, Crossplane) can reach Windows.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WINDOWS_USER="${WINDOWS_USER:-Dell}"
WINDOWS_IP="${WINDOWS_IP:-192.168.1.18}"

echo "Setting up Windows API proxy for ${WINDOWS_IP}..."

# 1. socat proxy for the Windows Ingestion Engine API (port 6444)
echo "[1/3] Creating socat proxy (windows-api-proxy) for port 6444..."
docker rm -f windows-api-proxy 2>/dev/null || true
docker run -d --name windows-api-proxy --network host alpine/socat \
  tcp-listen:6444,fork,reuseaddr tcp:${WINDOWS_IP}:6444
echo "  ✅ windows-api-proxy forwarding :6444 → ${WINDOWS_IP}:6444"

# 2. Python proxy for Spoke cluster API (port 6445)
echo "[2/3] Starting Python proxy for port 6445..."
python3 "${SCRIPT_DIR}/mac-windows-proxy.py" 6445 "${WINDOWS_IP}" 6445 &
echo "  ✅ Python proxy forwarding :6445 → ${WINDOWS_IP}:6445"

# 3. Python proxy for Spoke cluster API (port 6446)
echo "[3/3] Starting Python proxy for port 6446..."
python3 "${SCRIPT_DIR}/mac-windows-proxy.py" 6446 "${WINDOWS_IP}" 6446 &
echo "  ✅ Python proxy forwarding :6446 → ${WINDOWS_IP}:6446"

echo ""
echo "All proxies active:"
echo "  host.docker.internal:6444 → ${WINDOWS_IP}:6444  (Ingestion Engine)"
echo "  host.docker.internal:6445 → ${WINDOWS_IP}:6445  (Spoke cluster #1)"
echo "  host.docker.internal:6446 → ${WINDOWS_IP}:6446  (Spoke cluster #2)"
echo ""
echo "To stop the Python proxies: pkill -f mac-windows-proxy.py"
echo "To stop the socat proxy:    docker rm -f windows-api-proxy"
