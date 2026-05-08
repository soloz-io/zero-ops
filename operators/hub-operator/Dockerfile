# Build the manager binary
FROM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Copy root go.mod/go.sum
COPY go.mod go.sum ./
RUN echo "=== Go module info ===" && \
    head -5 go.mod && \
    echo "=== Verifying and downloading dependencies ===" && \
    go mod verify || true && \
    go mod download

# Copy entire source tree to preserve module structure
COPY . .

# Debug: Show directory structure
RUN echo "=== Workspace structure ===" && \
    ls -la && \
    echo "=== Hub-operator structure ===" && \
    ls -la operators/hub-operator/ && \
    echo "=== Hub-operator internal ===" && \
    ls -la operators/hub-operator/internal/ && \
    echo "=== Building from: $(pwd) ===" && \
    echo "=== Build command: go build -a -o manager ./operators/hub-operator/cmd/main.go ==="

# Build using relative path from workspace root
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager ./operators/hub-operator/cmd/main.go && \
    echo "=== Build successful, binary location: ===" && \
    ls -lh manager

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]


