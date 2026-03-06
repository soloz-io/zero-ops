# Testing Standards

## Mandatory Testing Framework

All agents MUST follow the priority-based testing standard for any testing task.

**Reference:** `.claude/skills/standards/priority-based-testing.md`

## Key Requirements

1. **Always use 3-tier priority framework** (Critical/Important/Validation)
2. **Get user approval** on priority ranking before implementing tests
3. **Respect tier limits** (1-2, 2-3, 1-2 tests per tier)
4. **At least 1 Tier 1 test** that validates critical path
5. **Maximum 2 verification attempts** to fix failing tests

## No Exceptions

This standard overrides any other testing guidance. If you encounter conflicting instructions, follow the priority-based testing standard and flag the conflict to the user.
