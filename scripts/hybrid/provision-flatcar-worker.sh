#!/usr/bin/env bash
# =============================================================================
# scripts/hybrid/provision-flatcar-worker.sh
#
# THE single entry point to provision a Hyper-V Flatcar Container Linux worker
# node on Windows home-lab hardware into the hybrid spoke cluster (ADR-046 §13).
#
# Architecture:
#   - Windows Host: Hyper-V Gen2 VM with Dynamic Memory and AutoStart
#   - OS: Flatcar Container Linux (Immutable, read-only /usr, native cgroups v2)
#   - Storage: Zero host disk formatting; VM runs from isolated 20GB VHDX
#   - Provisioning: Declarative Ignition config + offline binaries delivered via DVD ISO
#   - Kubernetes: Kubeadm join v1.31.6 via one-shot systemd service
#   - CNI: Cilium v1.17.18 (clean native cgroup & eBPF, no cgroup hacks)
#   - Storage Addon: local-path-provisioner on persistent /var/local-path-provisioner
#   - Network Mesh: Native Tailscale daemon connecting to tailnet
#
# Phases per node:
#   [0/6] Offline binary cache verification on Mac
#   [1/6] SSH reachability gate
#   [2/6] Windows host & Hyper-V preparation (power/lid, Hyper-V feature, directories)
#   [3/6] Flatcar base VHDX & Ignition ISO bundle preparation
#   [4/6] Provision Generation 2 Hyper-V VM with attached Ignition DVD ISO
#   [5/6] Wait for Flatcar First-Boot, Tailscale mesh, and Kubeadm join
#   [6/6] Verify node Ready in Spoke cluster (success gate)
#
# Usage:
#   ./provision-flatcar-worker.sh                          # provision all nodes
#   ./provision-flatcar-worker.sh --node 1                 # node index 1 only
#   ./provision-flatcar-worker.sh --verify                 # verify Ready status
# =============================================================================
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="${HERE}/home-lab.env"
ONLY_NODE=""
TS_AUTHKEY=""
VSWITCH_NAME="Hybrid-Switch"

# Sustained CPU ceiling applied to the Windows host (ADR-046 §24.5). These hosts are
# thin laptops running Kubernetes node VMs; at 100% they reach the ACPI critical
# thermal trip and the firmware shuts them down mid-provision. Override per host with
# --host-cpu-max if a box has healthier cooling.
HOST_CPU_MAX_PCT="${HOST_CPU_MAX_PCT:-70}"
TARGET_CLUSTER="all"    # Default: 'all' (auto-routes nodes to Hub/Spoke per home-lab.env)
MEMORY_BYTES="0"         # 0 = auto-detect (14GB or TotalHostRAM - 2GB)
MIN_MEMORY_BYTES="0"     # 0 = auto-detect (2GB)
MAX_MEMORY_BYTES="0"     # 0 = auto-detect (TotalHostRAM - 2GB)
CPU_COUNT="0"            # 0 = auto-detect (all host logical cores)
# Disk ceilings are chosen per target cluster; no flag should be needed for a
# normal provision. The VHDX is dynamically expanding and Resize-VHD only raises
# the ceiling, so a generous number costs nothing on the host until written.
#
# The old flat 20GB default was far too small once the hub moved its workloads to
# home-lab nodes (ADR-046 §19): a 20GB VHDX yields ~13GB usable after the Flatcar
# OS, and a hub node carries ArgoCD, cert-manager, Kyverno, Crossplane, ESO, NATS,
# Redis and Infisical AND platform-db's 10Gi local-path PVC. It hit
# DiskPressure=True, the kubelet tainted the node, and with the control plane
# tainted as well nothing could be scheduled anywhere.
HUB_DISK_GB_DEFAULT=100    # whole platform + platform-db PVC
SPOKE_DISK_GB_DEFAULT=60   # tenant workloads + shared-cnpg PVC
DISK_SIZE_BYTES=""         # set only by --disk-gb; empty means "use the target default"
CLI_DISK_SET=0
K8S_VERSION="v1.31.6"
TAILSCALE_VERSION="1.102.2"
FLATCAR_RELEASE_CHANNEL="stable"
UPGRADE_FLATCAR="false"
CACHE_DIR="${HOME}/.cache/soloz/flatcar-bin"
MODE="provision"

usage() {
  local exit_code="${1:-0}"
  cat <<EOF
Usage: $0 [OPTIONS]

Options:
  --cluster <hub|spoke|all> Target cluster (default: 'all', auto-routes per home-lab.env).
  --spoke <name>       Shorthand to target a specific spoke cluster.
  --node N             Provision only node index N (1-based from home-lab.env).
  --env FILE           Path to environment file (default: scripts/hybrid/home-lab.env)
  --ts-authkey KEY     Tailscale auth-key (auto-loaded from k8-secrets/tailscale/authkey if omitted).
  --vswitch NAME       Hyper-V Virtual Switch name (default: 'Default Switch').
  --upgrade-flatcar    Re-download fresh Flatcar base VHDX image from current release.
  --memory-gb N        OVERRIDE startup memory in GB (default: per-node from home-lab.env registry).
  --max-memory-gb N    OVERRIDE maximum memory ceiling in GB (default: per-node from registry).
  --min-memory-gb N    OVERRIDE minimum dynamic memory floor in GB (default: per-node from registry).
  --cpus N             OVERRIDE virtual CPU count (default: per-node from registry).
  --disk-gb N          OVERRIDE virtual disk ceiling in GB (default: 100 hub / 60 spoke).
  --verify             Only check Ready status of registered nodes; no changes.
  -h, --help           Show this help message

Phases per node:
  [0/6] Offline binary cache verification on Mac
  [1/6] SSH reachability gate
  [2/6] Windows host & Hyper-V preparation
  [3/6] Flatcar base VHDX & Ignition ISO bundle preparation
  [4/6] Provision Generation 2 Hyper-V VM with attached Ignition DVD ISO
  [5/6] Wait for Flatcar First-Boot, Tailscale mesh & Kubeadm join
  [6/6] Verify node Ready in target cluster (success gate)

Exit code 0 only if all targeted nodes are Ready.
EOF
  exit "$exit_code"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --cluster)          TARGET_CLUSTER="$2"; shift 2 ;;
    --spoke)            TARGET_CLUSTER="spoke"; HYBRID_SPOKE_NAME="$2"; shift 2 ;;
    --node)             ONLY_NODE="$2"; shift 2 ;;
    --env)              ENV_FILE="$2"; shift 2 ;;
    --ts-authkey)       TS_AUTHKEY="$2"; shift 2 ;;
    --vswitch)          VSWITCH_NAME="$2"; shift 2 ;;
    --host-cpu-max)     HOST_CPU_MAX_PCT="$2"; shift 2 ;;
    --upgrade-flatcar)  UPGRADE_FLATCAR="true"; shift ;;
    --memory-gb)        MEMORY_BYTES="$(($2 * 1024 * 1024 * 1024))"; shift 2 ;;
    --max-memory-gb) MAX_MEMORY_BYTES="$(($2 * 1024 * 1024 * 1024))"; shift 2 ;;
    --min-memory-gb) MIN_MEMORY_BYTES="$(($2 * 1024 * 1024 * 1024))"; shift 2 ;;
    --cpus)          CPU_COUNT="$2"; shift 2 ;;
    --disk-gb)       DISK_SIZE_BYTES="$(($2 * 1024 * 1024 * 1024))"; CLI_DISK_SET=1; shift 2 ;;
    --verify)        MODE="verify"; shift ;;
    -h|--help)       usage 0 ;;
    *) echo "ERROR: unknown option $1" >&2; usage 1 ;;
  esac
done

# Snapshot explicit CLI capacity (if any) — per-node registry values become the
# default and are applied at each node iteration; CLI values always win.
CLI_MEMORY_BYTES="$MEMORY_BYTES"
CLI_MIN_MEMORY_BYTES="$MIN_MEMORY_BYTES"
CLI_MAX_MEMORY_BYTES="$MAX_MEMORY_BYTES"
CLI_CPU_COUNT="$CPU_COUNT"

REPO_ROOT="$(cd "${HERE}/../.." && pwd)"
export ZERO_OPS_DIR="${REPO_ROOT}"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: Environment file not found: $ENV_FILE" >&2
  echo "       Copy home-lab.env.example → home-lab.env and fill in real values." >&2
  exit 1
fi
# shellcheck source=/dev/null
source "$ENV_FILE"

# Auto-fetch Tailscale authkey from k8-secrets if not passed via CLI
DEFAULT_TS_AUTHKEY_FILE="${REPO_ROOT}/k8-secrets/tailscale/authkey"
if [[ -z "$TS_AUTHKEY" && -f "$DEFAULT_TS_AUTHKEY_FILE" ]]; then
  TS_AUTHKEY="$(tr -d '\r\n' < "$DEFAULT_TS_AUTHKEY_FILE")"
fi

if [[ ! -f "$HUB_KUBECONFIG" ]]; then
  echo "ERROR: HUB_KUBECONFIG not found: $HUB_KUBECONFIG" >&2
  echo "       Check home-lab.env — the hub kubeconfig must exist on the Mac." >&2
  exit 1
fi

# Locate Mac SSH public keys for core user injection
SSH_PUBKEYS_JSON=""
for key_path in "$HOME/.ssh/"*.pub; do
  if [[ -f "$key_path" ]]; then
    k=$(tr -d '\r\n' < "$key_path")
    if [[ -n "$k" ]]; then
      if [[ -n "$SSH_PUBKEYS_JSON" ]]; then
        SSH_PUBKEYS_JSON="${SSH_PUBKEYS_JSON}, \"${k}\""
      else
        SSH_PUBKEYS_JSON="\"${k}\""
      fi
    fi
  fi
done
if [[ -z "$SSH_PUBKEYS_JSON" ]]; then
  echo "ERROR: No SSH public keys found in ~/.ssh/*.pub" >&2
  exit 1
fi

# ── Prerequisite check for required tools on Mac ─────────────────────────────
for tool in kubectl hdiutil curl ssh scp base64; do
  if ! command -v "$tool" &>/dev/null; then
    echo "ERROR: Required command '${tool}' not found on Mac." >&2
    echo "       Please install '${tool}' (e.g. brew install ${tool}) and re-run." >&2
    exit 1
  fi
