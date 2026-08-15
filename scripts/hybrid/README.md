# Hybrid Home-Lab Operations & Runbook (ADR-046)

## Quick Recovery (When a Node Goes `NotReady` or Drops)

If any worker node shows `NotReady` or disconnects, run this single command from your Mac:

```bash
# Recover specific node (Node 1 = Dell, Node 2 = Lenovo)
./scripts/hybrid/recover-wsl2-nat.sh --node 1
./scripts/hybrid/recover-wsl2-nat.sh --node 2

# Or recover ALL registered nodes at once:
./scripts/hybrid/recover-wsl2-nat.sh
```

### What this command does automatically:
1. **Rebuilds WinNAT / HNS**: Restarts Windows Host Network Service and ICS to fix any stale virtual adapter state.
2. **Re-establishes Tailscale Mesh**: Verifies connectivity and auto-restarts `tailscaled`.
3. **Restores Services Non-Destructively**: Ensures `containerd` and `kubelet` are healthy without wiping cluster state.
4. **Verifies Spoke Cluster `Ready`**: Checks and confirms `Ready: True` status in the spoke cluster.

---

## Daily Operational Commands

### 1. Check Live Cluster & Node Health
```bash
SPOKE_KC=$(kubectl --kubeconfig=k8-secrets/kubeconfig/hub-hybrid-dev.kubeconfig get secret spoke-pool-hybrid-dev-01-kubeconfig -n platform-capi -o jsonpath='{.data.value}' | base64 -d | cat)

# View all nodes
echo "$SPOKE_KC" | kubectl --kubeconfig=/dev/stdin get nodes -o wide

# View Cilium agents
echo "$SPOKE_KC" | kubectl --kubeconfig=/dev/stdin get pods -n kube-system -l k8s-app=cilium -o wide
```

### 2. After a Windows Reboot / Screen Idle
The system is configured with 24/7 background keepalive (`HybridWSLKeepAlive<IDX>`). If a host was fully rebooted:
```bash
# 1. Ensure Windows host power & persistence policies are active:
./scripts/hybrid/harden-windows-host.sh

# 2. Verify / re-join worker nodes:
./scripts/hybrid/join-home-workers.sh
```

---

## One-time node setup (new box or fresh WSL install)

```bash
# 1. Inside WSL2 on the Windows box (as root):
sudo ./scripts/hybrid/setup-wsl2-node.sh 1

# 2. Approve Tailscale login in browser when prompted

# 3. From the Mac: Harden Windows host (lid, power, updates, keep-alive)
./scripts/hybrid/harden-windows-host.sh --node 1

# 4. From the Mac: Join node to cluster
./scripts/hybrid/join-home-workers.sh --node 1
```

---

## Files

| Script | Purpose |
|--------|---------|
| `harden-windows-host.sh` | **Windows Server Hardening**: Lid close do nothing, disable updates, keepalive, powercfg |
| `recover-wsl2-nat.sh` | **Run this from Mac** after reboot/disconnect |
| `join-home-workers.sh` | Verify/rejoin all nodes (called by recover) |
| `home-worker-join.sh` | Runs on the WSL2 node — kubeadm join logic |
| `setup-wsl2-node.sh` | One-time node provisioning (containerd, kubelet, Tailscale) |
| `home-lab.env` | Node registry — gitignored, real values only |
| `home-lab.env.example` | Template for `home-lab.env` |
