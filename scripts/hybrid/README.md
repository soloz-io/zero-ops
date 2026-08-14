# Hybrid Home-Lab Scripts (ADR-046)

One command to run from the Mac after any Windows reboot or network disconnect:

```bash
./scripts/hybrid/recover-wsl2-nat.sh --node 1
```

That's it. It handles everything:
- WinNAT restart on Windows (rebuilds vEthernet WSL)
- Tailscale re-auth if needed (prints login URL; approve in browser)
- kubelet drop-in push (After=containerd)
- kubeadm rejoin + node Ready check

---

## Normal boot (no manual action needed)

After the fixes are applied, `wsl2-node-1` self-heals on Windows boot:

1. WSL2 starts → systemd starts tailscaled
2. `ExecStartPre` polls `172.27.32.1 UDP/53` until WinNAT DNS relay is up (≤90s)
3. tailscaled authenticates → Tailscale connects
4. `hybrid-home-worker-join.timer` fires → kubeadm join verifies membership

No Mac intervention required after a clean Windows reboot.

---

## When to run `recover-wsl2-nat.sh`

Run it when `wsl2-node-1` shows `NotReady` or Tailscale shows `NoState` and the
self-heal hasn't kicked in (e.g. WinNAT fully crashed, not just slow to start):

```bash
# Single node (usual case)
./scripts/hybrid/recover-wsl2-nat.sh --node 1

# Skip kubeadm rejoin (network/Tailscale fix only)
./scripts/hybrid/recover-wsl2-nat.sh --node 1 --skip-join

# Check spoke node status
kubectl --kubeconfig=k8-secrets/kubeconfig/hub-hybrid-dev.kubeconfig get nodes
```

---

## One-time node setup (new box or fresh WSL install)

```bash
# 1. Inside WSL2 on the Windows box (as root):
sudo ./scripts/hybrid/setup-wsl2-node.sh 1

# 2. Approve Tailscale login in browser when prompted

# 3. From the Mac:
./scripts/hybrid/join-home-workers.sh --node 1
```

---

## Files

| Script | Purpose |
|--------|---------|
| `recover-wsl2-nat.sh` | **Run this from Mac** after reboot/disconnect |
| `join-home-workers.sh` | Verify/rejoin all nodes (called by recover) |
| `home-worker-join.sh` | Runs on the WSL2 node — kubeadm join logic |
| `setup-wsl2-node.sh` | One-time node provisioning (containerd, kubelet, Tailscale) |
| `home-lab.env` | Node registry — gitignored, real values only |
| `home-lab.env.example` | Template for `home-lab.env` |
