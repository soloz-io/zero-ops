---
inclusion: manual
---
<!------------------------------------------------------------------------------------
   Add rules to this file or a short description that will apply across all your workspaces.
   
   Learn about inclusion modes: https://kiro.dev/docs/steering/#inclusion-modes
-------------------------------------------------------------------------------------> 

1. **Phase Completion**: Complete all tasks within the current phase implementation plan. Stop execution after completing the current phase and await explicit user approval before proceeding to subsequent phases.
2. **Validation Requirement**: Ensure all validation steps (unit tests, integration tests, and manual checks) pass successfully before marking a checkpoint as complete. Checkpoint must not be marked as complete if any validation steps fail.
3. **Status Reporting**: Notify the user immediately if checkpoint validation reveals challenges or if any tasks cannot be completed as specified.
4. **Phase Boundary Control**: Never automatically proceed to next phases. Always pause and request user confirmation after completing current phase objectives.
5. **Testing Standards**: Adhere strictly to the principles defined in [INTEGRATION-TESTING-PATTERNS.md](../../../zerotouch-platform/llm-context/testing/INTEGRATION-TESTING-PATTERNS.md).
6. **Strict Zero Fallback or mock of tasks**: you should never add fallback or mock the instructions. Do not deviate from actual logic. Do not deviate or over-engineer the solution. I would expect the test cases to fail if there are issues or during its first run as we are following TDD and implementations will be missing which is known already. Do not bloat or deviate from actual logics. Add only what is required.  If necesary implementations are missing, then before creating the test files, get my approval and build the missing implementations. Test cases should not enrich or enhance the value produced by the source file. It should write or display the artifacts as it is produced.

All Tasks to be completed till - 