FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /workspace-sync ./cmd/main.go

# debian-slim rather than distroless, because §14.2 needs real FUSE tooling.
#
# The four packages are not interchangeable and each is load-bearing:
#
#   squashfs-tools   mksquashfs — CREATES the archive. Easy to omit, because
#                    squashfuse sounds like it would provide it and does not.
#                    Without this every CreateArchive fails, and since archive
#                    creation is best-effort, the failure is a log line and the
#                    O(1) restore path silently never exists.
#   squashfuse       mounts the archive read-only.
#   fuse-overlayfs   lays the writable upper layer over it.
#   fuse3            fusermount3, which both of the above mount through.
#
#   ca-certificates  the root CA bundle. NOT optional, and easy to miss: the
#                    previous base was distroless/static, which ships one, while
#                    debian-slim does not. Without it every HTTPS call to object
#                    storage fails with `x509: certificate signed by unknown
#                    authority` — and because restore treats an unreachable
#                    bucket as fatal (deliberately, §14.2: starting empty would
#                    let the backstop overwrite a real workspace), the sidecar
#                    crash-loops, its startup probe never passes, the workload
#                    never starts, and the sandbox never becomes ready. Observed
#                    exactly that way: chat requests timed out with a 504 while
#                    the pod sat in Init:CrashLoopBackOff.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates squashfs-tools squashfuse fuse-overlayfs fuse3 \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /workspace /ws-staging

COPY --from=build /workspace-sync /usr/local/bin/workspace-sync

# No USER: this container runs as root, set explicitly by the operator's
# securityContext (privileged, runAsUser 0). FUSE mounting and Bidirectional
# mount propagation both require it — see workspace_sync.go for why the
# non-privileged variants cannot work. An image-level USER here would be
# overridden anyway and would only mislead the next reader.
ENTRYPOINT ["workspace-sync"]
