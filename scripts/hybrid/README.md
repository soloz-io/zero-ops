# Hybrid Home-Lab Operations & Runbook (ADR-046 §13)

## The Single Command

Home workers are **Hyper-V Gen2 VMs running immutable Flatcar Container Linux**,
provisioned 100% remotely from macOS via SSH. Provisioning, recovery, and
verification are handled by **one script**:

```bash
# Provision / (re)provision a node — prepares Windows (power/Hyper-V),
# builds the Flatcar base VHDX + Ignition ISO, creates the Gen2 VM, and
# joins the spoke via kubeadm.
./scripts/hybrid/provision-flatcar-worker.sh --node 1
./scripts/hybrid/provision-flatcar-worker.sh --cluster hub 

# Provision all registered nodes (home-lab.env)
./scripts/hybrid/provision-flatcar-worker.sh

# Verify Ready status of all registered nodes without changing anything
./scripts/hybrid/provision-flatcar-worker.sh --verify

# Autonomous Tailscale re-auth (embed a reusable auth-key)
./scripts/hybrid/provision-flatcar-worker.sh --node 1 --ts-authkey tskey-...
```

> **The run is only a success when the node reports `Ready`** in the spoke
> cluster. The provisioner exits non-zero if any targeted node fails to
> become Ready.

### Phases run per node

| Phase | What it does |
|-------|--------------|
| [0/6] | Verify offline worker binary cache on Mac (kubelet, kubeadm, tailscale) |
| [1/6] | SSH reachability gate to the Windows host |
| [2/6] | Windows host & Hyper-V preparation (power/lid, Hyper-V feature, directories) |
| [3/6] | Flatcar base VHDX & Ignition ISO bundle preparation |
| [4/6] | Provision Generation 2 Hyper-V VM with attached Ignition DVD ISO |
| [5/6] | Wait for Flatcar first-boot, Tailscale mesh, and kubeadm join |
| [6/6] | Verify node Ready — the success gate |

### How it works

1. **Ignition config** (`config.ign`, spec 3.4.0) declares hostname, Tailscale
   auth, kubelet, and a oneshot `kubeadm join` unit — no post-boot
   configuration needed. Join credentials (`<spoke>-home-worker-join` Secret,
   minted by hub-operator) are baked into the ISO.
2. **Native Tailscale daemon** joins the VM to the tailnet
   (`tailscale up --hostname=flatcar-node-<N>`); the spoke reachable via the
   tailnet InternalIP.
3. **Native Linux cgroup v2 + eBPF** — standard Cilium CNI deployment, no
   cgroup mount hacks.

### Node registry

Nodes are declared in `scripts/hybrid/home-lab.env` (gitignored; see
`home-lab.env.example`). Each line is
`<hostname>|<ssh-target>|<os-info>|<tailnet-host>|<box-tag>` — the
hostname/tailnet-host fields are derived by the provisioner
(`flatcar-node-<idx>`), so only the SSH target and box tag are authoritative.

The same node list lives in the SpokePool claim annotation
(`home-workers`), which drives hub-operator's join-payload Secret via
`manifests/providers/hybrid/k8s/spokepool-hybrid-composition.yaml`.

---

## Support Scripts

| Script | Purpose |
|--------|---------|
| `provision-flatcar-worker.sh` | **THE entry point** — Hyper-V + Flatcar provisioning |
| `render-home-workers.sh` | Render the SpokePool `home-workers` JSON annotation |
| `home-lab.env` | Node registry — gitignored, real values only |
| `home-lab.env.example` | Template for `home-lab.env` |