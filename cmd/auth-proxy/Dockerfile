FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY cmd/auth-proxy/go.mod cmd/auth-proxy/go.sum ./
COPY cmd/auth-proxy/main.go ./
COPY internal/auth-proxy/ ./internal/auth-proxy/

RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o auth-proxy .

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/auth-proxy .

EXPOSE 8080

CMD ["./auth-proxy"]
