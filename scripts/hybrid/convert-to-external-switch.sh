#!/usr/bin/env bash
# =============================================================================
# scripts/hybrid/convert-to-external-switch.sh
#
# Convert a home-lab Hyper-V host from the Internal switch + NetNat layout to an
# External switch bound to a physical adapter (ADR-046 §13).
#
# WHY THIS EXISTS
# ---------------
# provision-flatcar-worker.sh historically created the guest network as
#
#     New-VMSwitch -SwitchType Internal   +   New-NetNat 172.30.0.0/24
#
# An Internal switch has no physical uplink, so every guest sits behind a SECOND
# NAT (Hyper-V NetNat, then the home router). That layout is correct while one
# box hosts the whole cluster, and silently wrong the moment a cluster spans two
# boxes:
#
#   * Each box independently creates the SAME 172.30.0.0/24 island, each owning
#     172.30.0.1. Guests on different boxes cannot ARP each other and cannot be
#     routed to each other either — the peer address is "local" on both hosts.
#   * Double NAT defeats Tailscale's UDP hole-punching, so peers fall back to a
#     DERP relay.
#   * Worse, tailscaled advertises every local address as a candidate endpoint,
#     including Cilium's cilium_host (10.244.x.x). That address IS reachable —
#     through the VXLAN tunnel, which itself rides Tailscale. Peers select it and
#     the "direct" path becomes Tailscale -> VXLAN -> Tailscale. Observed effect:
#     5s TCP handshakes, stalled TLS, EAI_AGAIN storms, and Postgres sessions
#     that never establish.
#
# Binding the switch to a real adapter removes the inner NAT. Guests become
# first-class citizens on the LAN, Tailscale selects a real endpoint, and the
# circular pod-CIDR path stops being the only reachable candidate.
#
# THE NON-OBVIOUS PART
# --------------------
# On Wi-Fi, -AllowManagementOS $true is MANDATORY. With $false the adapter is
# taken away from the host's WLAN service — which is what maintains the 802.11
# association — so it drops to Status: Disconnected and the guest sits at
# "State: no-carrier (configuring)" forever. This looks exactly like "Hyper-V
# External switches don't work over Wi-Fi", which is the wrong conclusion.
# Verified working on a Realtek 8821CU USB adapter, the least favourable case.
#
# Usage:
#   ./convert-to-external-switch.sh --node 3                 # by registry index
#   ./convert-to-external-switch.sh --target LENOVO@192.168.1.11
#   ./convert-to-external-switch.sh --node 3 --adapter "Wi-Fi 2"
#   ./convert-to-external-switch.sh --node 3 --dry-run
#   ./convert-to-external-switch.sh --node 3 --revert
# =============================================================================
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
TARGET=""
NODE_IDX=""
ADAPTER=""
SWITCH_NAME="Hybrid-Switch"
NAT_NAME="Hybrid-NAT"
DRY_RUN=0
REVERT=0

usage() {
  sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --node)    NODE_IDX="$2"; shift 2 ;;
    --target)  TARGET="$2"; shift 2 ;;
    --adapter) ADAPTER="$2"; shift 2 ;;
    --switch)  SWITCH_NAME="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    --revert)  REVERT=1; shift ;;
    -h|--help) usage 0 ;;
    *) echo "ERROR: unknown option $1" >&2; usage 1 ;;
  esac
done

# ── Resolve the SSH target from the node registry ────────────────────────────
if [[ -z "$TARGET" ]]; then
  ENV_FILE="${HERE}/home-lab.env"
  [[ -f "$ENV_FILE" ]] || { echo "ERROR: no --target and $ENV_FILE missing" >&2; exit 1; }
  # shellcheck source=/dev/null
  source "$ENV_FILE"
  [[ -n "$NODE_IDX" ]] || { echo "ERROR: pass --node N or --target USER@IP" >&2; exit 1; }
  TARGET=$(echo "$HOME_WORKER_NODES" | grep -vE '^\s*$' | sed -n "${NODE_IDX}p" | cut -d'|' -f2)
  [[ -n "$TARGET" ]] || { echo "ERROR: no registry entry at index ${NODE_IDX}" >&2; exit 1; }
fi

# PowerShell emits CRLF. Strip the CR here rather than at each call site: a
# trailing \r makes every string comparison downstream silently false — an empty
# field reads as "\r", so `$4 != ""` matches and the wrong adapter gets picked.
win_ps() { ssh -o BatchMode=yes -o ConnectTimeout=30 -o StrictHostKeyChecking=no "$TARGET" \
             "powershell -NoProfile -ExecutionPolicy Bypass -Command -" 2>/dev/null | tr -d '\r'; }

