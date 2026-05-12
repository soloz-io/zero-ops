# function-cert-distribution

A Crossplane composition function that handles state-aware certificate distribution from Hub to Spoke clusters.

## Purpose

This function implements the phased rendering logic described in ADR-005 to eliminate Crossplane reconciliation deadlocks when dealing with asynchronous certificate generation and distribution.

## Features

- **Pure Reconciliation Logic**: Evaluates observed state rather than querying live cluster state
- **Short-Circuit Rendering**: Omits downstream resources when dependencies aren't ready
- **Semantic Conditions**: Emits explicit, typed conditions for SRE observability
- **Deterministic Timeouts**: Functionally deterministic timeout handling using creation timestamps
- **Complete Condition Emission**: Prevents state flapping by emitting full condition sets

## Function Logic

1. **Phase 1**: Waits for cert-manager to generate certificates
2. **Phase 2**: Waits for secret data to be projected into observed state
3. **Distribution**: Creates provider-kubernetes Object resources for spoke distribution

## Security Notes

- Secret material traverses the Crossplane gRPC pipeline
- Debug logging of full payloads is strictly prohibited
- All secret handling follows security best practices

## Usage

Deploy this function as a Crossplane composition function and reference it in your compositions that require certificate distribution to spoke clusters.