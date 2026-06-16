# Zero-Ops Local Development Setup

This directory contains automation scripts to bootstrap the **Synchronous Hybrid Split Architecture** for local development.

The Zero-Ops platform leverages a Hub-and-Spoke topology. Running massive workloads entirely on a single machine can cause Docker resource exhaustion (OOM errors). The "Synchronous Hybrid Split" resolves this by distributing the platform across two physical machines on the same local network, while uniquely bypassing CAPD Volume Mount Locality bugs via SSHFS.

1. **The Brain (Mac Host):** Hosts the Hub cluster natively. It runs the Crossplane engines and stores the state of the platform.
2. **The Muscle (Windows Host):** Acts purely as an execution compute node for Spoke fleets. 

---

## Network Prerequisites

Both machines must be on the **same LAN subnet** for Kubernetes API communication:

```bash
# Check Mac's current IP
ipconfig getifaddr en0

# Verify Windows is reachable
ping 192.168.1.18
ssh -o ConnectTimeout=5 Dell@192.168.1.18 "echo OK"
```

If the Mac and Windows are on different subnets (e.g., Mac on `192.168.43.x`, Windows on `192.168.1.x`), none of the kubeconfigs below will work — connect both machines to the same WiFi/LAN.

---

## Port Architecture

The distributed setup exposes multiple Kubernetes API servers across the two machines:

| Port | Host | Cluster | Purpose |
|---|---|---|---|
| `6443` | Mac (`0.0.0.0`) | `hub-local` | Hub kind cluster control-plane |
| `6444` | Windows (`192.168.1.18`) | `zero-ops-windows-engine` | Windows Ingestion Engine (CAPD manager) |
| `6445` | Windows (`192.168.1.18`) | `local-dev` | Spoke cluster #1 (tenant workloads) |
| `6446` | Windows (`192.168.1.18`) | `local-dev` | Spoke cluster #2 (tenant workloads) |

---

## Proxy & Connectivity

The Mac's Docker containers (including the Hub kind cluster and Crossplane) reach the Windows Kubernetes API servers via `host.docker.internal`, which resolves to the Mac host from inside Docker. Two proxy mechanisms bridge the gap:

### 1. `windows-api-proxy` (socat container — created by `setup-proxy.sh`)

A Docker container running `alpine/socat` in **host networking mode** that forwards TCP traffic:

```
localhost:6444 → socat → 192.168.1.18:6444
```

This makes the Windows Ingestion Engine API available at `host.docker.internal:6444` from inside any Docker container.

```bash
# Inspect the running proxy
docker logs windows-api-proxy

# Restart if needed
docker rm -f windows-api-proxy
docker run -d --name windows-api-proxy --network host alpine/socat \
  tcp-listen:6444,fork,reuseaddr tcp:192.168.1.18:6444
```

### 2. `mac-windows-proxy.py` (Python TCP proxy — manual)

For exposing additional ports (e.g., Spoke cluster APIs on `6445`/`6446`) to Docker containers via `host.docker.internal`:

```bash
# Forward host.docker.internal:6445 → 192.168.1.18:6445
python3 scripts/local-dev/mac-windows-proxy.py 6445 192.168.1.18 6445

# Forward host.docker.internal:6446 → 192.168.1.18:6446
python3 scripts/local-dev/mac-windows-proxy.py 6446 192.168.1.18 6446
```

---

## Kubeconfig Reference

Several kubeconfig files exist for accessing the different clusters. Choose the right one based on where you're running `kubectl`:

| Location | Server | Target | Use When |
|---|---|---|---|
| `~/.kube/windows-target-engine.yaml` | `host.docker.internal:6445` | Spoke cluster (CAPD-provisioned) | **Crossplane on Hub** (inside Docker) |
| `/tmp/windows-direct.kubeconfig` | `192.168.1.18:6444` | Ingestion Engine (kind) | Direct `kubectl` from Mac host |
| `/tmp/windows-hub.kubeconfig` | `192.168.1.18:6444` | Ingestion Engine (kind) | Direct `kubectl` from Mac host |
| `/tmp/temp-kubeconfig.yaml` | `192.168.1.18:6444` | Ingestion Engine (kind) | Direct `kubectl` from Mac host |
| `/tmp/windows-spoke.kubeconfig` | `192.168.1.18:6445` | Spoke cluster #1 | Direct `kubectl` from Mac host |
| `/tmp/windows-spoke-6446.kubeconfig` | `192.168.1.18:6446` | Spoke cluster #2 | Direct `kubectl` from Mac host |
| `/tmp/spoke.kubeconfig` | `192.168.1.18:6446` | Spoke cluster #2 | Direct `kubectl` from Mac host |

```bash
# Test connectivity (run from Mac host with matching subnet)
kubectl --kubeconfig=/tmp/windows-direct.kubeconfig get nodes

# Test from inside a Docker container (uses host.docker.internal)
docker exec hub-local-control-plane kubectl --kubeconfig=/.kube/windows-target-engine.yaml get nodes
```

---

