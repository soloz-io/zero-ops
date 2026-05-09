# Build the kube-sbt-api binary
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
    echo "=== kube-sbt-api structure ===" && \
    ls -la cmd/kube-sbt-api/ && \
    echo "=== Building from: $(pwd) ===" && \
    echo "=== Build command: go build -a -o kube-sbt-api ./cmd/kube-sbt-api/main.go ==="

# Build using relative path from workspace root
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o kube-sbt-api ./cmd/kube-sbt-api/main.go && \
    echo "=== Build successful, binary location: ===" && \
    ls -lh kube-sbt-api

# Use distroless as minimal base image to package the binary
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/kube-sbt-api .
USER 65532:65532

ENTRYPOINT ["/kube-sbt-api"]
