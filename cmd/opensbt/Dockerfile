FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/opensbt/ ./cmd/opensbt/
COPY internal/opensbt/ ./internal/opensbt/

RUN CGO_ENABLED=0 GOOS=linux go build -o opensbt ./cmd/opensbt

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/opensbt .

EXPOSE 8080

CMD ["./opensbt"]
