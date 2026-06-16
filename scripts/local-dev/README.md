# Zero-Ops Local Development Setup

This directory contains automation scripts to bootstrap the **Synchronous Hybrid Split Architecture** for local development.

The Zero-Ops platform leverages a Hub-and-Spoke topology. Running massive workloads entirely on a single machine can cause Docker resource exhaustion (OOM errors). The "Synchronous Hybrid Split" resolves this by distributing the platform across two physical machines on the same local network, while uniquely bypassing CAPD Volume Mount Locality bugs via SSHFS.

1. **The Brain (Mac Host):** Hosts the Hub cluster natively. It runs the Crossplane engines and stores the state of the platform.
2. **The Muscle (Windows Host):** Acts purely as an execution compute node for Spoke fleets. 

## Prerequisites

- Two physical machines connected to the same LAN (e.g., Windows and Mac).
- Secure SSH access established from the Mac to the Windows host.
- Docker Desktop installed on both machines.
- Windows Subsystem for Linux (WSL2) enabled on the Windows machine.
- `kind`, `kubectl`, `clusterctl`, and `hub` CLI installed on your Mac.

---

## 🛠️ Operational Workflow

To safely defeat CAPD Volume Locality limitations without complex SSHFS file bridges, we use the **Asymmetric Split** pattern. We create a dedicated "Ingestion Engine" (`kind` cluster) on the Windows machine. By injecting a custom `hostPath` mount into the Windows `kind` cluster, CAPD controllers running inside it can natively bind-mount the Windows Docker Desktop `/tmp` directory.

### Phase 1: Setup Windows Locality Bridge (Run Once)

1. **On Windows (PowerShell - Admin):**
   Expose the Windows host SSH server and configure default shell access.
   ```powershell
   .\scripts\local-dev\setup-windows-docker-host.ps1
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

3. **On Mac:**
   First, dynamically patch your local Crossplane Compositions to enable Windows routing. This modifies the local manifests to point to the Windows Ingestion Engine instead of the default Mac Hub. 
   *(Note: You must commit and push the patched files so ArgoCD applies them)*
   ```bash
   ./scripts/local-dev/enable-windows-routing.sh
   ```

4. **On Mac:**
   Run the distributed bootstrap wrapper script. This script automatically:
   - Connects to Windows over SSH to securely pull the Windows cluster credentials.
   - Bootstraps the Hub cluster on the Mac natively.
   - Injects the credentials so Crossplane uses the Windows Ingestion Engine.
   - Deploys your local SpokePools directly onto Windows!
   
   ```bash
   ./scripts/local-dev/bootstrap-distributed.sh
   ```
