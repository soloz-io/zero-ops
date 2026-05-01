// Package interfaces defines the core abstractions for the kube-sbt metering and billing system.
//
// ARCHITECTURAL DECISION (ADR 012):
// Static catalog (Meters, Features, Plans) is managed via GitOps + hub-operator CRDs.
// Dynamic runtime data (Subjects, Subscriptions, Invoices) is managed via kube-sbt REST API.
//
// IMetering: Runtime metering operations and read-only catalog access
// IBilling: Runtime subscription management and invoice queries
//
// These interfaces wrap OpenMeter SDK with namespace injection for multi-tenant isolation.
package interfaces
