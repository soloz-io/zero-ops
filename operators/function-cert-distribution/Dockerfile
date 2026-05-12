# Build the function binary
FROM golang:1.21-alpine AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o function-cert-distribution .

# Final image
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/function-cert-distribution .
ENTRYPOINT ["/function-cert-distribution"]