## Script Inventory

| Script | Run On | Purpose |
|---|---|---|
| `setup-windows-docker-host.ps1` | Windows (Admin) | Installs OpenSSH, opens firewall (22, 6443-6500), injects Mac SSH key, pre-pulls Docker images |
| `setup-windows-ingestion.ps1` | Windows | Creates `zero-ops-windows-engine` kind cluster, compiles patched CAPD manager |
| `setup-mac-orchestrator.sh` | Mac | SSHes into Windows, creates kind cluster, ingests kubeconfig |
| `setup-proxy.sh` | Mac | Creates/restarts socat + Python proxies for `host.docker.internal` → Windows bridging |
| `setup-windows-node.sh` | Windows WSL2 | Mounts Mac's `/var/folders` via SSHFS for CAPD volume locality |
| `bootstrap-distributed.sh` | Mac | SCPs Windows kubeconfig → patches IP → runs `hub-bootstrap.sh --topology multi` → applies SpokePool |
| `mac-windows-proxy.py` | Mac | TCP proxy for `host.docker.internal` → Windows IP bridging |
| `capd-port-mapping.patch` | — | Patches CAPD loadbalancer for static port binding |

---

## 🛠️ Operational Workflow

To safely defeat CAPD Volume Locality limitations without complex SSHFS file bridges, we use the **Asymmetric Split** pattern. We create a dedicated "Ingestion Engine" (`kind` cluster) on the Windows machine. By injecting a custom `hostPath` mount into the Windows `kind` cluster, CAPD controllers running inside it can natively bind-mount the Windows Docker Desktop `/tmp` directory.

### Phase 1: Setup Windows Locality Bridge (Run Once)

1. **On Windows (PowerShell - Admin):**
   Expose the Windows host SSH server and configure default shell access. **IMPORTANT**: You must pass your Mac's SSH public key so the Mac can securely authenticate!
   ```powershell
   # First, copy the contents of ~/.ssh/id_ed25519.pub from your Mac
   .\scripts\local-dev\setup-windows-docker-host.ps1 -MacPublicKey "ssh-ed25519 AAAAC3NzaC1... your-email@example.com"
   ```

2. **On Windows (PowerShell):**
   Create the Windows Ingestion Engine. This script automatically:
   - Creates the `kind` cluster and injects the critical `/tmp` hostPath mount to fix the Docker-in-Docker CAPD bug.
   - Downloads the upstream Cluster API source code and applies the `capd-port-mapping.patch`.
   - Natively compiles a custom AMD64 Docker image and deploys it, allowing the CAPD loadbalancer to bind to static ports (e.g., 6446) for cross-OS routing!
   ```powershell
   .\scripts\local-dev\setup-windows-ingestion.ps1
   ```

### Phase 2: Orchestrate and Bootstrap (Daily Workflow)

You can dynamically choose your platform topology during bootstrap!

**Option A: Bridged Multi-Machine Topology (Recommended for low-memory Macs)**
This builds the Hub on your Mac and provisions the Spoke clusters onto your Windows Ingestion Engine.
**IMPORTANT**: Before running the final bootstrap, edit `./scripts/local-dev/bootstrap-distributed.sh` and update `WINDOWS_USER` and `WINDOWS_IP` to match your Windows machine's credentials!
   ```bash
   # Run the distributed bootstrap wrapper script
   ./scripts/local-dev/bootstrap-distributed.sh
   ```

**Option B: Standalone Single-Machine Topology (Mac Only)**
This builds both the Hub and the Spoke clusters entirely within Docker Desktop on your Mac.
   ```bash
   # Run the standard hub-bootstrap script with the single topology
   ./scripts/hub-bootstrap.sh --provider local --topology single
   ```

---

## Troubleshooting

### Mac and Windows on different subnets
The Mac must be on the same `192.168.1.x` LAN as the Windows machine. If IPs don't match:
- Connect both to the same WiFi router
- Check Mac IP: `ipconfig getifaddr en0`
- Check Windows IP: `ipconfig` (run via SSH or RDP)

### No route to Windows
```bash
ping 192.168.1.18          # Test basic reachability
nc -zv -G 3 192.168.1.18 22  # Test SSH port
```
If both fail, the machines cannot see each other on the network.

### `host.docker.internal` not resolving
This DNS name **only works inside Docker containers**. On the Mac host itself, use the direct `192.168.1.18` kubeconfigs from `/tmp/` instead.

### `windows-api-proxy` not running
```bash
# Recreate all proxies (socat + Python)
./scripts/local-dev/setup-proxy.sh

# Or just restart the socat container
docker start windows-api-proxy
docker logs windows-api-proxy
```

### Stale kubeconfig with wrong IP
If the Windows machine's IP changed, update the server address in the kubeconfig:
```bash
sed -i '' 's/192.168.1.18/<new-windows-ip>/g' /tmp/windows-direct.kubeconfig
sed -i '' 's/192.168.1.18/<new-windows-ip>/g' ~/.kube/windows-target-engine.yaml
```
