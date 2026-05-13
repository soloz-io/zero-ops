# Build the function binary
FROM golang:1.24-alpine AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod tidy
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o function-cert-distribution .

# Final image
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/function-cert-distribution .
# Enterprise Fix: Inject Crossplane package metadata into the OCI image root
COPY package.yaml /package.yaml

ENTRYPOINT ["/function-cert-distribution"]