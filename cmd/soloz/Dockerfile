FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/soloz/ ./cmd/soloz/
COPY internal/soloz-cli/ ./internal/soloz-cli/
COPY internal/assets/ ./internal/assets/

RUN CGO_ENABLED=0 GOOS=linux go build -mod=mod -o soloz ./cmd/soloz

FROM alpine:latest

RUN apk --no-cache add ca-certificates kubectl

WORKDIR /root/

COPY --from=builder /app/soloz .

CMD ["./soloz"]
