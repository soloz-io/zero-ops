module github.com/soloz-io/zero-ops/cmd/nats-subscriber

go 1.21

replace github.com/soloz-io/zero-ops => ../..

require (
	github.com/google/uuid v1.6.0
	github.com/lib/pq v1.10.9
	github.com/soloz-io/zero-ops v0.0.0-00010101000000-000000000000
)