done

win_ps() { # Execute PowerShell on the Windows host via SSH (buffered)
  local SSH_TARGET="$1" PS_SCRIPT="$2"
  printf '%s\n' "$PS_SCRIPT" | ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
    "$SSH_TARGET" "powershell -NoProfile -ExecutionPolicy Bypass -Command -" 2>&1 || true
}

win_ps_stream() { # Stream PowerShell output live from Windows host via SSH
  local SSH_TARGET="$1" PS_SCRIPT="$2" FILTER="${3:-✓|→|===|⚠|Error|Failed|Exception}"
  printf '%s\n' "$PS_SCRIPT" | ssh -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=no \
    "$SSH_TARGET" "powershell -NoProfile -ExecutionPolicy Bypass -Command -" 2>&1 | \
    grep --line-buffered -E "$FILTER" | sed 's/^/      /' || true
}

# ── Phase 0: Offline binary cache verification on Mac ────────────────────────
phase_prep_binaries() {
  echo "    [0/6] Verifying offline worker binary cache on Mac..."
  mkdir -p "${CACHE_DIR}"

  # Also check /tmp/soloz-flatcar-cache
  if [[ -d "/tmp/soloz-flatcar-cache" ]]; then
    cp -n /tmp/soloz-flatcar-cache/* "${CACHE_DIR}/" 2>/dev/null || true
  fi

  if [[ ! -f "${CACHE_DIR}/kubelet" ]]; then
    echo "      → Downloading kubelet ${K8S_VERSION}..."
    curl -sSL -o "${CACHE_DIR}/kubelet" "https://dl.k8s.io/release/${K8S_VERSION}/bin/linux/amd64/kubelet"
    chmod +x "${CACHE_DIR}/kubelet"
  fi

  if [[ ! -f "${CACHE_DIR}/kubeadm" ]]; then
    echo "      → Downloading kubeadm ${K8S_VERSION}..."
    curl -sSL -o "${CACHE_DIR}/kubeadm" "https://dl.k8s.io/release/${K8S_VERSION}/bin/linux/amd64/kubeadm"
    chmod +x "${CACHE_DIR}/kubeadm"
  fi

  if [[ ! -f "${CACHE_DIR}/kubectl" ]]; then
    echo "      → Downloading kubectl ${K8S_VERSION}..."
    curl -sSL -o "${CACHE_DIR}/kubectl" "https://dl.k8s.io/release/${K8S_VERSION}/bin/linux/amd64/kubectl"
    chmod +x "${CACHE_DIR}/kubectl"
  fi

  local TS_VER_FILE="${CACHE_DIR}/.tailscale-version"
  if [[ ! -f "${CACHE_DIR}/tailscale" || ! -f "${CACHE_DIR}/tailscaled" || ! -f "$TS_VER_FILE" || "$(< "$TS_VER_FILE")" != "$TAILSCALE_VERSION" ]]; then
    echo "      → Downloading Tailscale ${TAILSCALE_VERSION}..."
    local TS_TMP
    TS_TMP=$(mktemp -d /tmp/tailscale-dl-XXXXXX)
    curl -sSL "https://pkgs.tailscale.com/stable/tailscale_${TAILSCALE_VERSION}_amd64.tgz" | tar -xz -C "$TS_TMP" --strip-components=1
    cp "${TS_TMP}/tailscale" "${TS_TMP}/tailscaled" "${CACHE_DIR}/"
    chmod +x "${CACHE_DIR}/tailscale" "${CACHE_DIR}/tailscaled"
    echo "$TAILSCALE_VERSION" > "$TS_VER_FILE"
    rm -rf "$TS_TMP"
  fi

  local CNI_VERSION="v1.6.0"
  if [[ ! -f "${CACHE_DIR}/loopback" ]]; then
    echo "      → Downloading CNI plugins ${CNI_VERSION}..."
    local CNI_TMP
    CNI_TMP=$(mktemp -d /tmp/cni-dl-XXXXXX)
    curl -sSL "https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-linux-amd64-${CNI_VERSION}.tgz" | tar -xz -C "$CNI_TMP"
    cp "$CNI_TMP"/* "${CACHE_DIR}/" 2>/dev/null || true
    chmod +x "${CACHE_DIR}"/* 2>/dev/null || true
    rm -rf "$CNI_TMP"
  fi
  echo "      ✓ All worker binaries cached in ${CACHE_DIR}"
}

# ── Phase 2: Windows host & Hyper-V preparation ──────────────────────────────
phase_prep_hyperv() {
  local SSH_TARGET="$1" HOSTNAME="$2"
  echo "    [2/6] Preparing Windows host & Hyper-V..."
  local PS_PREP="
Write-Output '=== [2a/2d] Power & Lid Settings ==='
try {
  powercfg /setacvalueindex SCHEME_CURRENT 4f971e89-eebd-4455-a8de-9e59040e7347 5ca83367-6e45-459f-a27b-476b1d01c936 0
  powercfg /setdcvalueindex SCHEME_CURRENT 4f971e89-eebd-4455-a8de-9e59040e7347 5ca83367-6e45-459f-a27b-476b1d01c936 0
  powercfg /change standby-timeout-ac 0
  powercfg /change hibernate-timeout-ac 0
  powercfg /change monitor-timeout-ac 5
  # Critical-battery action -> Do nothing. These boxes are laptops-as-servers with
  # exhausted batteries reporting 0 mWh while on AC; Windows otherwise enacts the
  # critical action ON AC and hibernates a running node out from under the cluster.
  powercfg /setacvalueindex SCHEME_CURRENT SUB_BATTERY BATACTIONCRIT 0
  powercfg /setdcvalueindex SCHEME_CURRENT SUB_BATTERY BATACTIONCRIT 0
  powercfg /setactive SCHEME_CURRENT
  Write-Output '    ✓ Lid Close -> Do Nothing; Sleep/Hibernate -> Never; Critical battery -> None'
} catch { Write-Output ('    ⚠ Power: ' + \$_.Exception.Message) }

Write-Output '=== [2b/2d] Thermal Protection ==='
# Why this exists (ADR-046 §24.5): on 2026-08-23 a Dell Latitude E7470 (15W
# i5-6300U, 2 cores / 4 threads) hosting two Kubernetes node VMs shut itself down
# four times in one hour:
#     Kernel-Power 86: 'The system was shut down due to a critical thermal event.'
#     ACPI Thermal Zone = Intel(R) Dynamic Platform Thermal Framework, _CRT = 373K
# esifsvc enacts that trip via shutdown.exe as NT AUTHORITY\LOCAL SERVICE, which
# surfaces as event 1074 and looks like a clean administrative shutdown. It is not:
# it is the hardware protecting itself, and no amount of Kubernetes-side work
# survives it. Capping sustained CPU is the only software lever that helps.
try {
  \$g = ((powercfg /getactivescheme) -join ' ') -replace '.*GUID:\s*([0-9a-f-]{36}).*','\$1'
  \$SUBPROC = '54533251-82be-4824-96c1-47b60b740d00'
  \$BOOST   = 'be337238-0d82-4146-a960-4f3749d470c7'
  # Sustained ceiling. 70% keeps a 15W part under its trip point under container
  # load; slower bootstraps are strictly better than a shutdown mid-provision.
  powercfg /setacvalueindex \$g SUB_PROCESSOR PROCTHROTTLEMAX $HOST_CPU_MAX_PCT
  powercfg /setdcvalueindex \$g SUB_PROCESSOR PROCTHROTTLEMAX $HOST_CPU_MAX_PCT
  # Turbo is where the thermal spikes come from and buys little here. The setting
  # is hidden by default on most OEM images, hence the un-hide first.
  powercfg /attributes \$SUBPROC \$BOOST -ATTRIB_HIDE | Out-Null
  powercfg /setacvalueindex \$g \$SUBPROC \$BOOST 0
  powercfg /setdcvalueindex \$g \$SUBPROC \$BOOST 0
  # Active cooling ramps the fan BEFORE throttling. Passive throttles first and
  # lets heat accumulate, which is how the critical trip is reached.
  powercfg /setacvalueindex \$g SUB_PROCESSOR SYSCOOLPOL 1
  powercfg /setdcvalueindex \$g SUB_PROCESSOR SYSCOOLPOL 1
  powercfg /setactive \$g
  Write-Output ('    ✓ CPU max ' + $HOST_CPU_MAX_PCT + '%, turbo off, active cooling')
  \$therm = Get-WinEvent -FilterHashtable @{LogName='System'; Id=86; ProviderName='Microsoft-Windows-Kernel-Power'} -MaxEvents 3 -ErrorAction SilentlyContinue
  if (\$therm) {
    Write-Output '    ⚠ This host HAS shut down on thermal trips before:'
    # The '→' prefix is load-bearing: win_ps_stream filters host output through
    # grep -E '✓|→|===|⚠|Error|Failed|Exception', so an unprefixed line is silently
    # discarded and the warning would print with no evidence under it.
    \$therm | ForEach-Object { Write-Output ('      → ' + \$_.TimeCreated.ToString('yyyy-MM-dd HH:mm:ss')) }
    Write-Output '      → If these continue, the fix is physical: clean the fan, repaste, improve airflow.'
  }
} catch { Write-Output ('    ⚠ Thermal: ' + \$_.Exception.Message) }

Write-Output '=== [2c/2d] Hyper-V Feature Check ==='
try {
  \$os = Get-CimInstance Win32_OperatingSystem -ErrorAction SilentlyContinue
  \$feature = Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -ErrorAction SilentlyContinue
  if (\$feature -and \$feature.State -eq 'Enabled') {
    \$hostInfo = if (\$os) { \$os.Caption.Trim() + ' (Build ' + \$os.BuildNumber + ')' } else { 'Enabled' }
    Write-Output ('    ✓ Hyper-V is enabled (' + \$hostInfo + ')')
  } else {
    Write-Output '    ⚠ Hyper-V not enabled. Enabling Microsoft-Hyper-V...'
    Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All -NoRestart | Out-Null
    Write-Output '    ✓ Enabled Hyper-V'
  }
} catch { Write-Output ('    ⚠ Hyper-V feature: ' + \$_.Exception.Message) }

Write-Output '=== [2d/2d] Flatcar Directories & Virtual Switch ==='
try {
  \$solozDir = 'C:\ProgramData\soloz\flatcar'
  if (!(Test-Path \$solozDir)) { New-Item -ItemType Directory -Path \$solozDir -Force | Out-Null }
  
  \$switch = Get-VMSwitch -Name '$VSWITCH_NAME' -ErrorAction SilentlyContinue
  if (\$switch) {
    Write-Output '    ✓ Virtual switch ($VSWITCH_NAME) present'
  } else {
    Write-Output '    ⚠ Switch $VSWITCH_NAME not found, listing available switches:'
    Get-VMSwitch | ForEach-Object { Write-Output ('      - ' + \$_.Name) }
  }
  Write-Output 'HYPERV-PREP=OK'
} catch { Write-Output ('HYPERV-PREP=ERR: ' + \$_.Exception.Message) }
"
  win_ps_stream "$SSH_TARGET" "$PS_PREP"
}

# ── Phase 3: Flatcar base VHDX & Ignition ISO bundle preparation ─────────────
phase_prep_flatcar_and_ignition() {
  local SSH_TARGET="$1" VM_NAME="$2" NODE_IDX="$3"
  echo "    [3/6] Preparing Flatcar base VHDX & Ignition ISO bundle (${VM_NAME})..."

  # 1. Ensure Flatcar base VHDX exists on Windows Host
  local PS_VHDX="
\$solozDir = 'C:\ProgramData\soloz\flatcar';
\$baseVhdx = Join-Path \$solozDir 'flatcar-base.vhdx';
\$rawVhdx = Join-Path \$solozDir 'flatcar_production_hyperv_vhdx_image.vhdx';
\$zipPath = Join-Path \$solozDir 'flatcar-base.vhdx.bz2';

if ('$UPGRADE_FLATCAR' -eq 'true') {
  Remove-Item \$baseVhdx -Force -ErrorAction SilentlyContinue
  Remove-Item \$rawVhdx -Force -ErrorAction SilentlyContinue
}

# Consolidate legacy named base image if present
if (!(Test-Path \$baseVhdx) -and (Test-Path \$rawVhdx)) {
  Move-Item -Path \$rawVhdx -Destination \$baseVhdx -Force
}

if (!(Test-Path \$baseVhdx)) {
  Write-Output '    → Downloading Flatcar ${FLATCAR_RELEASE_CHANNEL} Hyper-V base image...'
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
  \$url = 'https://${FLATCAR_RELEASE_CHANNEL}.release.flatcar-linux.net/amd64-usr/current/flatcar_production_hyperv_vhdx_image.vhdx.bz2'
  Invoke-WebRequest -Uri \$url -OutFile \$zipPath -UseBasicParsing
  
  Write-Output '    → Decompressing bz2 archive...'
  Push-Location \$solozDir
  & bzip2 -d -k -f \$zipPath
  if (Test-Path \$rawVhdx) {
    Move-Item -Path \$rawVhdx -Destination \$baseVhdx -Force
  }
  Pop-Location
  Remove-Item \$zipPath -Force -ErrorAction SilentlyContinue
  Write-Output '    ✓ Base Flatcar VHDX initialized'
} else {
  Write-Output '    ✓ Base Flatcar VHDX already cached'
}
"
  win_ps_stream "$SSH_TARGET" "$PS_VHDX"

  # 2. Stop and remove existing VM to release file locks (checks legacy, hub, and spoke names)
  local PS_STOP="
\$vmNames = @('$VM_NAME', 'flatcar-node-${NODE_IDX}', 'flatcar-hub-node-${NODE_IDX}', 'flatcar-spoke-node-${NODE_IDX}') | Select-Object -Unique;
foreach (\$name in \$vmNames) {
  \$existingVM = Get-VM -Name \$name -ErrorAction SilentlyContinue
  if (\$existingVM) {
    Stop-VM -Name \$name -Force -TurnOff -ErrorAction SilentlyContinue
    Remove-VM -Name \$name -Force -ErrorAction SilentlyContinue
  }
}
"
  win_ps "$SSH_TARGET" "$PS_STOP" >/dev/null

  # 2b. Clean up any stale Kubernetes node registration (idempotent across Hub & Spoke)
  echo "    → Cleaning up any stale Kubernetes node registration (${VM_NAME})..."
  kubectl --kubeconfig="${HUB_KUBECONFIG}" delete node "${VM_NAME}" "flatcar-node-${NODE_IDX}" "flatcar-hub-node-${NODE_IDX}" "flatcar-spoke-node-${NODE_IDX}" --ignore-not-found=true >/dev/null 2>&1 || true

  local SPOKE_KC
  SPOKE_KC=$(mktemp /tmp/hybrid-spoke-XXXXXX)
  if kubectl --kubeconfig="${HUB_KUBECONFIG}" \
        get secret "${HYBRID_SPOKE_NAME}-kubeconfig" \
        -n platform-capi -o jsonpath='{.data.value}' 2>/dev/null \
        | base64 -d > "$SPOKE_KC" 2>/dev/null; then
    kubectl --kubeconfig="$SPOKE_KC" delete node "${VM_NAME}" "flatcar-node-${NODE_IDX}" "flatcar-hub-node-${NODE_IDX}" "flatcar-spoke-node-${NODE_IDX}" --ignore-not-found=true >/dev/null 2>&1 || true
    rm -f "$SPOKE_KC"
  else
    rm -f "$SPOKE_KC"
  fi

  # 3. Fetch or mint join credentials
  local JOIN_TOKEN="" CA_CERT_HASH="" CONTROL_PLANE_ENDPOINT=""
  local CLUSTER_TARGET="${4:-$TARGET_CLUSTER}"

  if [[ "$CLUSTER_TARGET" == "hub" ]]; then
    echo "    → Minting Hub bootstrap token for node ${VM_NAME}..."
    local TOKEN_ID TOKEN_SECRET
    TOKEN_ID=$(python3 -c "import secrets, string; print(''.join(secrets.choice(string.ascii_lowercase + string.digits) for _ in range(6)))")
    TOKEN_SECRET=$(python3 -c "import secrets, string; print(''.join(secrets.choice(string.ascii_lowercase + string.digits) for _ in range(16)))")
    JOIN_TOKEN="${TOKEN_ID}.${TOKEN_SECRET}"

    cat <<EOF | kubectl --kubeconfig="${HUB_KUBECONFIG}" apply -f - >/dev/null
apiVersion: v1
kind: Secret
metadata:
  name: bootstrap-token-${TOKEN_ID}
  namespace: kube-system
type: bootstrap.kubernetes.io/token
stringData:
  token-id: "${TOKEN_ID}"
  token-secret: "${TOKEN_SECRET}"
  usage-bootstrap-authentication: "true"
  usage-bootstrap-signing: "true"
  auth-extra-groups: "system:bootstrappers:kubeadm:default-node-token"
EOF

    CA_CERT_HASH=$(kubectl --kubeconfig="${HUB_KUBECONFIG}" get cm -n kube-system kube-root-ca.crt -o jsonpath='{.data.ca\.crt}' 2>/dev/null \
      | openssl x509 -pubkey -noout 2>/dev/null \
      | openssl rsa -pubin -outform der 2>/dev/null \
      | openssl dgst -sha256 -hex 2>/dev/null \
      | sed 's/^.* //')

    local SERVER
    SERVER=$(kubectl --kubeconfig="${HUB_KUBECONFIG}" config view --minify -o jsonpath='{.clusters[0].cluster.server}')
    CONTROL_PLANE_ENDPOINT="${SERVER}"
  else
    # Fetch Spoke join credentials from Hub
    local JOIN_SECRET_NAME="${HYBRID_SPOKE_NAME}-home-worker-join"
    JOIN_TOKEN=$(kubectl --kubeconfig="${HUB_KUBECONFIG}" \
      get secret "${JOIN_SECRET_NAME}" -n platform-capi \
      -o jsonpath="{.data.node-${NODE_IDX}-token}" 2>/dev/null | base64 -d || true)
    CA_CERT_HASH=$(kubectl --kubeconfig="${HUB_KUBECONFIG}" \
      get secret "${JOIN_SECRET_NAME}" -n platform-capi \
      -o jsonpath='{.data.ca-cert-hash}' 2>/dev/null | base64 -d || true)
    CONTROL_PLANE_ENDPOINT=$(kubectl --kubeconfig="${HUB_KUBECONFIG}" \
      get secret "${JOIN_SECRET_NAME}" -n platform-capi \
      -o jsonpath="{.data.control-plane-endpoint}" 2>/dev/null | base64 -d || true)

    if [[ -z "$JOIN_TOKEN" || -z "$CA_CERT_HASH" || -z "$CONTROL_PLANE_ENDPOINT" ]]; then
      echo "    ✗ Spoke join credentials not ready on Hub. Wait for hub-operator." >&2
      return 1
    fi
  fi

  # Normalize endpoint (host:port)
  CONTROL_PLANE_ENDPOINT="${CONTROL_PLANE_ENDPOINT#https://}"
  CONTROL_PLANE_ENDPOINT="${CONTROL_PLANE_ENDPOINT#http://}"
  CONTROL_PLANE_ENDPOINT="${CONTROL_PLANE_ENDPOINT%%/*}"

  # 4. Generate Ignition configuration (spec 3.4.0) with base64 data URIs
  echo "    → Generating declarative Ignition config..."
  local TMP_DIR
  TMP_DIR=$(mktemp -d /tmp/ignition-gen-XXXXXX)
  trap 'rm -rf "$TMP_DIR"' RETURN

  local NODE_LABELS="workload-location=home,topology.kubernetes.io/zone=home,node.kubernetes.io/exclude-from-external-load-balancers=true"
  if [[ "$CLUSTER_TARGET" == "hub" ]]; then
    NODE_LABELS="workload-location=home,hub-role=worker,topology.kubernetes.io/zone=home,node.kubernetes.io/exclude-from-external-load-balancers=true"
  fi

  local HOSTNAME_B64 SYSCTL_B64 MODULES_B64 NETWORK_B64 TS_AUTHKEY_B64
  HOSTNAME_B64=$(printf '%s' "${VM_NAME}" | base64 | tr -d '\r\n')
  SYSCTL_B64=$(printf 'net.ipv4.ip_forward = 1\nnet.bridge.bridge-nf-call-iptables = 1\nnet.bridge.bridge-nf-call-ip6tables = 1\nfs.inotify.max_user_watches = 524288\nfs.inotify.max_user_instances = 8192\n' | base64 | tr -d '\r\n')
  MODULES_B64=$(printf 'overlay\nbr_netfilter\n' | base64 | tr -d '\r\n')
  local STATIC_NET="[Match]
Name=eth*
Name=!cilium_* !lxc* !tailscale*

[Network]
DHCP=no
Address=172.30.0.$((10 + NODE_IDX))/24
Gateway=172.30.0.1
DNS=8.8.8.8
DNS=1.1.1.1
"
  NETWORK_B64=$(printf '%s' "$STATIC_NET" | base64 | tr -d '\r\n')
  TS_AUTHKEY_B64=$(printf '%s' "${TS_AUTHKEY}" | base64 | tr -d '\r\n')

  local ISO_ROOT="${TMP_DIR}/iso-root"
  mkdir -p "${ISO_ROOT}/ignition" "${ISO_ROOT}/bin"

  # Helper scripts on ISO
  cat <<'SH_EOF' > "${ISO_ROOT}/bin/setup-node.sh"
#!/bin/sh
mkdir -p /opt/bin /opt/cni/bin /etc/cni/net.d /var/lib/tailscale /var/lib/kubelet /etc/kubernetes/manifests /media/iso /etc/containerd
mount -t iso9660 /dev/disk/by-label/config-2 /media/iso 2>/dev/null || mount -t iso9660 /dev/disk/by-label/CONFIG-2 /media/iso 2>/dev/null || mount -t iso9660 /dev/sr0 /media/iso 2>/dev/null || true
if [ -d /media/iso/bin ]; then
  cp -n /media/iso/bin/* /opt/bin/
  chmod +x /opt/bin/*
  for cni_tool in loopback host-local portmap bridge bandwidth firewall tuning vlan macvlan ipvlan; do
    [ -f "/media/iso/bin/${cni_tool}" ] && cp -n "/media/iso/bin/${cni_tool}" /opt/cni/bin/
  done
  chmod +x /opt/cni/bin/* 2>/dev/null || true
fi
modprobe overlay || true
modprobe br_netfilter || true
sysctl --system || true

# Cilium BPF & cgroup2 prep
mkdir -p /sys/fs/bpf /run/cilium/cgroupv2 /sys/fs/cgroup/kubepods.slice /sys/fs/cgroup/kubepods-burstable.slice /sys/fs/cgroup/kubepods-besteffort.slice
mount -t bpf bpf /sys/fs/bpf 2>/dev/null || true
mount --make-shared /sys/fs/bpf 2>/dev/null || true
mount -t cgroup2 none /run/cilium/cgroupv2 2>/dev/null || true
mount --make-shared /run/cilium/cgroupv2 2>/dev/null || true

# Bootstrap DNAT for Cilium if kubelet.conf exists
if [ -f /etc/kubernetes/kubelet.conf ]; then
  EP=$(awk '/server:/{print $2}' /etc/kubernetes/kubelet.conf 2>/dev/null | sed 's#https://##')
  CP_HOST=${EP%%:*}; CP_PORT=${EP##*:}
  if [ -n "$CP_HOST" ] && [ -n "$CP_PORT" ]; then
    sysctl -w net.ipv4.conf.all.route_localnet=1 2>/dev/null || true
    iptables -t nat -I OUTPUT 1 -d 10.96.0.1 -p tcp --dport 443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
    iptables -t nat -I PREROUTING 1 -d 10.96.0.1 -p tcp --dport 443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
    iptables -t nat -I OUTPUT 1 -d 127.0.0.1 -p tcp --dport 6443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
    iptables -t nat -I POSTROUTING 1 -d "${CP_HOST}" -j MASQUERADE 2>/dev/null || true
  fi
fi

containerd config default > /etc/containerd/config.toml 2>/dev/null || true
sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml 2>/dev/null || true

cat <<'CRI_EOF' > /etc/crictl.yaml
runtime-endpoint: unix:///run/containerd/containerd.sock
image-endpoint: unix:///run/containerd/containerd.sock
timeout: 10
debug: false
CRI_EOF

ip addr add 172.30.0.__NODE_IP__/24 dev eth0 2>/dev/null || true
ip link set eth0 up 2>/dev/null || true
ip route add default via 172.30.0.1 dev eth0 2>/dev/null || true
systemctl restart systemd-networkd 2>/dev/null || true
SH_EOF
  sed -i '' "s/__NODE_IP__/$((10 + NODE_IDX))/g" "${ISO_ROOT}/bin/setup-node.sh" 2>/dev/null || sed -i "s/__NODE_IP__/$((10 + NODE_IDX))/g" "${ISO_ROOT}/bin/setup-node.sh"
  chmod +x "${ISO_ROOT}/bin/setup-node.sh"

  cat <<'SH_EOF' > "${ISO_ROOT}/bin/join-cluster.sh"
#!/bin/sh
export PATH="/opt/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH"

for i in $(seq 1 30); do
  [ -S /run/containerd/containerd.sock ] && break
  sleep 1
done

for i in $(seq 1 30); do
  TS_IP=$(/opt/bin/tailscale ip -4 2>/dev/null || true)
  [ -n "$TS_IP" ] && break
  sleep 1
done

echo "[join] Starting kubeadm join against __ENDPOINT__ (node: __VM_NAME__, tailscale IP: ${TS_IP})..."
for i in $(seq 1 30); do
  if /opt/bin/kubeadm join __ENDPOINT__ \
      --token __TOKEN__ \
      --discovery-token-ca-cert-hash sha256:__HASH__ \
      --node-name __VM_NAME__ \
      --cri-socket unix:///run/containerd/containerd.sock \
      --ignore-preflight-errors=all \
      --v=2 2>&1; then
    # 1. IMMEDIATELY insert bootstrap DNAT: 10.96.0.1:443 & status.hostIP:6443 -> Control Plane Endpoint
    EP=$(awk '/server:/{print $2}' /etc/kubernetes/kubelet.conf 2>/dev/null | sed 's#https://##')
    CP_HOST=${EP%%:*}; CP_PORT=${EP##*:}
    if [ -n "$CP_HOST" ] && [ -n "$CP_PORT" ]; then
      sysctl -w net.ipv4.conf.all.route_localnet=1 2>/dev/null || true
      iptables -t nat -I OUTPUT 1 -d 10.96.0.1 -p tcp --dport 443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
      iptables -t nat -I PREROUTING 1 -d 10.96.0.1 -p tcp --dport 443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
      if [ -n "$TS_IP" ]; then
        iptables -t nat -I OUTPUT 1 -d "${TS_IP}" -p tcp --dport 6443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
        iptables -t nat -I PREROUTING 1 -d "${TS_IP}" -p tcp --dport 6443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
      fi
      iptables -t nat -I OUTPUT 1 -d 127.0.0.1 -p tcp --dport 6443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
      iptables -t nat -I POSTROUTING 1 -d "${CP_HOST}" -j MASQUERADE 2>/dev/null || true
      echo "[join] ✓ Inserted bootstrap DNAT: 10.96.0.1:443 & ${TS_IP}:6443 -> ${CP_HOST}:${CP_PORT}"
    fi

    # 2. Dynamic kubelet --node-ip assertion and restart
    TS_IP=$(/opt/bin/tailscale ip -4 2>/dev/null || true)
    if [ -n "$TS_IP" ] && [ -f /var/lib/kubelet/kubeadm-flags.env ]; then
      sed -i "s/KUBELET_KUBEADM_ARGS=\"/KUBELET_KUBEADM_ARGS=\"--node-ip=${TS_IP} /g" /var/lib/kubelet/kubeadm-flags.env 2>/dev/null || true
    fi
    systemctl restart kubelet
    echo "[join] ✓ kubeadm join succeeded"

    # 3. Tailscale carries NODE traffic only (ADR-046 addendum: Tailscale is a
    #    node-level underlay). Cilium runs routing-mode=tunnel, so cross-node pod
    #    traffic is VXLAN-encapsulated between tailnet IPs and pod IPs never appear
    #    on the wire. Advertising/accepting podCIDR subnet routes is therefore
    #    unnecessary AND harmful: tailscaled installs them into table 52, whose
    #    ip rule (5270) precedes main (32766), so they shadow Cilium's tunnel route
    #    for host-originated traffic to remote pods. Deliberately NOT advertised.
    echo "[join] ✓ Tailscale left as node-only underlay (no podCIDR advertisement)"

    exit 0
  fi
  echo "[join] Join attempt $i failed, retrying in 3s..."
  sleep 3
done
exit 1
SH_EOF
  sed -i '' "s|__ENDPOINT__|${CONTROL_PLANE_ENDPOINT}|g; s|__TOKEN__|${JOIN_TOKEN}|g; s|__HASH__|${CA_CERT_HASH}|g; s|__VM_NAME__|${VM_NAME}|g" "${ISO_ROOT}/bin/join-cluster.sh" 2>/dev/null || sed -i "s|__ENDPOINT__|${CONTROL_PLANE_ENDPOINT}|g; s|__TOKEN__|${JOIN_TOKEN}|g; s|__HASH__|${CA_CERT_HASH}|g; s|__VM_NAME__|${VM_NAME}|g" "${ISO_ROOT}/bin/join-cluster.sh"
  chmod +x "${ISO_ROOT}/bin/join-cluster.sh"

  # Cilium bootstrap-DNAT cleanup (ADR-046 §13). join-cluster.sh inserts
  # iptables rules (10.96.0.1:443 / <tailnet-ip>:6443 -> CP endpoint) so
  # kubelet/Cilium can reach the API server BEFORE Cilium's BPF kube-proxy
  # replacement is loaded. Once Cilium is Ready, those rules shadow the BPF
  # datapath (and go stale if the CP endpoint rotates) — this oneshot removes
  # them after the cilium-agent container is Running.
  cat <<'SH_EOF' > "${ISO_ROOT}/bin/cilium-bootstrap-cleanup.sh"
#!/bin/sh
export PATH="/opt/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH"

for i in $(seq 1 90); do
  if crictl ps --name cilium-agent 2>/dev/null | grep -q "Running"; then
    break
  fi
  sleep 2
done

sleep 10

[ -f /etc/kubernetes/kubelet.conf ] || exit 0
EP=$(awk '/server:/{print $2}' /etc/kubernetes/kubelet.conf 2>/dev/null | sed 's#https://##')
CP_HOST=${EP%%:*}; CP_PORT=${EP##*:}
TS_IP=$(/opt/bin/tailscale ip -4 2>/dev/null || true)
[ -n "$CP_HOST" ] && [ -n "$CP_PORT" ] || exit 0

# setup-node.sh bootstrap rules
iptables -t nat -D OUTPUT -d 10.96.0.1 -p tcp --dport 443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
iptables -t nat -D PREROUTING -d 10.96.0.1 -p tcp --dport 443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
iptables -t nat -D OUTPUT -d 127.0.0.1 -p tcp --dport 6443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
iptables -t nat -D POSTROUTING -d "${CP_HOST}" -j MASQUERADE 2>/dev/null || true
# join-cluster.sh bootstrap rules (node tailnet IP variants)
if [ -n "$TS_IP" ]; then
  iptables -t nat -D OUTPUT -d "${TS_IP}" -p tcp --dport 6443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
  iptables -t nat -D PREROUTING -d "${TS_IP}" -p tcp --dport 6443 -j DNAT --to-destination "${CP_HOST}:${CP_PORT}" 2>/dev/null || true
fi
echo "[cleanup] ✓ Removed bootstrap DNAT rules (Cilium BPF kube-proxy replacement active)"
exit 0
SH_EOF
  chmod +x "${ISO_ROOT}/bin/cilium-bootstrap-cleanup.sh"

  # Dynamic kubelet --node-ip re-assertion (ADR-046 §13). The kubelet.service
  # ExecStartPre runs this before EVERY kubelet start so the Node/CiliumNode
  # advertise the tailnet IPv4 (never the home-LAN 172.30.0.x), dynamically —
  # never hardcoded. Survives reboots and kubeadm upgrades. Idempotent.
  cat <<'SH_EOF' > "${ISO_ROOT}/bin/dynamic-node-ip.sh"
#!/bin/sh
TS_BIN="${TS_BIN:-$(command -v tailscale 2>/dev/null || echo /opt/bin/tailscale)}"
FLAGS=/var/lib/kubelet/kubeadm-flags.env
[ -f "$FLAGS" ] || exit 0
[ -x "$TS_BIN" ] || exit 0
TS_IP="$($TS_BIN ip -4 2>/dev/null)" || exit 0
[ -n "$TS_IP" ] || exit 0
sed -i "s/ --node-ip=[0-9.]*//g" "$FLAGS"
sed -i "s/KUBELET_KUBEADM_ARGS=\"/KUBELET_KUBEADM_ARGS=\"--node-ip=${TS_IP} /g" "$FLAGS"
exit 0
SH_EOF
  chmod +x "${ISO_ROOT}/bin/dynamic-node-ip.sh"

  local IGN_FILE="${ISO_ROOT}/config.ign"
  cat <<EOF > "$IGN_FILE"
{
  "ignition": {
    "version": "3.4.0"
  },
  "storage": {
    "files": [
      {
        "path": "/etc/hostname",
        "mode": 420,
        "overwrite": true,
        "contents": {
          "source": "data:text/plain;charset=utf-8;base64,${HOSTNAME_B64}"
        }
      },
      {
        "path": "/etc/sysctl.d/99-kubernetes.conf",
        "mode": 420,
        "overwrite": true,
        "contents": {
          "source": "data:text/plain;charset=utf-8;base64,${SYSCTL_B64}"
        }
      },
      {
        "path": "/etc/modules-load.d/kubernetes.conf",
        "mode": 420,
        "overwrite": true,
        "contents": {
          "source": "data:text/plain;charset=utf-8;base64,${MODULES_B64}"
        }
      },
      {
        "path": "/etc/systemd/network/10-static.network",
        "mode": 420,
        "overwrite": true,
        "contents": {
          "source": "data:text/plain;charset=utf-8;base64,${NETWORK_B64}"
        }
      },
      {
        "path": "/var/lib/tailscale/authkey",
        "mode": 384,
        "overwrite": true,
        "contents": {
          "source": "data:text/plain;charset=utf-8;base64,${TS_AUTHKEY_B64}"
        }
      }
    ]
  },
  "passwd": {
    "users": [
      {
        "name": "core",
        "sshAuthorizedKeys": [
          ${SSH_PUBKEYS_JSON}
        ]
      }
    ]
  },
  "systemd": {
    "units": [
      {
        "name": "containerd.service",
        "enabled": true
      },
      {
        "name": "k8s-install.service",
        "enabled": true,
        "contents": "[Unit]\nDescription=Install Kubernetes & Tailscale Binaries from Local DVD\nAfter=local-fs.target\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=/bin/sh -c 'mkdir -p /media/iso; mount -t iso9660 /dev/disk/by-label/config-2 /media/iso 2>/dev/null || mount -t iso9660 /dev/disk/by-label/CONFIG-2 /media/iso 2>/dev/null || mount -t iso9660 /dev/sr0 /media/iso 2>/dev/null || true; [ -f /media/iso/bin/setup-node.sh ] && /bin/sh /media/iso/bin/setup-node.sh'\n\n[Install]\nWantedBy=multi-user.target\n"
      },
      {
        "name": "tailscaled.service",
        "enabled": true,
        "contents": "[Unit]\nDescription=Tailscale Node Agent\nAfter=k8s-install.service\nWants=k8s-install.service\n\n[Service]\nExecStartPre=/usr/bin/mkdir -p /var/lib/tailscale /opt/bin\nExecStart=/opt/bin/tailscaled --state=/var/lib/tailscale/tailscaled.state --socket=/run/tailscale/tailscaled.sock\nExecStartPost=/bin/sh -c 'sleep 2; /opt/bin/tailscale up --authkey=${TS_AUTHKEY} --hostname=${VM_NAME}'\nRestart=always\nRestartSec=5\n\n[Install]\nWantedBy=multi-user.target\n"
      },
      {
        "name": "kubelet.service",
        "enabled": true,
        "contents": "[Unit]\nDescription=kubelet: The Kubernetes Node Agent\nDocumentation=https://kubernetes.io/docs/\nWants=containerd.service tailscaled.service\nAfter=containerd.service tailscaled.service\nConditionPathExists=/var/lib/kubelet/config.yaml\n\n[Service]\nEnvironment=\"KUBELET_EXTRA_ARGS=--node-labels=${NODE_LABELS} --provider-id=unmanaged://${VM_NAME}\"\nEnvironmentFile=-/var/lib/kubelet/kubeadm-flags.env\nExecStartPre=/opt/bin/dynamic-node-ip.sh\nExecStart=/opt/bin/kubelet --config=/var/lib/kubelet/config.yaml --bootstrap-kubeconfig=/etc/kubernetes/bootstrap-kubelet.conf --kubeconfig=/etc/kubernetes/kubelet.conf \$KUBELET_EXTRA_ARGS \$KUBELET_KUBEADM_ARGS\nRestart=always\nStartLimitInterval=0\nRestartSec=10\n\n[Install]\nWantedBy=multi-user.target\n"
      },
      {
        "name": "kubeadm-join.service",
        "enabled": true,
        "contents": "[Unit]\nDescription=Join Kubernetes Cluster via Kubeadm\nAfter=tailscaled.service containerd.service\nWants=tailscaled.service containerd.service\nConditionPathExists=!/etc/kubernetes/kubelet.conf\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=/opt/bin/join-cluster.sh\n\n[Install]\nWantedBy=multi-user.target\n"
      },
      {
        "name": "cilium-bootstrap-cleanup.service",
        "enabled": true,
        "contents": "[Unit]\nDescription=Remove Cilium bootstrap DNAT rules after Cilium is Ready\nAfter=kubeadm-join.service tailscaled.service\nWants=kubeadm-join.service\nConditionPathExists=/etc/kubernetes/kubelet.conf\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=/opt/bin/cilium-bootstrap-cleanup.sh\n\n[Install]\nWantedBy=multi-user.target\n"
      }
    ]
  }
}
EOF

  # 5. Populate ISO bundle (supports both Ignition & official Flatcar config-2 config-drive)
  mkdir -p "${ISO_ROOT}/openstack/latest"
  cat <<EOF > "${ISO_ROOT}/openstack/latest/meta_data.json"
{
  "hostname": "${VM_NAME}",
  "name": "${VM_NAME}",
  "public_keys": {
    "core": "$(head -n 1 "$HOME/.ssh/"*.pub | tr -d '\r\n')"
  }
}
EOF

  cat <<EOF > "${ISO_ROOT}/openstack/latest/user_data"
#cloud-config

ssh_authorized_keys:
$(awk '{print "  - " $0}' "$HOME/.ssh/"*.pub)

write_files:
  - path: /etc/sysctl.d/99-kubernetes.conf
    permissions: '0644'
    content: |
      net.ipv4.ip_forward = 1
      net.bridge.bridge-nf-call-iptables = 1
      net.bridge.bridge-nf-call-ip6tables = 1
      fs.inotify.max_user_watches = 524288
      fs.inotify.max_user_instances = 8192

  - path: /etc/modules-load.d/kubernetes.conf
    permissions: '0644'
    content: |
      overlay
      br_netfilter

  - path: /etc/systemd/network/10-static.network
    permissions: '0644'
    content: |
      [Match]
      Name=eth*
      Name=!cilium_* !lxc* !tailscale*

      [Network]
      DHCP=no
      Address=172.30.0.$((10 + NODE_IDX))/24
      Gateway=172.30.0.1
      DNS=8.8.8.8
      DNS=1.1.1.1

  - path: /var/lib/tailscale/authkey
    permissions: '0600'
    content: |
      ${TS_AUTHKEY}

coreos:
  units:
    - name: systemd-networkd.service
      command: restart

    - name: containerd.service
      command: start

    - name: k8s-install.service
      command: start
      content: |
        [Unit]
        Description=Install Kubernetes & Tailscale Binaries from Local DVD
        After=local-fs.target

        [Service]
        Type=oneshot
        RemainAfterExit=yes
        ExecStart=/bin/sh -c 'mkdir -p /media/iso; mount -t iso9660 /dev/disk/by-label/config-2 /media/iso 2>/dev/null || mount -t iso9660 /dev/disk/by-label/CONFIG-2 /media/iso 2>/dev/null || mount -t iso9660 /dev/sr0 /media/iso 2>/dev/null || true; [ -f /media/iso/bin/setup-node.sh ] && /bin/sh /media/iso/bin/setup-node.sh'

        [Install]
        WantedBy=multi-user.target

    - name: tailscaled.service
      command: start
      content: |
        [Unit]
        Description=Tailscale Node Agent
        After=k8s-install.service
        Wants=k8s-install.service

        [Service]
        ExecStartPre=/usr/bin/mkdir -p /var/lib/tailscale /opt/bin
        ExecStart=/opt/bin/tailscaled --state=/var/lib/tailscale/tailscaled.state --socket=/run/tailscale/tailscaled.sock
        ExecStartPost=/bin/sh -c 'sleep 2; /opt/bin/tailscale up --authkey=${TS_AUTHKEY} --hostname=${VM_NAME}'
        Restart=always
        RestartSec=5

        [Install]
        WantedBy=multi-user.target

    - name: kubelet.service
      command: start
      content: |
        [Unit]
        Description=kubelet: The Kubernetes Node Agent
        Documentation=https://kubernetes.io/docs/
        Wants=containerd.service tailscaled.service
        After=containerd.service tailscaled.service
        ConditionPathExists=/var/lib/kubelet/config.yaml

        [Service]
        Environment="KUBELET_EXTRA_ARGS=--node-labels=${NODE_LABELS} --provider-id=unmanaged://${VM_NAME}"
        EnvironmentFile=-/var/lib/kubelet/kubeadm-flags.env
        ExecStartPre=/opt/bin/dynamic-node-ip.sh
        ExecStart=/opt/bin/kubelet --config=/var/lib/kubelet/config.yaml --bootstrap-kubeconfig=/etc/kubernetes/bootstrap-kubelet.conf --kubeconfig=/etc/kubernetes/kubelet.conf \$KUBELET_EXTRA_ARGS \$KUBELET_KUBEADM_ARGS
        Restart=always
        StartLimitInterval=0
        RestartSec=10

        [Install]
        WantedBy=multi-user.target

    - name: kubeadm-join.service
      command: start
      content: |
        [Unit]
        Description=Join Kubernetes Cluster via Kubeadm
        After=tailscaled.service containerd.service
        Wants=tailscaled.service containerd.service
        ConditionPathExists=!/etc/kubernetes/kubelet.conf

        [Service]
        Type=oneshot
        RemainAfterExit=yes
        ExecStart=/opt/bin/join-cluster.sh

        [Install]
        WantedBy=multi-user.target

    - name: cilium-bootstrap-cleanup.service
      command: start
      content: |
        [Unit]
        Description=Remove Cilium bootstrap DNAT rules after Cilium is Ready
        After=kubeadm-join.service tailscaled.service
        Wants=kubeadm-join.service
        ConditionPathExists=/etc/kubernetes/kubelet.conf

        [Service]
        Type=oneshot
        RemainAfterExit=yes
        ExecStart=/opt/bin/cilium-bootstrap-cleanup.sh

        [Install]
        WantedBy=multi-user.target

    - name: iso-cleanup.service
      command: start
      content: |
        [Unit]
        Description=Unmount Ignition ISO to prevent false DiskPressure condition
        After=local-fs.target
        ConditionPathIsMountPoint=/media/iso

        [Service]
        Type=oneshot
        RemainAfterExit=yes
        ExecStart=/bin/umount -f /media/iso

        [Install]
        WantedBy=multi-user.target
EOF

  cp "$IGN_FILE" "${ISO_ROOT}/ignition/config.ign"
  cp "${CACHE_DIR}/"* "${ISO_ROOT}/bin/"

  echo "    → Packaging standalone config-2 & binary ISO bundle..."
  local ISO_OUT="${TMP_DIR}/config-drive.iso"
  hdiutil makehybrid -iso -joliet -default-volume-name "config-2" -o "$ISO_OUT" "$ISO_ROOT" >/dev/null

  # 6. SCP Ignition config & ISO bundle to Windows host
  echo "    → Uploading Ignition config and binary ISO bundle to host..."
  local REMOTE_IGN="C:/ProgramData/soloz/flatcar/${VM_NAME}-config.ign"
  local REMOTE_ISO="C:/ProgramData/soloz/flatcar/${VM_NAME}-ignition.iso"
  scp -o BatchMode=yes -o StrictHostKeyChecking=no "$IGN_FILE" "${SSH_TARGET}:${REMOTE_IGN}" >/dev/null
  scp -o BatchMode=yes -o StrictHostKeyChecking=no "$ISO_OUT" "${SSH_TARGET}:${REMOTE_ISO}" >/dev/null
  echo "    ✓ Ignition config & ISO bundle uploaded"
}

