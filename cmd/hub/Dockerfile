FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/hub/ ./cmd/hub/
COPY internal/hub/ ./internal/hub/
COPY internal/assets/ ./internal/assets/

RUN CGO_ENABLED=0 GOOS=linux go build -o hub ./cmd/hub

FROM alpine:latest

RUN apk --no-cache add ca-certificates kubectl

WORKDIR /root/

COPY --from=builder /app/hub .

CMD ["./hub"]
