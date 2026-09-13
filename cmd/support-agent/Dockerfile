# The Support Agent's container.
#
# Distroless and nonroot: the agent reads two files and makes outbound HTTP
# requests, so it needs no shell, no package manager and no writable filesystem.
# A support component with a shell in it is a support component somebody will
# eventually exec into on a tenant's cluster, which is the access ADR-067 says
# the platform does not have.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO off so the binary runs on the distroless static base below.
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/support-agent ./cmd/support-agent

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/support-agent /support-agent
USER 65532:65532
ENTRYPOINT ["/support-agent"]
