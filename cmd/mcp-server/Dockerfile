FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/mcp-server/ ./cmd/mcp-server/
COPY internal/platform/agent-core/ ./internal/platform/agent-core/
COPY internal/kube-sbt/interfaces/ ./internal/kube-sbt/interfaces/
COPY internal/kube-sbt/models/ ./internal/kube-sbt/models/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build -mod=mod -o mcp-server ./cmd/mcp-server

FROM alpine:3.21

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/mcp-server .

EXPOSE 8080

CMD ["./mcp-server"]
