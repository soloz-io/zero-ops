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

To safely defeat CAPD Volume Locality limitations (which cause "file not found" errors when CAPD runs on a remote node), we establish an SSHFS bridge between the two machines. This allows the Windows Docker daemon to natively read temporary CAPD files from the Mac in real-time.

### Phase 1: Establish the Bridge

1. **On Windows (PowerShell - Admin):**
   Expose the Windows host SSH and ports.
   ```powershell
   .\scripts\setup-windows-docker-host.ps1
   ```

2. **On Windows (WSL2 Ubuntu):**
   Establish the synchronous filesystem bridge to the Mac.
   ```bash
   ./scripts/local-dev/setup-windows-node.sh
   ```

### Phase 2: Orchestrate and Bootstrap

3. **On Mac:**
   Run the distributed bootstrap wrapper script. This script automatically connects to Windows over SSH, executes the SSHFS ingestion script inside WSL2, configures the Mac Hub's Docker Engine to natively target the Windows host, and deploys your local SpokePools directly onto Windows!
   ```bash
   ./scripts/local-dev/bootstrap-distributed.sh
   ```
