# ADR-044: Local Provider Runtime Abstraction

**Date:** 2026-06-08
**Status:** Accepted

## Context

The local provider (`docker` / CAPD) hardcodes `/var/run/docker.sock` as the Docker API endpoint in the Kind cluster configuration and, transitively, in CAPD's DockerMachineTemplate. On macOS, Docker Desktop injects a proxy socket (`docker.proxy.sock`) into containers that overrides the explicit mount. The proxy does not support full Docker SDK operations (container lifecycle management), causing CAPD to fail with "Cannot connect to the Docker daemon."

Other developer runtimes (Colima, Rancher Desktop, native Linux Docker) expose the Docker socket at different paths or use the standard `/var/run/docker.sock` without proxy interference.

The platform must support all common developer runtimes without embedding host-specific paths into Cluster API resources.

## Decision

### Principle

Local runtime endpoints are a Day-0 bootstrap concern. The Docker API socket path is discovered or configured at bootstrap time and injected into the Kind cluster configuration. It must never appear in Cluster API resources (ClusterClass, DockerMachineTemplate, SpokePool, Composition).

### Supported Runtimes

| Runtime | Default Socket | Needs Override? |
|---------|---------------|-----------------|
| Linux Docker Engine | `/var/run/docker.sock` | No |
| Colima (macOS/Linux) | `/var/run/docker.sock` (symlink) | No |
| Rancher Desktop | `/var/run/docker.sock` | No |
| Docker Desktop (macOS) | `/var/run/docker.sock` → proxy | **Yes** — must override to `~/.docker/run/docker.sock` |

### Bootstrap Configuration

The `hub bootstrap` command accepts:

```bash
hub bootstrap --provider docker --docker-socket /Users/arun/.docker/run/docker.sock
```

or:

```bash
export ZERO_OPS_DOCKER_SOCKET=/Users/arun/.docker/run/docker.sock
hub bootstrap --provider docker
```

The CLI validates that the socket exists before creating the Kind cluster.

### Kind Config Generation

When `DockerSocketPath` is specified, the `LocalProvider` reads the Kind config template (`kind-config.yaml`), substitutes the Docker socket path using Go templates, writes a generated config to a temporary file, and passes it to `kind create cluster --config`.

When `DockerSocketPath` is empty (default), the static config file is used unchanged — preserving backward compatibility with Linux/Colima where `/var/run/docker.sock` is the real socket.

### What Must Not Change

The following must never carry Docker socket paths:

- ClusterClass (`capd-spoke-pool-v1.yaml`)
- DockerMachineTemplate extraMounts
- SpokePool CRD or XRD
- Crossplane Composition patches
- CAPD deployment manifests

These represent cluster desired state. The Docker socket path is a host-runtime implementation detail that must not leak into platform APIs.

## Ownership

This ADR defines a bootstrap configuration boundary and does not own platform resources. For resource ownership, see ADR-039.

## Consequences

### Positive

- Developers can use Docker Desktop, Colima, Rancher Desktop, or native Docker without code changes — only a CLI flag or environment variable.
- Cluster API resources remain runtime-agnostic.
- Socket validation fails early with a clear error message, avoiding opaque Kind/CAPD failures minutes later.
- No new ADR amendments required — this is a purely additive bootstrap concern.

### Negative

- Docker Desktop users must explicitly pass `--docker-socket` or set `ZERO_OPS_DOCKER_SOCKET`. The platform cannot auto-detect the proxy socket because it is injected by Docker Desktop at container runtime and not visible from the host.
- If Docker Desktop changes its proxy behavior in a future version, the override path may need updating.

## References

- ADR-036: Pluggable Infrastructure Provider Architecture
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
