# Manifests Directory

This directory contains infrastructure definitions for the Zero-Ops Platform.

## Structure

- `core/` - Bootstrap components (CAPI operator, providers)
- `classes/` - ClusterClass definitions (Talos-based topologies)

## Usage

All manifests are embedded in the CLI binary via `go:embed` and applied during bootstrap.
