---
inclusion: always
---
<!------------------------------------------------------------------------------------
   Add rules to this file or a short description and have Kiro refine them for you.
   
   Learn about inclusion modes: https://kiro.dev/docs/steering/#inclusion-modes
-------------------------------------------------------------------------------------> 

- Never create any temporary test files without user approval.
- Never create Readme.md file without user approval.
- Always keep your response consice. to the point. less then 20 lines.
- You should never add fallback or mock the instructions. Do no not deviate from actual logic. Do not deviate or over-engineer the solution. I would expect the test cases to fail if there are issues or during its first run as we are following TDD and implementations will be missing which is known already. Do not bloat or deviate from actual logics. Add only what is required.  If necesary implementations are missing, then before creating the test files, get my approval and build the missing implementations. Test cases should not enrich or enhance the value produced by the source file. It should write or display the artifacts as it is produced.
- Always pick next test tasks only when the current task is validated and tests pass.