FROM golang:1.26 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -a -o workspace-sync ./cmd

# distroless/static, matching ephemeral-job-operator: this holds the object
# store credential, so it gets the smallest surface available. It needs
# ca-certificates for TLS to the S3 endpoint, which the :nonroot base carries.
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/workspace-sync .
# 65532 is distroless' nonroot uid. The workload runs as 1000 and both write to
# the same volume, which is why the operator sets an fsGroup on the pod — see
# buildPodSpec.
USER 65532:65532
ENTRYPOINT ["/workspace-sync"]