# ── Phase 4: Provision Generation 2 Hyper-V VM ───────────────────────────────
phase_provision_flatcar_vm() {
  local SSH_TARGET="$1" VM_NAME="$2" NODE_IDX="$3" CLUSTER_TARGET="${4:-hub}"
  echo "    [4/6] Provisioning Flatcar Gen2 VM via Hyper-V KVP + DVD (${VM_NAME} → ${CLUSTER_TARGET})..."

  local PS_VM="
\$vmName = '${VM_NAME}';
\$solozDir = 'C:\ProgramData\soloz\flatcar';
\$baseVhdx = Join-Path \$solozDir 'flatcar-base.vhdx';
\$rawVhdx = Join-Path \$solozDir 'flatcar_production_hyperv_vhdx_image.vhdx';
if (!(Test-Path \$baseVhdx) -and (Test-Path \$rawVhdx)) {
  Move-Item -Path \$rawVhdx -Destination \$baseVhdx -Force
}
\$vhdPath = Join-Path \$solozDir '${VM_NAME}.vhdx';
\$ignPath = Join-Path \$solozDir '${VM_NAME}-config.ign';
\$isoPath = Join-Path \$solozDir '${VM_NAME}-ignition.iso';
\$kvpctl = Join-Path \$solozDir 'kvpctl.exe';

\$existingVM = Get-VM -Name \$vmName -ErrorAction SilentlyContinue
if (\$existingVM) {
  Stop-VM -Name \$vmName -Force -TurnOff -ErrorAction SilentlyContinue
  Remove-VM -Name \$vmName -Force -ErrorAction SilentlyContinue
  Start-Sleep -Seconds 2
}

# Clean any stale checkpoint avhdx files
Get-ChildItem \$solozDir -Filter '${VM_NAME}*.avhdx' | Remove-Item -Force -ErrorAction SilentlyContinue

Write-Output '    → Creating fresh VM disk from pristine base Flatcar VHDX...'
Copy-Item -Path \$baseVhdx -Destination \$vhdPath -Force
Resize-VHD -Path \$vhdPath -SizeBytes $NODE_DISK_BYTES

Write-Output '    → Creating Generation 2 VM (\$vmName)...'
\$cs = Get-CimInstance Win32_ComputerSystem
\$totalRamBytes = [int64]\$cs.TotalPhysicalMemory
\$reserveBytes = [int64](5GB)

# Co-located Dual-VM memory layout (16 GB Dell):
# Windows + Hyper-V ~5 GB | Hub VM 6 GB | Spoke VM 4 GB | Safety margin ~1 GB
if ('$CLUSTER_TARGET' -eq 'spoke') {
  \$autoMaxRam = [int64](4GB)
  \$autoStartup = [int64](3GB)
  \$autoMin = [int64](2GB)
} else {
  \$autoMaxRam = [int64](6GB)
  \$autoStartup = [int64](3.5GB)
  \$autoMin = [int64](2GB)
}

\$proc = Get-CimInstance Win32_Processor
# Divide the host's threads between the VMs that will share it, rather than handing
# every VM the full count. The old behaviour gave each VM NumberOfLogicalProcessors,
# so two nodes on a 4-thread part ran 8 vCPUs — 2:1 oversubscription, and the direct
# cause of the thermal trips in ADR-046 §24.5. Counts VMs already defined plus this
# one, so it self-adjusts as a box gains or loses nodes. Floor of 2: kubelet plus a
# CNI on a single vCPU is not viable.
\$logical = [int]\$proc.NumberOfLogicalProcessors
\$peerVms = @(Get-VM -ErrorAction SilentlyContinue | Where-Object { \$_.Name -ne \$vmName }).Count
\$share   = [math]::Max(1, \$peerVms + 1)
\$autoCpus = [math]::Max(2, [math]::Floor(\$logical / \$share))

\$finalMaxRam = \$autoMaxRam
if ($MAX_MEMORY_BYTES -gt 0) { \$finalMaxRam = [int64]$MAX_MEMORY_BYTES }
\$finalStartup = \$autoStartup
if ($MEMORY_BYTES -gt 0) { \$finalStartup = [int64]$MEMORY_BYTES }
\$finalMin = \$autoMin
if ($MIN_MEMORY_BYTES -gt 0) { \$finalMin = [int64]$MIN_MEMORY_BYTES }
\$finalCpus = \$autoCpus
if ($CPU_COUNT -gt 0) { \$finalCpus = [int]$CPU_COUNT }

# Oversubscription guard (ADR-046 §24.5). home-lab.env is gitignored, so a stale
# cpus column survives every repo change and silently reinstates the condition that
# powered this class of host off mid-provision. An explicit value still wins — the
# operator may know better — but it does not get to be silent about it.
\$otherCpus = 0
Get-VM -ErrorAction SilentlyContinue | Where-Object { \$_.Name -ne \$vmName } | ForEach-Object { \$otherCpus += \$_.ProcessorCount }
\$totalCpus = \$otherCpus + \$finalCpus
if (\$totalCpus -gt \$logical) {
  Write-Output ('    ⚠ vCPU oversubscription: ' + \$totalCpus + ' vCPUs across ' + (\$peerVms + 1) + ' VM(s) on ' + \$logical + ' host threads')
  Write-Output ('    → this ratio drove ACPI critical-thermal shutdowns on a 15W host; see ADR-046 §24.5')
  Write-Output ('    → lower the cpus column in home-lab.env for the VMs on this box')
}

Write-Output ('    ✓ Capacity: ' + \$finalCpus + ' vCPUs, ' + [math]::Round(\$finalStartup/1GB, 1) + ' GB Startup (Dynamic ' + [math]::Round(\$finalMin/1GB, 1) + ' - ' + [math]::Round(\$finalMaxRam/1GB, 1) + ' GB Max, 5 GB Host OS Reserve)')

New-VM -Name \$vmName -Generation 2 -MemoryStartupBytes \$finalStartup -VHDPath \$vhdPath -SwitchName '$VSWITCH_NAME' | Out-Null
Add-VMDvdDrive -VMName \$vmName -Path \$isoPath | Out-Null
Set-VMFirmware -VMName \$vmName -EnableSecureBoot Off
\$hdd = Get-VMHardDiskDrive -VMName \$vmName
\$dvd = Get-VMDvdDrive -VMName \$vmName
Set-VMFirmware -VMName \$vmName -BootOrder \$hdd, \$dvd
Set-VM -VMName \$vmName -AutomaticCheckpointsEnabled:\$false
Set-VMMemory -VMName \$vmName -DynamicMemoryEnabled \$true -StartupBytes \$finalStartup -MinimumBytes \$finalMin -MaximumBytes \$finalMaxRam
Set-VMProcessor -VMName \$vmName -Count \$finalCpus
Set-VM -VMName \$vmName -AutomaticStartAction Start

Write-Output '    → Injecting Ignition config via kvpctl...'
if (Test-Path \$kvpctl) {
  & \$kvpctl \$vmName add-ign \$ignPath
}

Start-VM -Name \$vmName
Write-Output '    ✓ Started VM'

Write-Output '    → Setting up host portproxy for SSH...'
\$vmIp = \$null
for (\$i = 0; \$i -lt 15; \$i++) {
  Start-Sleep -Seconds 1
  \$adapter = Get-VMNetworkAdapter -VMName \$vmName -ErrorAction SilentlyContinue
  \$ip = \$adapter.IPAddresses | Where-Object { \$_ -match '^\d+\.\d+\.\d+\.\d+' } | Select-Object -First 1
  if (\$ip) { \$vmIp = \$ip; break }
}
if (!\$vmIp) { \$vmIp = '172.30.0.$((10 + NODE_IDX))' }
\$listenPort = $((2220 + NODE_IDX))
netsh interface portproxy delete v4tov4 listenport=\$listenPort listenaddress=0.0.0.0 | Out-Null
netsh interface portproxy add v4tov4 listenport=\$listenPort listenaddress=0.0.0.0 connectaddress=\$vmIp connectport=22 | Out-Null
Write-Output ('    ✓ Portproxy: host:' + \$listenPort + ' -> ' + \$vmIp + ':22')
Write-Output 'VM-READY=OK'
"
  win_ps_stream "$SSH_TARGET" "$PS_VM"
}

