```markdown
# wire Development Patterns

> Auto-generated skill from repository analysis

## Overview

This skill teaches the core development patterns, coding conventions, and workflows used in the `wire` Go codebase. The repository focuses on robust internal logic, observability, and job coordination, with a strong emphasis on technical root cause analysis and iterative improvement. You'll learn how to structure code, document changes, optimize performance, and update observability dashboards following established team practices.

## Coding Conventions

- **File Naming:** Use `camelCase` for Go source files.
  - Example: `jobManager.go`, `jobStateMachine.go`
- **Import Style:** Use relative imports within the module.
  - Example:
    ```go
    import (
        "internal/coordinator"
        "internal/observability"
    )
    ```
- **Export Style:** Use named exports for functions, types, and variables.
  - Example:
    ```go
    // Exported function
    func NewJobManager() *JobManager { ... }

    // Exported type
    type JobManager struct { ... }
    ```
- **Commit Messages:** Prefix with `perf`, `docs`, or `fix` as appropriate. Keep messages concise (~65 characters).
  - Example: `perf: optimize job deduplication in coordinator`

## Workflows

### Document and Implement Technical Root Cause Analysis
**Trigger:** When investigating a production or test issue, writing a technical design doc (TRD/WIP), and implementing the fix.
**Command:** `/new-trd-implementation`

1. Create or update a TRD document:
    - `docs/trds/WIP-XX/README.md`
    - Include analysis, findings, and a plan of action.
2. Implement code changes as per the TRD:
    - Edit relevant files in `internal/*/*.go`.
    - Example:
      ```go
      // internal/coordinator/jobManager.go
      func (jm *JobManager) FixDuplicateJobs() { ... }
      ```
3. Update related tests if necessary.
    - Example: `internal/coordinator/job_state_machine_test.go`
4. Optionally update observability dashboards if metrics are affected.
    - Example: `examples/observability-stack/grafana/dashboards/wire.json`

---

### Observability Dashboard and Metrics Update
**Trigger:** When adding new metrics, changing metric semantics, or visualizing new data in Grafana.
**Command:** `/update-metrics-dashboard`

1. Modify or add metric definitions in `internal/observability/*.go`.
    - Example:
      ```go
      // internal/observability/metrics.go
      var JobLatency = prometheus.NewHistogram(...)
      ```
2. Update or create dashboard panels in:
    - `examples/observability-stack/grafana/dashboards/wire.json`
3. Document metric changes in the relevant TRD if part of a WIP.
    - `docs/trds/WIP-XX/README.md`
4. Test that metrics are correctly scraped and displayed.

---

### Coordinator Job Manager Optimization
**Trigger:** When optimizing job management logic for performance or correctness, often in response to a WIP/TRD.
**Command:** `/optimize-job-manager`

1. Edit `internal/coordinator/job_manager.go` to optimize job handling logic.
    - Example:
      ```go
      func (jm *JobManager) SubmitJob(job *Job) error {
          // Improved duplicate check logic
      }
      ```
2. Update related files as needed:
    - `job_state_machine.go`, `coordinator.go`, `recovery.go`
3. Update or add tests in `job_state_machine_test.go`.
4. Document the change in `docs/trds/WIP-XX/README.md`.

## Testing Patterns

- **Test File Naming:** Test files use the pattern `*.test.*` (e.g., `job_state_machine_test.go`).
- **Framework:** No explicit testing framework detected; standard Go testing is likely used.
- **Example:**
  ```go
  // internal/coordinator/job_state_machine_test.go
  func TestJobStateTransitions(t *testing.T) {
      // Test logic here
  }
  ```

## Commands

| Command                   | Purpose                                                        |
|---------------------------|----------------------------------------------------------------|
| /new-trd-implementation   | Start a new TRD-based analysis and implementation workflow     |
| /update-metrics-dashboard | Update or add metrics and dashboard panels                     |
| /optimize-job-manager     | Optimize job manager logic for performance or correctness      |
```
