---
name: document-and-implement-technical-root-cause-analysis
description: Workflow command scaffold for document-and-implement-technical-root-cause-analysis in wire.
allowed_tools: ["Bash", "Read", "Write", "Grep", "Glob"]
---

# /document-and-implement-technical-root-cause-analysis

Use this workflow when working on **document-and-implement-technical-root-cause-analysis** in `wire`.

## Goal

Documents a technical root cause analysis (TRD/WIP) and implements the corresponding code fix or optimization.

## Common Files

- `docs/trds/WIP-*/README.md`
- `internal/*/*.go`
- `examples/observability-stack/grafana/dashboards/wire.json`

## Suggested Sequence

1. Understand the current state and failure mode before editing.
2. Make the smallest coherent change that satisfies the workflow goal.
3. Run the most relevant verification for touched files.
4. Summarize what changed and what still needs review.

## Typical Commit Signals

- Create or update docs/trds/WIP-XX/README.md with analysis and plan.
- Implement code changes in relevant internal/* files as per the TRD.
- Update related tests if necessary.
- Optionally update observability dashboards if the change affects metrics.

## Notes

- Treat this as a scaffold, not a hard-coded script.
- Update the command if the workflow evolves materially.