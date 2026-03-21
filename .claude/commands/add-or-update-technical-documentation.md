---
name: add-or-update-technical-documentation
description: Workflow command scaffold for add-or-update-technical-documentation in wire.
allowed_tools: ["Bash", "Read", "Write", "Grep", "Glob"]
---

# /add-or-update-technical-documentation

Use this workflow when working on **add-or-update-technical-documentation** in `wire`.

## Goal

Adds or updates technical documentation, architecture diagrams, or design docs to the project, often in the docs/ folder.

## Common Files

- `docs/*.md`
- `docs/*.svg`
- `docs/*.puml`
- `docs/*.mermaid`
- `AGENTS.md`
- `CONTRIBUTING.md`

## Suggested Sequence

1. Understand the current state and failure mode before editing.
2. Make the smallest coherent change that satisfies the workflow goal.
3. Run the most relevant verification for touched files.
4. Summarize what changed and what still needs review.

## Typical Commit Signals

- Create or update one or more markdown files in docs/ (e.g., TECHNICAL_DOCUMENTATION.md, ARCHITECTURE_DIAGRAMS.md, LOW_LEVEL_DESIGN.md)
- Optionally add or update SVG, Mermaid, or PlantUML diagrams in docs/
- Optionally update or add AGENTS.md, CONTRIBUTING.md, or ROADMAP.md
- Commit with a message starting with 'docs:'

## Notes

- Treat this as a scaffold, not a hard-coded script.
- Update the command if the workflow evolves materially.