echo "=== External switch conversion ==="
echo "    Target: ${TARGET}"
echo "    Switch: ${SWITCH_NAME}"
echo ""

# ── Revert path ──────────────────────────────────────────────────────────────
if [[ "$REVERT" == "1" ]]; then
  echo "    Reverting ${SWITCH_NAME} to Internal + NetNat..."
  echo "
\$s = Get-VMSwitch -Name '${SWITCH_NAME}' -ErrorAction SilentlyContinue
if (\$s -and \$s.SwitchType -eq 'External') { Remove-VMSwitch -Name '${SWITCH_NAME}' -Force }
if (!(Get-VMSwitch -Name '${SWITCH_NAME}' -ErrorAction SilentlyContinue)) {
  New-VMSwitch -Name '${SWITCH_NAME}' -SwitchType Internal | Out-Null
}
\$ifIdx = (Get-NetAdapter -Name ('vEthernet (' + '${SWITCH_NAME}' + ')') -ErrorAction SilentlyContinue).ifIndex
if (\$ifIdx -and !(Get-NetIPAddress -InterfaceIndex \$ifIdx -IPAddress '172.30.0.1' -ErrorAction SilentlyContinue)) {
  New-NetIPAddress -IPAddress '172.30.0.1' -PrefixLength 24 -InterfaceIndex \$ifIdx -ErrorAction SilentlyContinue | Out-Null
}
if (!(Get-NetNat -Name '${NAT_NAME}' -ErrorAction SilentlyContinue)) {
  New-NetNat -Name '${NAT_NAME}' -InternalIPInterfaceAddressPrefix '172.30.0.0/24' -ErrorAction SilentlyContinue | Out-Null
}
'REVERTED'
" | win_ps | sed 's/^/    /'
  exit 0
fi