# ── Phase 6: Verify node Ready ───────────────────────────────────────────────
node_ready() {
  local HOSTNAME="$1"
  local CLUSTER_TARGET="${2:-$TARGET_CLUSTER}"
  local TARGET_KC
  local TMP_KC=""

  if [[ "$CLUSTER_TARGET" == "hub" ]]; then
    TARGET_KC="${HUB_KUBECONFIG}"
  else
    TMP_KC=$(mktemp /tmp/hybrid-spoke-XXXXXX)
    if ! kubectl --kubeconfig="${HUB_KUBECONFIG}" \
          get secret "${HYBRID_SPOKE_NAME}-kubeconfig" \
          -n platform-capi -o jsonpath='{.data.value}' 2>/dev/null \
          | base64 -d > "$TMP_KC"; then
      echo "    ✗ could not fetch spoke kubeconfig" >&2
      rm -f "$TMP_KC"
      return 1
    fi
    TARGET_KC="$TMP_KC"
  fi

  if kubectl --kubeconfig="$TARGET_KC" get node "${HOSTNAME}" &>/dev/null; then
    local READY
    READY=$(kubectl --kubeconfig="$TARGET_KC" get node "${HOSTNAME}" \
      -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
    [[ -n "$TMP_KC" ]] && rm -f "$TMP_KC"
    if [[ "$READY" == "True" ]]; then
      echo "    ✓ ${HOSTNAME}: Ready in ${CLUSTER_TARGET} cluster"
      return 0
    else
      echo "    ✗ ${HOSTNAME}: exists in ${CLUSTER_TARGET} but Ready=${READY}" >&2
      return 1
    fi
  else
    [[ -n "$TMP_KC" ]] && rm -f "$TMP_KC"
    echo "    ✗ ${HOSTNAME}: not a member of ${CLUSTER_TARGET} cluster" >&2
    return 1
  fi
}

# ── Phase 5 & 6: Monitor guest boot & verify node Ready ─────────────────────
phase_monitor_and_verify() {
  local SSH_TARGET="$1" HOSTNAME="$2" NODE_IDX="$3" CLUSTER_TARGET="${4:-$TARGET_CLUSTER}"
  echo "    [5/6] Flatcar VM booted — monitoring guest startup & join progress..."
  echo "    [6/6] Waiting for node Ready in ${CLUSTER_TARGET} cluster (up to 5 min)..."
  local HOST_IP="${SSH_TARGET#*@}"
  local GUEST_PORT=$((2220 + NODE_IDX))
  for attempt in $(seq 1 30); do
    local ELAPSED=$((attempt * 10))
    local VM_STATUS SSH_STATUS INST_STATUS TS_STATUS CRI_STATUS JOIN_STATUS KUBE_STATUS LAST_LOG

    # Query VM status from Hyper-V
    VM_STATUS=$(ssh -o BatchMode=yes -o ConnectTimeout=3 -o StrictHostKeyChecking=no "$SSH_TARGET" \
      "powershell -Command '(Get-VM -Name ${HOSTNAME} -ErrorAction SilentlyContinue).State'" 2>/dev/null | tr -d '\r\n' || echo "Unknown")
    [[ -z "$VM_STATUS" ]] && VM_STATUS="Unknown"

    # Query Guest SSH & service status
    local GUEST_SSH_OPT=(-o BatchMode=yes -o ConnectTimeout=2 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -p "$GUEST_PORT")
    if ssh "${GUEST_SSH_OPT[@]}" core@"${HOST_IP}" "echo ok" &>/dev/null; then
      SSH_STATUS="Connected"
      local RAW_STATUS
      RAW_STATUS=$(ssh "${GUEST_SSH_OPT[@]}" core@"${HOST_IP}" \
        'for u in k8s-install tailscaled containerd kubeadm-join kubelet; do printf "%s " "$(systemctl is-active $u.service 2>/dev/null || echo unknown)"; done' 2>/dev/null || echo "unknown unknown unknown unknown unknown")

      read -r INST_STATUS TS_STATUS CRI_STATUS JOIN_STATUS KUBE_STATUS <<< "$RAW_STATUS"
      [[ -z "$INST_STATUS" ]] && INST_STATUS="unknown"
      [[ -z "$TS_STATUS" ]] && TS_STATUS="unknown"
      [[ -z "$CRI_STATUS" ]] && CRI_STATUS="unknown"
      [[ -z "$JOIN_STATUS" ]] && JOIN_STATUS="unknown"
      [[ -z "$KUBE_STATUS" ]] && KUBE_STATUS="unknown"

      CRI_PODS=$(ssh "${GUEST_SSH_OPT[@]}" core@"${HOST_IP}" \
        "sudo crictl pods -o json 2>/dev/null | jq -r '[.items[]? | \"\(.metadata.name) (\(.state))\"] | join(\", \")' 2>/dev/null" || true)

      LAST_LOG=$(ssh "${GUEST_SSH_OPT[@]}" core@"${HOST_IP}" \
        "journalctl -u k8s-install.service -u tailscaled.service -u containerd.service -u kubeadm-join.service -u kubelet.service -n 1 --no-pager -q 2>/dev/null | tr -d '\r\n'" 2>/dev/null || true)
    else
      SSH_STATUS="Booting"
      INST_STATUS="pending"
      TS_STATUS="pending"
      CRI_STATUS="pending"
      JOIN_STATUS="pending"
      KUBE_STATUS="pending"
      CRI_PODS=""
      LAST_LOG=""
    fi

    # Check cluster membership
    if node_ready "$HOSTNAME" "$CLUSTER_TARGET" 2>/dev/null | grep -q "Ready"; then
      echo "      [${attempt}/30] (${ELAPSED}s) VM: ${VM_STATUS} | SSH: ${SSH_STATUS} | Install: ${INST_STATUS} | Tailscale: ${TS_STATUS} | CRI: ${CRI_STATUS} | Join: ${JOIN_STATUS} | Kubelet: ${KUBE_STATUS} | ${CLUSTER_TARGET}: Ready ✓"
      echo "    ✓ ${HOSTNAME}: Ready in ${CLUSTER_TARGET} cluster"
      # Apply node-role labels from cluster side (idempotent with kubelet --node-labels)
      if [[ "$CLUSTER_TARGET" == "hub" ]]; then
        kubectl --kubeconfig="${HUB_KUBECONFIG}" label node "${HOSTNAME}" \
          node-role.kubernetes.io/home= node-role.kubernetes.io/worker= hub-role=worker workload-location=home --overwrite >/dev/null 2>&1 || true
      else
        local SPOKE_KC
        SPOKE_KC=$(mktemp /tmp/hybrid-spoke-XXXXXX)
        if kubectl --kubeconfig="${HUB_KUBECONFIG}" \
              get secret "${HYBRID_SPOKE_NAME}-kubeconfig" \
              -n platform-capi -o jsonpath='{.data.value}' 2>/dev/null \
              | base64 -d > "$SPOKE_KC"; then
          kubectl --kubeconfig="$SPOKE_KC" label node "${HOSTNAME}" \
            node-role.kubernetes.io/home= node-role.kubernetes.io/worker= workload-location=home --overwrite >/dev/null 2>&1 || true
          rm -f "$SPOKE_KC"
        fi
      fi
      return 0
    else
      echo "      [${attempt}/30] (${ELAPSED}s) VM: ${VM_STATUS} | SSH: ${SSH_STATUS} | Install: ${INST_STATUS} | Tailscale: ${TS_STATUS} | CRI: ${CRI_STATUS} | Join: ${JOIN_STATUS} | Kubelet: ${KUBE_STATUS} | ${CLUSTER_TARGET}: Joining..."
      if [[ -n "$CRI_PODS" ]]; then
        echo "        ↳ [CRI Pods]: ${CRI_PODS}"
      fi
      if [[ -n "$LAST_LOG" ]]; then
        echo "        ↳ [Guest Log]: ${LAST_LOG}"
      fi
    fi
    sleep 10
  done

  echo "    ✗ ${HOSTNAME} did not become Ready within 5 min." >&2
  echo "    → Diagnostic: Dumping guest journal logs (port ${GUEST_PORT})..." >&2
  ssh -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -p "$GUEST_PORT" core@"${HOST_IP}" \
    "journalctl -u containerd.service -u k8s-install.service -u tailscaled.service -u kubeadm-join.service -u kubelet.service --no-pager -n 120" 2>&1 | sed 's/^/        /' || true
  return 1
}

# ── Main ─────────────────────────────────────────────────────────────────────

if [[ "$MODE" == "verify" ]]; then
  echo "=== Verify mode — checking Flatcar node Ready status ==="
  local_ok=1
  idx=0
  while IFS='|' read -r _HOST _SSH _WSL _TAILNET _TAG NODE_TARGET _STARTUP_GB _MIN_GB _MAX_GB _CPUS; do
    [[ -z "$_HOST" && -z "$_SSH" ]] && continue
    idx=$((idx + 1))
    CURR_TARGET="hub"
    if [[ "$idx" -gt 1 ]]; then CURR_TARGET="spoke"; fi
    if [[ -n "${NODE_TARGET:-}" ]]; then CURR_TARGET="$NODE_TARGET"; fi
    if [[ "$TARGET_CLUSTER" == "hub" || "$TARGET_CLUSTER" == "spoke" ]]; then CURR_TARGET="$TARGET_CLUSTER"; fi
    VM_NAME="${_HOST}"
    if [[ -z "$VM_NAME" ]]; then
      if [[ "$CURR_TARGET" == "hub" ]]; then
        VM_NAME="flatcar-hub-node-${idx}"
      else
        VM_NAME="flatcar-spoke-node-${idx}"
      fi
    fi
    if node_ready "$VM_NAME" "$CURR_TARGET"; then :; else local_ok=0; fi
  done <<< "$HOME_WORKER_NODES"
  [[ "$local_ok" == "1" ]] && echo "=== ALL REGISTERED NODES READY ===" || echo "=== SOME NODES NOT READY ==="
  exit $((1 - local_ok))
fi

NODE_IDX=0
FAILED_NODES=()

echo "=== Hybrid Hyper-V + Flatcar Container Linux worker provisioner ==="
echo "    Target Mode:    ${TARGET_CLUSTER} (Hub + Spoke auto-routing)"
echo "    Spoke:          ${HYBRID_SPOKE_NAME}"
echo "    Tailnet:        ${TAILNET_NAME}"
echo "    Hub kc:         ${HUB_KUBECONFIG}"
echo "    K8s ver:        ${K8S_VERSION}"
echo "    Tailscale ver:  ${TAILSCALE_VERSION}"
echo "    Flatcar ch/ver: ${FLATCAR_RELEASE_CHANNEL} (stable 4593.2.4)"
echo "    Hyper-V Switch: ${VSWITCH_NAME}"
echo ""

phase_prep_binaries

while IFS='|' read -r _HOST SSH_TARGET WSL_DISTRO _TAILNET BOX_TAG NODE_TARGET STARTUP_GB MIN_GB MAX_GB CPUS DISK_GB; do
  [[ -z "$SSH_TARGET" ]] && continue
  NODE_IDX=$((NODE_IDX + 1))
  [[ -n "$ONLY_NODE" && "$NODE_IDX" != "$ONLY_NODE" ]] && continue

  # Per-node capacity: registry fields are the DEFAULT; CLI flags override.
  MEMORY_BYTES="0"; MIN_MEMORY_BYTES="0"; MAX_MEMORY_BYTES="0"; CPU_COUNT="0"
  if [[ "${STARTUP_GB:-}" =~ ^[0-9]+$ ]]; then
    MEMORY_BYTES="$((${STARTUP_GB} * 1024 * 1024 * 1024))"
    [[ "${MIN_GB:-}" =~ ^[0-9]+$ ]] && MIN_MEMORY_BYTES="$((${MIN_GB} * 1024 * 1024 * 1024))"
    [[ "${MAX_GB:-}" =~ ^[0-9]+$ ]] && MAX_MEMORY_BYTES="$((${MAX_GB} * 1024 * 1024 * 1024))"
    [[ "${CPUS:-}" =~ ^[0-9]+$ ]] && CPU_COUNT="${CPUS}"
  fi
  [[ "$CLI_MEMORY_BYTES" -gt 0 ]] && MEMORY_BYTES="$CLI_MEMORY_BYTES"
  [[ "$CLI_MIN_MEMORY_BYTES" -gt 0 ]] && MIN_MEMORY_BYTES="$CLI_MIN_MEMORY_BYTES"
  [[ "$CLI_MAX_MEMORY_BYTES" -gt 0 ]] && MAX_MEMORY_BYTES="$CLI_MAX_MEMORY_BYTES"
  [[ "$CLI_CPU_COUNT" -gt 0 ]] && CPU_COUNT="$CLI_CPU_COUNT"

  # Disk ceiling, in precedence order: --disk-gb > registry field 11 > per-target
  # default. Resolved after CURR_TARGET is known (just below), so a hub node gets
  # the hub default without anyone passing a flag.

  # Where this node belongs. The registry's target field (column 6) is
  # authoritative; the index heuristic is only a fallback for older registries
  # that predate that column.
  CURR_TARGET="hub"
  if [[ "$NODE_IDX" -gt 1 ]]; then CURR_TARGET="spoke"; fi
  if [[ -n "${NODE_TARGET:-}" ]]; then CURR_TARGET="$NODE_TARGET"; fi

  # --cluster SELECTS which registered nodes to act on; it does not retarget them.
  #
  # It used to overwrite CURR_TARGET, so `--cluster hub` walked every entry in
  # home-lab.env and joined them all to the hub — flatcar-spoke-node-1 included,
  # built under its spoke name but wired into the hub. That silently contradicts
  # the registry, whose whole purpose is to say where each node belongs, and the
  # documented behaviour ("auto-routes per home-lab.env").
  if [[ "$TARGET_CLUSTER" == "hub" || "$TARGET_CLUSTER" == "spoke" ]]; then
    if [[ "$CURR_TARGET" != "$TARGET_CLUSTER" ]]; then
      echo "── node ${NODE_IDX}: ${_HOST:-node-${NODE_IDX}} → ${CURR_TARGET}, skipped (--cluster ${TARGET_CLUSTER})"
      continue
    fi
  fi

  DEFAULT_DISK_GB="$SPOKE_DISK_GB_DEFAULT"
  [[ "$CURR_TARGET" == "hub" ]] && DEFAULT_DISK_GB="$HUB_DISK_GB_DEFAULT"
  NODE_DISK_BYTES="$((DEFAULT_DISK_GB * 1024 * 1024 * 1024))"
  [[ "${DISK_GB:-}" =~ ^[0-9]+$ ]] && NODE_DISK_BYTES="$((DISK_GB * 1024 * 1024 * 1024))"
  [[ "$CLI_DISK_SET" == "1" ]] && NODE_DISK_BYTES="$DISK_SIZE_BYTES"

  HOSTNAME="${_HOST}"
  if [[ -z "$HOSTNAME" ]]; then
    if [[ "$CURR_TARGET" == "hub" ]]; then
      HOSTNAME="flatcar-hub-node-${NODE_IDX}"
    else
      HOSTNAME="flatcar-spoke-node-${NODE_IDX}"
    fi
  fi
  TAILNET_HOST="${HOSTNAME}.${TAILNET_NAME}"

  echo "── node ${NODE_IDX}: ${HOSTNAME} (${BOX_TAG} → ${CURR_TARGET}) ─────────────────"
  echo "    SSH: ${SSH_TARGET}  VM: ${HOSTNAME}  Tailnet: ${TAILNET_HOST}  Target: ${CURR_TARGET}"
  if [[ "$MEMORY_BYTES" -gt 0 || "$CPU_COUNT" -gt 0 ]]; then
    MCAP="$((MEMORY_BYTES / 1024 / 1024 / 1024))" MMIN="$((MIN_MEMORY_BYTES / 1024 / 1024 / 1024))" MMAX="$((MAX_MEMORY_BYTES / 1024 / 1024 / 1024))"
    echo "    Capacity: ${CPU_COUNT:-auto} vCPUs, ${MCAP:-auto} GB startup (${MMIN:-auto}-${MMAX:-auto} GB dynamic range)"
  fi

  # [1/6] SSH reachability gate
  echo "    [1/6] SSH reachability gate..."
  if ! ssh -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=no \
        "${SSH_TARGET}" "echo ok" 2>/dev/null; then
    echo "    ✗ SSH to ${SSH_TARGET} failed — Windows host unreachable. Skipping." >&2
    FAILED_NODES+=("${HOSTNAME}")
    continue
  fi
  echo "    ✓ SSH connected"

  phase_prep_hyperv "$SSH_TARGET" "$HOSTNAME"
  phase_prep_flatcar_and_ignition "$SSH_TARGET" "$HOSTNAME" "$NODE_IDX" "$CURR_TARGET"
  phase_provision_flatcar_vm "$SSH_TARGET" "$HOSTNAME" "$NODE_IDX" "$CURR_TARGET"

  if ! phase_monitor_and_verify "$SSH_TARGET" "$HOSTNAME" "$NODE_IDX" "$CURR_TARGET"; then
    FAILED_NODES+=("${HOSTNAME}")
  fi
  echo ""
done <<< "$HOME_WORKER_NODES"

echo ""
if [[ ${#FAILED_NODES[@]} -gt 0 ]]; then
  echo "=== PROVISION INCOMPLETE — failed nodes: ${FAILED_NODES[*]} ==="
  exit 1
fi
echo "=== ✓ ALL TARGETED FLATCAR NODES PROVISIONED AND READY ==="
