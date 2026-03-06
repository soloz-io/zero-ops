---
inclusion: always
---

# Development Standards Reference

All development standards are maintained in `.claude/skills/standards/`. 
Consult these before executing any task.

## Core Standards

### Task Execution
**Reference:** `.claude/skills/standards/task-execution-standards.md`
- Quality standards and integration requirements
- Sequential approval process
- Spec development process

### Testing
**Reference:** `.claude/skills/standards/priority-based-testing.md`
- 3-tier priority framework (Critical/Important/Validation)
- Get user approval on priority ranking
- Maximum 2 verification attempts

### Code Modularity
**Reference:** `.claude/skills/standards/code-modularity-standards.md`
- Strict separation of concerns
- Layer architecture (Presentation/Business/Data/Models)
- Never mix layers in a single file

### Task Planning
**Reference:** `.claude/skills/standards/task-planning-principles.md`
- Dependency-aware ordering
- Risk-first sequencing
- Monorepo considerations
- Task granularity and documentation standards

### Skills Usage
**Reference:** `.claude/skills/standards/skill-usage.md`
- How to invoke and use skills effectively

### Spec Review
**Reference:** `.claude/skills/standards/spec-review.md`
- Pre-implementation checklist

## Workflow

1. Read `.kiro/steering/read-skills-first.md` to understand the skills system
2. Consult relevant standards from above before starting work
3. Follow the patterns and requirements defined in each standard
