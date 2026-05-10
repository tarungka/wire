---
name: observability-dashboard-and-metrics-update
description: Workflow command scaffold for observability-dashboard-and-metrics-update in wire.
allowed_tools: ["Bash", "Read", "Write", "Grep", "Glob"]
---

# /observability-dashboard-and-metrics-update

Use this workflow when working on **observability-dashboard-and-metrics-update** in `wire`.

## Goal

Updates or adds new metrics and corresponding dashboard panels in response to code or product changes.

## Common Files

- `internal/observability/*.go`
- `examples/observability-stack/grafana/dashboards/wire.json`
- `docs/trds/WIP-*/README.md`

## Suggested Sequence

1. Understand the current state and failure mode before editing.
2. Make the smallest coherent change that satisfies the workflow goal.
3. Run the most relevant verification for touched files.
4. Summarize what changed and what still needs review.

## Typical Commit Signals

- Modify or add metric definitions in internal/observability/*.go.
- Update or create relevant dashboard panels in examples/observability-stack/grafana/dashboards/wire.json.
- Document metric changes in the relevant TRD if part of a WIP.
- Test that metrics are correctly scraped and displayed.

## Notes

- Treat this as a scaffold, not a hard-coded script.
- Update the command if the workflow evolves materially.