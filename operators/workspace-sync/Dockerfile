FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /workspace-sync ./cmd/main.go

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    squashfuse fuse3 fuse-overlayfs \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /workspace /ws-staging
COPY --from=build /workspace-sync /usr/local/bin/workspace-sync
RUN id sync >/dev/null 2>&1 || useradd -r -s /bin/false sync
USER sync
ENTRYPOINT ["workspace-sync"]
