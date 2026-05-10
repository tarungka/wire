---
name: coordinator-job-manager-optimization
description: Workflow command scaffold for coordinator-job-manager-optimization in wire.
allowed_tools: ["Bash", "Read", "Write", "Grep", "Glob"]
---

# /coordinator-job-manager-optimization

Use this workflow when working on **coordinator-job-manager-optimization** in `wire`.

## Goal

Optimizes job management logic in the coordinator, often for performance or correctness, typically in response to a WIP/TRD.

## Common Files

- `internal/coordinator/job_manager.go`
- `internal/coordinator/job_state_machine.go`
- `internal/coordinator/coordinator.go`
- `internal/coordinator/recovery.go`
- `internal/coordinator/job_state_machine_test.go`
- `docs/trds/WIP-*/README.md`

## Suggested Sequence

1. Understand the current state and failure mode before editing.
2. Make the smallest coherent change that satisfies the workflow goal.
3. Run the most relevant verification for touched files.
4. Summarize what changed and what still needs review.

## Typical Commit Signals

- Edit internal/coordinator/job_manager.go to optimize job handling logic.
- Update related files such as job_state_machine.go, coordinator.go, or recovery.go if needed.
- Update or add tests in job_state_machine_test.go.
- Document the change in docs/trds/WIP-XX/README.md.

## Notes

- Treat this as a scaffold, not a hard-coded script.
- Update the command if the workflow evolves materially.