---
inclusion: always
purpose: Product definition, target users, features, and business objectives
scope: Product overview, user personas, key features, success metrics, technical constraints
topics: [product-purpose, target-users, core-capabilities, business-objectives, technical-constraints]
update_criteria: Product vision changes, new user personas, feature additions, constraint updates
---

# Product Overview

## Purpose
Zero-Ops v8.0 is a SaaS factory platform that provisions complete, production-grade AI-native SaaS environments on demand through a single declarative click. It transforms infrastructure provisioning from a manual process into an automated, agentic workflow.

## Target Users

### Primary Personas
- **SaaS Builders**: Companies who want to build apps like Replit, Lovable, Emergent who need production-grade backend stack to build AI-native products
- **Zero-Ops Platform Team**: Uses the same template to operate the platform itself (self-hosting model)
- **Tenant Admins**: Onboard organizations, provision environments, manage team access
- **Tenant Developers**: Build SaaS products within provisioned environments

### Secondary Personas
- **Platform Agents (AI)**: Programmatic actors using MCP-compatible clients
- **On-Call Engineers**: Review/approve destructive operations, manage autopilot PRs
- **Fleet Observers**: Monitor fleet-wide capacity, cost analysis, anomaly triage

## Key Features

### Core Capabilities
- **AINativeSaaS XRD**: Single declarative object expands into full-stack AI environment
- **MCP-First Architecture**: All platform capabilities exposed via Model Context Protocol
- **OAuth2 PKCE Authentication**: Secure authentication flow for IDE clients
- **GitOps-First**: All mutations via Git commits, ArgoCD reconciles
- **BYOC Model**: Tenant provides cloud credentials, compute runs in their account
- **Autopilot with Consent**: Agents propose changes via PRs, tenants approve

### Infrastructure Stack (per tenant)
- Kubernetes cluster (Enterprise) or namespace (Starter)
- PostgreSQL HA with pgvector (AI features)
- Object storage (Hetzner S3)
- Secret management (KSOPS + Age)
- Observability (VictoriaMetrics, OpenSearch)
- Agent runtime (gVisor sandbox)
- AI model gateway (LiteLLM)
- Auto REST API (PostgREST)

## Business Objectives

### Primary Goals
- **Reduce Time-to-Market**: From months to minutes for SaaS infrastructure setup
- **Enable AI-Native Development**: Built-in vector search, agent runtime, LLM gateway
- **Ensure Production Readiness**: HA databases, observability, security, backup/restore
- **Maintain Cost Efficiency**: BYOC model, shared infrastructure for starter tier

### Success Metrics
- Provisioning time: <15 minutes for Enterprise, <1 minute for Starter
- Zero manual configuration required
- 100% GitOps compliance (no imperative cluster writes)
- Support for 10,000+ tenant environments per management cluster

## Technical Constraints

### Architecture Principles
- **Idempotent Operations**: All API operations safe to retry infinitely
- **Eventual Consistency**: Crossplane reconciles continuously, no terminal failures
- **Async Agent Pattern**: Agents submit intents and exit, no blocking/polling
- **Multi-Tenant Isolation**: Physical (Enterprise) or namespace (Starter) isolation

### Security Requirements
- Credentials never transit through AI agents
- All endpoints require HTTPS/TLS
- JWT-based authentication with RBAC
- Network policies enforce tenant isolation
- Age encryption for secrets in Git