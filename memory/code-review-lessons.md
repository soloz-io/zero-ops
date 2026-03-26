---
purpose: Lessons learned from code review feedback
scope: Code review methodology and verification practices
topics: verification before bug reporting, actual vs assumed implementation
update_criteria: Update when receiving feedback about code review accuracy
---

# Code Review Lessons Learned

## Critical Lesson: Verify Before Reporting Bugs

**Date:** 2026-03-26
**Context:** agents-core Phase 3-6 code review

**What Happened:**
- Made assumptions about bugs without verifying actual implementation
- Reported field reference bugs (tool.Name, tool.Description) without checking actual code
- User correctly challenged: "did you verify the logics before raising the bug?"

**Key Learning:**
- ALWAYS read and verify actual implementation before reporting bugs
- Don't assume bugs exist based on partial code analysis
- Check actual field usage, imports, and method calls in context
- Verify compilation issues exist before reporting them

**Correct Code Review Process:**
1. Read complete implementation files
2. Verify actual field/method references
3. Check imports and dependencies exist
4. Confirm bugs through evidence, not assumptions
5. Only report verified deviations from specs

**User Expectation:**
- Code reviewer role is to find actual deviations, not assumed ones
- Must validate logic and implementation before raising bugs
- Accuracy and verification are critical for credibility