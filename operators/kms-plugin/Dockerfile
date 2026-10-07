# The KMS v2 plugin image.
#
# Shaped like every other operator image here (spoke-identity-operator is the model):
# a cross-compiling builder stage, a distroless static runtime, a non-root uid. The
# differences below are forced by what this binary IS, not by preference.
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Its own Go module, so only its own manifest is copied. It pins k8s.io/kms to the
# Kubernetes version the control plane runs; pinning that in the platform's shared
# module dragged k8s.io/apiserver and controller-runtime backwards for every other
# operator.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/

# CGO_ENABLED=0 because the runtime below has no libc. -trimpath so the binary does
# not embed build paths, which is the one reproducibility lever available without
# changing how the whole fleet is built.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -a -o kms-plugin ./cmd/

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/kms-plugin .

# 65532:65532 is distroless's `nonroot`, and this plugin CAN run as it — which is not
# obvious, because it has to create a unix socket in a hostPath directory that kubelet
# would otherwise make root-owned and 0755.
#
# The directory is pre-created with this ownership by a preKubeadmCommand, so the
# plugin never needs root to bind:
#
#     install -d -o 65532 -g 65532 -m 0700 /var/run/kms
#
# The API server reads the socket AS ROOT and root bypasses the permission check, so
# 0700 owned by 65532 serves the API server while excluding everything else on the
# node. Running the plugin as root to avoid one `install` would have given a process
# that can ask for every DEK in the cluster to be unwrapped more privilege than it
# needs, for the life of the node.
USER 65532:65532
ENTRYPOINT ["/kms-plugin"]
