package postgres

import "github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"

// Compile-time assertion: Storage must satisfy IStorage.
var _ interfaces.IStorage = (*Storage)(nil)
