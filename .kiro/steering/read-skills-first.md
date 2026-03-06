---
inclusion: always
---

# Skills-First Development Approach

## Core Principle

**ALWAYS consult relevant skill documentation before executing any task.** Skills are your canonical source of truth for domain-specific patterns, standards, and best practices.
you must read your relevant skill before implementing your tasks.

## Skill Directory Structure

The `.claude/skills/` directory contains specialized knowledge organized by role:

- **backend-developer/**: Go and Python service patterns, API design, database patterns
- **frontend-developer/**: Next.js, React, TypeScript, component building, testing
- **devops-engineer/**: Infrastructure, deployment, monitoring, OpenTofu patterns
- **design-system/**: UI component standards, accessibility, design tokens
- **solution-architect/**: System design, integration patterns, architecture decisions
- **product-owner/**: Requirements analysis, spec development, user stories
- **standards/**: Cross-cutting standards and conventions

## Mandatory Workflow

### 1. Identify Task Domain
Before starting any task, determine which skill domain(s) apply:
- Frontend work → `frontend-developer/SKILLS.md`
- Backend services → `backend-developer/SKILL.md`
- Infrastructure → `devops-engineer/SKILL.md`
- UI components → `design-system/SKILL.md`
- Architecture decisions → `solution-architect/SKILLS.md`

### 2. Read Relevant Skills
**MUST read the primary SKILL.md file** for the domain before proceeding. Each skill document contains:
- Technology stack standards
- Code patterns and conventions
- Architecture guidelines
- Quality requirements
- Integration patterns

### 3. Apply Skill-Specific Patterns
Follow the patterns, conventions, and standards defined in the skill documentation:
- Use prescribed technology versions
- Follow established code organization
- Apply documented design patterns
- Respect quality standards
- Maintain consistency with examples

### 4. Consult Specialized Sub-Skills
For complex tasks, read specialized sub-skill documents:
- `component-building/` for UI component patterns
- `testing/` for test strategies
- `patterns/` for implementation patterns
- `guides/` for step-by-step procedures
- `spec-development/` for specification creation

## Decision Authority

Skills documentation has **higher authority** than:
- General best practices
- External documentation
- Personal preferences
- Generic patterns

Skills documentation has **equal authority** with:
- Project-specific steering rules
- Architecture decision records
- Technical design documents

## Conflict Resolution

When conflicts arise between skill documents and other guidance:
1. **Skill-specific rules override general rules** for that domain
2. **Project steering rules override skill defaults** when explicitly stated
3. **Consult multiple skills** for cross-domain tasks
4. **Document deviations** when requirements force exceptions

## Quality Assurance

Before completing any task, verify:
- [ ] Relevant skill documentation was consulted
- [ ] Prescribed patterns were followed
- [ ] Technology stack matches skill standards
- [ ] Code organization aligns with skill structure
- [ ] Quality requirements are met

## Examples

**Frontend Component Task:**
1. Read `.claude/skills/frontend-developer/SKILLS.md`
2. Check `.claude/skills/frontend-developer/component-building/` for patterns
3. Verify against `.claude/skills/design-system/SKILL.md` for UI standards
4. Implement following documented conventions

**Backend API Task:**
1. Read `.claude/skills/backend-developer/SKILL.md`
2. Check language-specific patterns (Go vs Python)
3. Review `.claude/skills/backend-developer/patterns/` for API design
4. Follow polyglot architecture standards

**Infrastructure Task:**
1. Read `.claude/skills/devops-engineer/SKILL.md`
2. Check `.claude/skills/devops-engineer/tools/` for OpenTofu patterns
3. Review `.claude/skills/devops-engineer/standards/` for conventions
4. Apply infrastructure-as-code best practices

## Skill Maintenance

Skills are living documents. When you discover:
- Missing patterns that should be documented
- Outdated conventions that need updating
- New best practices to incorporate
- Conflicts between skills

Flag these for skill maintenance using the `skill-maintainer/` guidance.
