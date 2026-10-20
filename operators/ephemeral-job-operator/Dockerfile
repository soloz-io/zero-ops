FROM golang:1.26 AS builder
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux go build -a -o manager ./cmd
# ADR-108: the preview-access service ships in the same image, so one digest
# pins both and they cannot drift apart on the PreviewAccess API.
RUN CGO_ENABLED=0 GOOS=linux go build -a -o preview-access ./cmd/preview-access

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /workspace/preview-access .
USER 65532:65532
ENTRYPOINT ["/manager"]
