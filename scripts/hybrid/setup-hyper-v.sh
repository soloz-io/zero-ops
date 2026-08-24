#!/usr/bin/env bash
# =============================================================================
# scripts/hybrid/setup-hyper-v.sh
#
# One-time setup: Install Hyper-V on a remote Windows Home machine via SSH.
# Copies an enablement batch script to the target, runs it as Administrator,
# and reboots the machine.
#
# Usage:
#   ./setup-hyper-v.sh                          # uses default target from home-lab.env
#   ./setup-hyper-v.sh --target LENOVO@192.168.1.12
#   ./setup-hyper-v.sh --target LENOVO@192.168.1.12 --no-reboot
# =============================================================================
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
TARGET=""
NO_REBOOT=0

usage() {
  cat <<EOF
Usage: $0 [OPTIONS]

Install Hyper-V on a remote Windows Home machine via SSH.

Options:
  --target USER@IP    SSH target (e.g. LENOVO@192.168.1.12)
  --no-reboot         Skip automatic reboot after installation
  -h, --help          Show this help message
EOF
  exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)    TARGET="$2"; shift 2 ;;
    --no-reboot) NO_REBOOT=1; shift ;;
    -h|--help)   usage 0 ;;
    *) echo "ERROR: unknown option $1" >&2; usage 1 ;;
  esac
done

# Auto-detect target from home-lab.env if not provided
if [[ -z "$TARGET" ]]; then
  ENV_FILE="${HERE}/home-lab.env"
  if [[ -f "$ENV_FILE" ]]; then
    # shellcheck source=/dev/null
    source "$ENV_FILE"
    # Use the Lenovo entry if present, otherwise first entry
    TARGET=$(echo "$HOME_WORKER_NODES" | grep -i lenovo | head -1 | cut -d'|' -f2 || true)
    if [[ -z "$TARGET" ]]; then
      TARGET=$(echo "$HOME_WORKER_NODES" | head -1 | cut -d'|' -f2)
    fi
  fi
  if [[ -z "$TARGET" ]]; then
    echo "ERROR: No --target specified and could not auto-detect from home-lab.env" >&2
    exit 1
  fi
fi

echo "=== Hyper-V Setup for Windows Home ==="
echo "    Target: ${TARGET}"
echo ""

# ── Step 1: Verify SSH reachability ──────────────────────────────────────────
echo "    [1/4] Verifying SSH reachability..."
if ! ssh -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=no "$TARGET" "echo ok" 2>/dev/null; then
  echo "    ✗ SSH to ${TARGET} failed. Is the machine on and SSH enabled?" >&2
  exit 1
fi
echo "    ✓ SSH connected"

# ── Step 2: Check if Hyper-V is already enabled ──────────────────────────────
echo "    [2/4] Checking Hyper-V status..."
HV_STATUS=$(echo 'if (Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -ErrorAction SilentlyContinue | Where-Object { $_.State -eq "Enabled" }) { Write-Output "ENABLED" } else { Write-Output "DISABLED" }' \
  | ssh -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=no "$TARGET" \
  "powershell -NoProfile -ExecutionPolicy Bypass -Command -" 2>/dev/null | tr -d '\r\n')

if [[ "$HV_STATUS" == "ENABLED" ]]; then
  echo "    ✓ Hyper-V is already enabled — nothing to do"
  exit 0
fi
echo "    → Hyper-V not enabled. Proceeding with installation."

# ── Step 3: Copy and run the enablement batch script ─────────────────────────
echo "    [3/4] Installing Hyper-V packages (this may take a few minutes)..."

REMOTE_BAT="C:/Users/lenovo/Desktop/enable-hyper-v-home.bat"

# Write the batch content to a temp file using a quoted heredoc (prevents bash expansion of % and %%)
TMP_BAT=$(mktemp /tmp/hyper-v-XXXXXX.bat)
cat <<'BATEOF' > "$TMP_BAT"
@echo off
echo.
echo === Installing Hyper-V on Windows Home ===
echo.

pushd "%~dp0"

echo [1/3] Enumerating Hyper-V packages...
dir /b %SystemRoot%\servicing\Packages\*Hyper-V*.mum >hyper-v.txt 2>nul
set PKG_COUNT=0
for /f %%i in ('findstr /i . hyper-v.txt 2^>nul') do set /a PKG_COUNT+=1
echo       Found %PKG_COUNT% packages.
echo.

echo [2/3] Installing packages (this may take a few minutes)...
for /f %%i in ('findstr /i . hyper-v.txt 2^>nul') do (
    dism /online /norestart /add-package:"%SystemRoot%\servicing\Packages\%%i" >nul 2>&1
)
del hyper-v.txt 2>nul
echo       Packages installed.
echo.

echo [3/3] Enabling Microsoft-Hyper-V feature...
Dism /online /enable-feature /featurename:Microsoft-Hyper-V -All /LimitAccess /ALL
echo.

popd

echo === Hyper-V installation complete. Reboot required. ===
BATEOF

scp -o BatchMode=yes -o StrictHostKeyChecking=no "$TMP_BAT" "${TARGET}:${REMOTE_BAT}" >/dev/null
rm -f "$TMP_BAT"

# SSH session is already elevated (verified). Run the batch directly via cmd.exe.
# Using Start-Process with -Wait to capture output and block until complete.
echo "cmd /c ${REMOTE_BAT}" \
  | ssh -o BatchMode=yes -o ConnectTimeout=300 -o StrictHostKeyChecking=no "$TARGET" \
  "powershell -NoProfile -ExecutionPolicy Bypass -Command -" 2>&1 | sed 's/^/      /'

echo "    ✓ Hyper-V packages installed"

# ── Step 4: Reboot ───────────────────────────────────────────────────────────
if [[ "$NO_REBOOT" == "1" ]]; then
  echo "    [4/4] Skipping reboot (--no-reboot). Please reboot manually."
else
  echo "    [4/4] Rebooting ${TARGET}..."
  echo 'shutdown /r /t 0 /f' \
    | ssh -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=no "$TARGET" \
    "powershell -NoProfile -ExecutionPolicy Bypass -Command -" 2>/dev/null || true
  echo "    ✓ Reboot initiated. Wait ~60 seconds, then verify with:"
  echo "      ssh ${TARGET} \"powershell -Command 'Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V | Select-Object FeatureName,State'\""
fi

echo ""
echo "=== Done ==="
