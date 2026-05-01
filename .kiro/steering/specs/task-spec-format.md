---
inclusion: manual
---
<!------------------------------------------------------------------------------------
   Add rules to this file or a short description that will apply across all your workspaces.
   
   Learn about inclusion modes: https://kiro.dev/docs/steering/#inclusion-modes
-------------------------------------------------------------------------------------> 

# TASKS CHECKPOINTS FORMAT FOR VALIDATION

Proceed with tasks.md creation.

---

## 1. Checkpoint rules (strictly follow)

- **Review gates**: Use multiple checkpoints/pitstops that require my review before moving on.
- **Value per checkpoint**: Each checkpoint must be a value deliverable—the outcome of a set of implementations that can be tested and reviewed via test cases/scripts.
- **Verification**: Each checkpoint must have concrete verification criteria. Verification must be outcome-driven only: validate the artifacts produced by the testing files, not internal implementation.
- **User journey order**: Checkpoints must follow the user journey sequence of the entire context for which the feature is being built.
- **Clear “done”**: It must be easy to tell when a checkpoint is done. Each checkpoint is a task that comes after a set of implementation tasks.
- **Sequential**: Each checkpoint task must be completed before starting the next checkpoint’s tasks.
- **Structure**: Model checkpoints as regular tasks, e.g. `- [ ] 2. **CHECKPOINT 1: Title**`.
- **Numbering**: Use clean sequential numbering within each phase (see section 3).
- **Validation scope**: They must perform real cluster-level (or equivalent) environment validation. State this clearly in each task. Do not create any unit/integration/e2e automation testing tasks. Create only manual testing tasks with clear instructions for testing. Mention in all CHECKPOINTs that its manual testing and no script shall be created. Each phase completion shall be reported with manual testing instructions.

---

## 2. Task list structure and template

Create tasks in the exact structure below, with subtasks as shown.

```
## Task List
### Phase 1: title
- [x] 1. Implement deepagents-runtime HTTP and WebSocket API endpoints
  -
  -
  -
- [x] 2. Add FastAPI HTTP endpoints to deepagents-runtime
  -
  -
- [ ] 3. **CHECKPOINT 1: Title**
  - **Deliverable**:
  - **Verification Criteria**:
    -
    -
  - **Test Script**:
    - For any implementations under `zerotouch-platform` (infrastructure: Kubernetes manifests, Crossplane, ArgoCD), validate all infrastructure components by running the cluster validation script `/scripts/<test-script-name>.py`. The test must check deployed resources in a real or representative cluster environment.
    - For the SDK (Python package), ensure all SDK logic is tested with the package’s own test suite (e.g., `/sdk/tests/<test-name>.py`), using only the SDK’s source and its local test location.
    - For platform service implementations, validate business logic by running service-specific integration tests in the relevant service repository’s integration test folder (for example, `<service-name>/tests/integration/<test-script-name>.py` for Python or `<service-name>/tests/integration/<test-script-name>.js` for Node.js).
    - The test file name must not be generic; it must reflect the logic it is validating.
  - **Success Criteria**: All API endpoints functional and returning expected data structures
### Phase 2:
- [ ]
- [ ]
- [ ] **CHECKPOINT 2: ...
more...
```
---

## 3. Task numbering

- **Use**: `- [ ] 1. Task title` (integer + period).
- **Do not use**: `- [ ] 1.1 Task title` (decimal numbering).
- Kiro expects `1.`, `2.`, `3.` — not `1.1`, `1.2`, `1.3`.

---

## 4. Checkpoints as quality gates

Checkpoint tasks are about **achieving the deliverable AND passing validation**, not just running scripts. You cannot mark a checkpoint task complete until the validation script passes.

### When a checkpoint is complete (all must be true)

- **Deliverable works**: e.g. infrastructure deployed, Gateway has IP, service accessible.
- **Validation script passes**: exit code 0, all checks green.
- **Success criteria met**: the specific technical requirements for that checkpoint are satisfied.

### When a checkpoint is not complete

- The script runs but validation fails.
- The deliverable exists but does not work properly.
- Success criteria are not met.

**Example — CHECKPOINT 1:**

- ❌ **Not complete**: Script runs but finds pods not ready.
- ✅ **Complete**: All infrastructure pods Ready + secrets synced + script passes.

**Sequential dependency**: Each checkpoint blocks the next phase until both the deliverable works and validation passes. This keeps the foundation stable before proceeding.