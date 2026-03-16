FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY cmd/mcp-server/go.mod cmd/mcp-server/go.sum ./

RUN go mod download

COPY cmd/mcp-server/main.go ./

RUN CGO_ENABLED=0 GOOS=linux go build -o mcp-server .

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/mcp-server .

EXPOSE 8080

CMD ["./mcp-server"]
