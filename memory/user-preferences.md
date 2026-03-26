---
purpose: User preferences, communication style, and workflow patterns
scope: Response preferences, approval requirements, development approach, interaction patterns
topics: [communication-style, approval-workflow, development-approach, testing-preferences]
update_criteria: User feedback on communication, new preferences expressed, workflow changes
---

# User Preferences & Instructions

## Communication Style
- Prefers concise responses (under 20 lines)
- Values technical accuracy over verbose explanations
- Wants design-level discussion, not implementation details during report-only mode
- Expects analysis of related files before answering, never assumptions
- Keep responses technical at high level, concise and to the point

## Approval Workflow
- Wants approval before creating temporary files or README files
- Requires design approval before proceeding with implementation
- Values practical implementation patterns that can be directly applied
- Works on one demo at a time with clear progression

## Development Approach
- Follows TDD approach - expects tests to fail initially
- Emphasizes not deviating from actual logic or over-engineering
- Values minimal code that addresses requirements directly
- Expects E2E tests only, rejects unit tests
- Requires actual cluster or local execution validation

## Testing Preferences
- Tests must use same code paths as production
- Tests should hold testing/asserting logic only, not business logic
- Production flow should not require re-implementation from test cases
- Only proceed to next tasks when current task is validated and tests pass

## Memory Management
- Identified issue with memory pollution across multiple contexts
- Prefers separate memory files for different topics with clear frontmatter
- Values context-appropriate memory updates, not single-file mixing

## Report-Only Mode
- Do not implement anything, just report
- Analyze related files completely before answering
- Do not generate configurations or code during conversation
- Keep conversation at design level, not implementation details
- Analyze user reported prompt with required involved files before proposing fix

## Interface Preferences
- **MCP-Only Interaction**: All tenant/platform interactions MUST be via MCP (Model Context Protocol)
- **No CLI/UI in Phase 1-2**: CLI is ONLY for initial hub bootstrap, not for tenant operations
- **No Web UI in Phase 1-2**: All operations through MCP servers, no web console
- **Agent-First Design**: Platform designed for AI agents as primary users, not humans

## GitOps Workflow
- **Never apply manifests manually**: All Kubernetes resources MUST be managed via GitOps
- **ArgoCD is source of truth**: Commit to Git, ArgoCD syncs automatically
- **Bootstrap exception**: Only hub bootstrap CLI can apply root ArgoCD app, everything else via Git