# ── Inventory: adapters, switches, and which adapter carries our SSH ─────────
SSH_HOST_IP="${TARGET#*@}"
echo "    [1/4] Inventory..."
INVENTORY=$(echo "
'MGMT_IP=${SSH_HOST_IP}'
Get-NetAdapter | Where-Object { \$_.Status -eq 'Up' -and \$_.InterfaceDescription -notmatch 'Hyper-V Virtual' } |
  ForEach-Object {
    \$ip = (Get-NetIPAddress -InterfaceIndex \$_.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue |
            Select-Object -First 1).IPAddress
    'ADAPTER|' + \$_.Name + '|' + \$_.MediaType + '|' + \$ip
  }
Get-VMSwitch | ForEach-Object { 'SWITCH|' + \$_.Name + '|' + \$_.SwitchType }
" | win_ps)
echo "$INVENTORY" | grep -E '^(ADAPTER|SWITCH|MGMT_IP)' | sed 's/^/      /'

CURRENT_TYPE=$(echo "$INVENTORY" | awk -F'|' -v s="$SWITCH_NAME" '$1=="SWITCH" && $2==s {print $3}')
if [[ "$CURRENT_TYPE" == "External" ]]; then
  echo "    ✓ ${SWITCH_NAME} is already External — nothing to do"
  exit 0
fi

# ── Choose the adapter to bind ───────────────────────────────────────────────
# Prefer an adapter that is NOT carrying this SSH session. Binding the
# management adapter briefly interrupts host networking, and on a single-adapter
# host a driver that mishandles the bind leaves the box needing physical access.
if [[ -z "$ADAPTER" ]]; then
  # Candidates are physical adapters that are not carrying this SSH session.
  # "Network Bridge" and similar pseudo-adapters are excluded: they are Up and
  # look plausible, but binding one produces a switch that passes no traffic.
  # An empty IP does not disqualify an adapter — one already bound to another
  # switch reports no address yet is still the right thing to bind.
  ADAPTER=$(echo "$INVENTORY" | awk -F'|' -v mgmt="$SSH_HOST_IP" \
            '$1=="ADAPTER" && $4!=mgmt && $2 !~ /Bridge|Loopback/ && $4!="" {print $2; exit}')
  [[ -n "$ADAPTER" ]] || ADAPTER=$(echo "$INVENTORY" | awk -F'|' -v mgmt="$SSH_HOST_IP" \
            '$1=="ADAPTER" && $4!=mgmt && $2 !~ /Bridge|Loopback/ {print $2; exit}')
  if [[ -n "$ADAPTER" ]]; then
    echo "    [2/4] Binding spare adapter '${ADAPTER}' (management stays on ${SSH_HOST_IP})"
  else
    ADAPTER=$(echo "$INVENTORY" | awk -F'|' -v mgmt="$SSH_HOST_IP" \
              '$1=="ADAPTER" && $4==mgmt {print $2; exit}')
    [[ -n "$ADAPTER" ]] || { echo "ERROR: could not determine an adapter to bind" >&2; exit 1; }
    echo "    [2/4] ⚠️  Only the management adapter '${ADAPTER}' is available."
    echo "          Binding it will interrupt this SSH session. AllowManagementOS keeps"
    echo "          the host online via the new vEthernet, but if the driver mishandles"
    echo "          the bind the box needs PHYSICAL access to recover."
    echo "          Re-run with --adapter to override, or connect a second adapter."
  fi
else
  echo "    [2/4] Binding adapter '${ADAPTER}' (explicit)"
fi

if [[ "$DRY_RUN" == "1" ]]; then
  echo "    [dry-run] would run:"
  echo "      Remove-NetNat -Name ${NAT_NAME}"
  echo "      Remove-VMSwitch -Name ${SWITCH_NAME}"
  echo "      New-VMSwitch -Name ${SWITCH_NAME} -NetAdapterName '${ADAPTER}' -AllowManagementOS \$true"
  exit 0
fi

# ── Convert ──────────────────────────────────────────────────────────────────
# -AllowManagementOS $true is not optional on Wi-Fi; see the header. The WLAN
# profile is reconnected explicitly afterwards because dropping and rebinding an
# 802.11 adapter can leave it associated-but-idle on some drivers.
echo "    [3/4] Converting..."
echo "
\$ErrorActionPreference = 'Continue'
\$wasWifi = ((Get-NetAdapter -Name '${ADAPTER}' -ErrorAction SilentlyContinue).MediaType -match '802.11')
\$ssid = \$null
if (\$wasWifi) { \$ssid = ((netsh wlan show interfaces) | Select-String 'SSID\s+:' | Select-Object -First 1) -replace '.*:\s*','' }

Get-VMNetworkAdapter -All | Where-Object { \$_.SwitchName -eq '${SWITCH_NAME}' } |
  ForEach-Object { 'DETACH|' + \$_.VMName }

if (Get-NetNat -Name '${NAT_NAME}' -ErrorAction SilentlyContinue) {
  Remove-NetNat -Name '${NAT_NAME}' -Confirm:\$false
  'REMOVED_NAT'
}
if (Get-VMSwitch -Name '${SWITCH_NAME}' -ErrorAction SilentlyContinue) {
  Remove-VMSwitch -Name '${SWITCH_NAME}' -Force
  'REMOVED_SWITCH'
}
Start-Sleep -Seconds 5
if (\$wasWifi -and \$ssid) { netsh wlan connect name=\"\$ssid\" interface='${ADAPTER}' | Out-Null; Start-Sleep -Seconds 10 }

New-VMSwitch -Name '${SWITCH_NAME}' -NetAdapterName '${ADAPTER}' -AllowManagementOS \$true -ErrorAction SilentlyContinue | Out-Null
Start-Sleep -Seconds 15
'SWITCH_TYPE=' + (Get-VMSwitch -Name '${SWITCH_NAME}' -ErrorAction SilentlyContinue).SwitchType
'ADAPTER_STATUS=' + (Get-NetAdapter -Name '${ADAPTER}' -ErrorAction SilentlyContinue).Status
" | win_ps | sed 's/^/      /'

# ── Verify ───────────────────────────────────────────────────────────────────
# A created switch is NOT proof of a working bridge — the Wi-Fi failure mode is
# "switch exists, adapter disconnected, guest has no carrier". Assert both.
echo "    [4/4] Verifying..."
RESULT=$(echo "
\$sw = Get-VMSwitch -Name '${SWITCH_NAME}' -ErrorAction SilentlyContinue
\$ad = Get-NetAdapter -Name '${ADAPTER}' -ErrorAction SilentlyContinue
if (\$sw.SwitchType -eq 'External' -and \$ad.Status -eq 'Up') { 'OK' } else { 'FAIL' }
'  switch=' + \$sw.SwitchType + ' adapter=' + \$ad.Status
" | win_ps)
echo "$RESULT" | sed 's/^/      /'

if echo "$RESULT" | grep -q '^OK'; then
  echo ""
  echo "    ✓ ${SWITCH_NAME} is External and '${ADAPTER}' stayed associated."
  echo ""
  echo "    NEXT: guests still hold the old static 172.30.0.x address and must be"
  echo "    re-provisioned (or have 10-static.network switched to DHCP=yes) before"
  echo "    they can use the LAN. Re-run provision-flatcar-worker.sh for this node."
else
  echo ""
  echo "    ✗ Conversion did not settle. Roll back with:"
  echo "      $0 --target ${TARGET} --revert"
  exit 1
fi
