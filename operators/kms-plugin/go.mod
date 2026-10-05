// The KMS v2 plugin is its own module, deliberately.
//
// It pins k8s.io/kms to the Kubernetes version the control plane actually runs,
// because the thing it implements is that release's provider interface. Pinning it in
// the platform's shared module instead dragged k8s.io/apiserver from v0.35.0 to
// v0.31.6 and controller-runtime from v0.23.3 to v0.19.7 -- every other operator
// downgraded so this one could be correct. Four of the five operators here are already
// separate modules for the same kind of reason.
module github.com/soloz-io/zero-ops/operators/kms-plugin

go 1.26.0

require k8s.io/kms v0.31.6

require (
	github.com/gogo/protobuf v1.3.2 // indirect
	golang.org/x/net v0.26.0 // indirect
	golang.org/x/sys v0.21.0 // indirect
	golang.org/x/text v0.16.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240701130421-f6361c86f094 // indirect
	google.golang.org/grpc v1.65.0 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
)
