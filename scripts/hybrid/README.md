# Hybrid Home-Lab Operations & Runbook (ADR-046)

## The Single Command

Nodes are fully **self-healing** — they recover from network glitches, wedged
WSL2 VMs, and Tailscale drops **without any Mac intervention**. A Windows
watchdog (every 2 min) and a WSL2 watchdog (every 60s) restore the node.

Provisioning, recovery, and verification are all handled by **one script**:

```bash
# Provision / (re)provision a node — hardens Windows, installs watchdogs,
# provisions the WSL2 distro, installs reboot resilience, joins the spoke.
./scripts/hybrid/provision-home-worker.sh --node 1

# Provision all registered nodes
./scripts/hybrid/provision-home-worker.sh

# Repair an already-provisioned node (watchdogs + join, no re-provision)
./scripts/hybrid/provision-home-worker.sh --recover 1

# Autonomous Tailscale re-auth (embed a reusable auth-key in the node)
./scripts/hybrid/provision-home-worker.sh --node 1 --ts-authkey tskey-...

# Check Ready status of all registered nodes without changing anything
./scripts/hybrid/provision-home-worker.sh --verify
```

> **The run is only a success when the node reports `Ready`** in the spoke
> cluster. The orchestrator exits non-zero if any targeted node fails to
> become Ready.

### Phases run per node

| Phase | What it does |
|-------|--------------|
| [1/7] | SSH reachability gate |
| [2/7] | Harden Windows host (power/lid, disable updates, OpenSSH, `.wslconfig`) |
| [3/7] | Deploy self-healing watchdogs (Windows `HybridWSLWatchdog<N>` + WSL2 `tailscale-watchdog.timer`) |
| [4/7] | Provision WSL2 distro (`setup-wsl2-node.sh`: containerd, kubelet, tailscale, cgroup) |
| [5/7] | Install reboot resilience (Windows autostart + systemd `hybrid-home-worker-join.timer`) |
| [6/7] | Join spoke cluster (`home-worker-join.sh`, idempotent) |
| [7/7] | Verify node Ready — the success gate |

### How self-healing works (no Mac required)

1. **Windows watchdog** (`HybridWSLWatchdog<N>`, SYSTEM, every 2 min):
   rebuilds WinNAT if missing, detects a **wedged/unresponsive WSL2 VM** and
   performs a **full VM reset** (`wsl --shutdown` + HNS/SharedAccess restart +
   vEthernet rebuild + distro restart) with a cooldown to prevent thrashing.
2. **WSL2 watchdog** (`tailscale-watchdog.timer`, every 60s): restores
   tailscaled, containerd, kubelet, and clears stale Cilium pidfiles.
3. **Auto-join timer** (`hybrid-home-worker-join.timer`, every 10 min + on
   boot): re-runs `home-worker-join.sh` to restore cluster membership.

### One-time node setup (new box or fresh WSL install)

```bash
# 1. Inside WSL2 on the Windows box (as root):
sudo ./scripts/hybrid/setup-wsl2-node.sh 1

# 2. From the Mac — the single provisioning command:
./scripts/hybrid/provision-home-worker.sh --node 1 --ts-authkey <reusable-key>
```

---

## Deprecated Scripts

These are now thin wrappers (or no-ops) delegating to
`provision-home-worker.sh` — kept only for reference compatibility:

| Script | Replaced by |
|--------|-------------|
| `harden-windows-host.sh` | phase [2/7] + [3/7] of `provision-home-worker.sh` |
| `join-home-workers.sh` | phases [5/7] + [6/7] of `provision-home-worker.sh` |
| `recover-wsl2-nat.sh` | `provision-home-worker.sh --recover N` + the autonomous Windows watchdog |
| `prepare-wsl2-cgroup.sh` | `setup-wsl2-node.sh` (cilium-host-prep.service) + `tailscale-watchdog.sh` |
| `fix-cilium-pid.sh` | `tailscale-watchdog.sh` (heal_cilium_pid) |

## Hyper-V + Talos Linux Topology (Proposed / Next-Gen)

Replaces WSL2 convenience nodes with **Hyper-V Gen2 VMs running immutable Talos Linux**.
Eliminates WSL2 cgroup namespace isolation quirks and PID collisions, running native Linux cgroup v2 & eBPF.

```bash
# Provision / bootstrap a Talos worker node via Hyper-V + talosctl:
./scripts/hybrid/provision-talos-worker.sh --node 1 --ts-authkey <key>

# Verify Ready status of Talos worker nodes:
./scripts/hybrid/provision-talos-worker.sh --verify
```

### Files

| Script / Template | Purpose |
|--------|---------|
| `provision-talos-worker.sh` | **THE entry point for Hyper-V + Talos** — provisions Gen2 VM + Talos apply |
| `talos/talos-worker.template.yaml` | Talos worker node machine configuration template |
| `provision-home-worker.sh` | Entry point for legacy WSL2 nodes (self-healing watchdogs + kubeadm) |
| `setup-wsl2-node.sh` | WSL2 distro provisioning (containerd, kubelet, tailscale, cgroup) |
| `home-worker-join.sh` | Runs on WSL2 node — kubeadm join logic (consumed by auto-join timer) |
| `windows/wsl2-node-watchdog.ps1` | Windows watchdog (VM reset + WinNAT heal, every 2 min) |
| `wsl2/tailscale-watchdog.sh` (+service/timer) | WSL2 watchdog (tailscale/containerd/kubelet/cilium, every 60s) |
| `render-home-workers.sh` | Render the SpokePool `home-workers` JSON annotation |
| `home-lab.env` | Node registry — gitignored, real values only |
| `home-lab.env.example` | Template for `home-lab.env` |
