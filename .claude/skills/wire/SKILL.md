---
name: wire-conventions
description: Development conventions and patterns for wire. Go Go project with mixed commits.
---

# Wire Conventions

> Generated from [tarungka/wire](https://github.com/tarungka/wire) on 2026-03-21

## Overview

This skill teaches Claude the development patterns and conventions used in wire.

## Tech Stack

- **Primary Language**: Go
- **Framework**: Go
- **Architecture**: hybrid module organization
- **Test Location**: separate

## When to Use This Skill

Activate this skill when:
- Making changes to this repository
- Adding new features following established patterns
- Writing tests that match project conventions
- Creating commits with proper message format

## Commit Conventions

Follow these commit message conventions based on 50 analyzed commits.

### Commit Style: Mixed Style

### Prefixes Used

- `docs`
- `feat`
- `update`
- `chore`
- `fix`
- `add`

### Message Guidelines

- Average message length: ~44 characters
- Keep first line concise and descriptive
- Use imperative mood ("Add feature" not "Added feature")


*Commit message example*

```text
docs: low level design (#131)
```

*Commit message example*

```text
feat: Cost-Optimized CI/CD with Message Triggers (#127)
```

*Commit message example*

```text
fix: Fix golangci-lint timeout issues in GitHub workflow
```

*Commit message example*

```text
chore: remove stable bot
```

*Commit message example*

```text
build: do not run ci on PR to master
```

*Commit message example*

```text
docs: Add comprehensive engineering roadmap and recommendations (#98)
```

*Commit message example*

```text
docs: Comprehensive README.md update (#123)
```

*Commit message example*

```text
docs: Add comprehensive AGENTS.md documentation (#121)
```

## Architecture

### Project Structure: Single Package

This project uses **hybrid** module organization.

### Configuration Files

- `.github/workflows/build.yml`
- `.github/workflows/docker-image.yml`
- `.github/workflows/go.yml`
- `.github/workflows/label.yml`
- `.github/workflows/linter.yml`
- `.github/workflows/pr-validate.yml`
- `.github/workflows/release.yml`
- `.github/workflows/security.yml`
- `.github/workflows/test-full.yml`
- `Dockerfile`
- `utils/generate-random-data/Dockerfile`

### Guidelines

- This project uses a hybrid organization
- Follow existing patterns when adding new code

## Code Style

### Language: Go

### Naming Conventions

| Element | Convention |
|---------|------------|
| Files | camelCase |
| Functions | camelCase |
| Classes | PascalCase |
| Constants | SCREAMING_SNAKE_CASE |

### Import Style: Relative Imports

### Export Style: Named Exports


*Preferred import style*

```typescript
// Use relative imports
import { Button } from '../components/Button'
import { useAuth } from './hooks/useAuth'
```

*Preferred export style*

```typescript
// Use named exports
export function calculateTotal() { ... }
export const TAX_RATE = 0.1
export interface Order { ... }
```

## Common Workflows

These workflows were detected from analyzing commit patterns.

### Feature Development

Standard feature implementation workflow

**Frequency**: ~11 times per month

**Steps**:
1. Add feature implementation
2. Add tests for feature
3. Update documentation

**Files typically involved**:
- `docs/*`
- `**/*.test.*`

**Example commit sequence**:
```
feat: Feature architecture improvements (#18)
chore: updated the go version to v1.23 (#19)
chore: updated the go version to v1.23 (#31)
```

### Refactoring

Code refactoring and cleanup workflow

**Frequency**: ~4 times per month

**Steps**:
1. Ensure tests pass before refactor
2. Refactor code structure
3. Verify tests still pass

**Files typically involved**:
- `src/**/*`

**Example commit sequence**:
```
feat: Feature architecture improvements (#18)
chore: updated the go version to v1.23 (#19)
chore: updated the go version to v1.23 (#31)
```

### Add Or Update Technical Documentation

Adds or updates technical documentation, architecture diagrams, or design docs to the project, often in the docs/ folder.

**Frequency**: ~2 times per month

**Steps**:
1. Create or update one or more markdown files in docs/ (e.g., TECHNICAL_DOCUMENTATION.md, ARCHITECTURE_DIAGRAMS.md, LOW_LEVEL_DESIGN.md)
2. Optionally add or update SVG, Mermaid, or PlantUML diagrams in docs/
3. Optionally update or add AGENTS.md, CONTRIBUTING.md, or ROADMAP.md
4. Commit with a message starting with 'docs:'

**Files typically involved**:
- `docs/*.md`
- `docs/*.svg`
- `docs/*.puml`
- `docs/*.mermaid`
- `AGENTS.md`
- `CONTRIBUTING.md`
- `ROADMAP.md`

**Example commit sequence**:
```
Create or update one or more markdown files in docs/ (e.g., TECHNICAL_DOCUMENTATION.md, ARCHITECTURE_DIAGRAMS.md, LOW_LEVEL_DESIGN.md)
Optionally add or update SVG, Mermaid, or PlantUML diagrams in docs/
Optionally update or add AGENTS.md, CONTRIBUTING.md, or ROADMAP.md
Commit with a message starting with 'docs:'
```

### Add Or Update Ci Cd Workflows

Creates or modifies GitHub Actions workflows for CI/CD, linting, or automation.

**Frequency**: ~1 times per month

**Steps**:
1. Create or update YAML files in .github/workflows/
2. Optionally add or update Makefile, scripts, or config files related to CI/CD
3. Commit with message mentioning 'ci', 'build', 'workflow', or 'lint'

**Files typically involved**:
- `.github/workflows/*.yml`
- `.github/workflows/*.yaml`
- `Makefile`
- `scripts/*.sh`
- `.golangci.yml`

**Example commit sequence**:
```
Create or update YAML files in .github/workflows/
Optionally add or update Makefile, scripts, or config files related to CI/CD
Commit with message mentioning 'ci', 'build', 'workflow', or 'lint'
```

### Feature Development With Multi File Changes

Implements a new feature or major refactor, typically touching multiple internal packages, updating go.mod/go.sum, and possibly adding tests.

**Frequency**: ~2 times per month

**Steps**:
1. Edit or add files in internal/, pipeline/, sinks/, sources/, or cmd/
2. Update go.mod and go.sum for dependencies
3. Optionally add or update tests (e.g., *_test.go)
4. Optionally update documentation in docs/
5. Commit with message starting with 'feat:' or 'refactor:'

**Files typically involved**:
- `internal/**/*.go`
- `pipeline/**/*.go`
- `sinks/**/*.go`
- `sources/**/*.go`
- `cmd/**/*.go`
- `go.mod`
- `go.sum`

**Example commit sequence**:
```
Edit or add files in internal/, pipeline/, sinks/, sources/, or cmd/
Update go.mod and go.sum for dependencies
Optionally add or update tests (e.g., *_test.go)
Optionally update documentation in docs/
Commit with message starting with 'feat:' or 'refactor:'
```

### Add Or Update Readme Or Contributing

Adds or updates README.md or CONTRIBUTING.md to improve onboarding, usage, or contribution guidelines.

**Frequency**: ~1 times per month

**Steps**:
1. Edit or add README.md and/or CONTRIBUTING.md
2. Optionally update docs/ or add links to documentation
3. Commit with message starting with 'docs:' or mentioning 'readme', 'contributing'

**Files typically involved**:
- `README.md`
- `CONTRIBUTING.md`

**Example commit sequence**:
```
Edit or add README.md and/or CONTRIBUTING.md
Optionally update docs/ or add links to documentation
Commit with message starting with 'docs:' or mentioning 'readme', 'contributing'
```

### Add New Source Or Sink Connector

Implements a new data source or sink (e.g., Kafka, Mongo, Elasticsearch), updating config examples and pipeline logic.

**Frequency**: ~1 times per month

**Steps**:
1. Add or update files in sources/ or sinks/ (e.g., kafka.go, mongo.go, elasticsearch.go)
2. Update .config/config.json or .config/config.yaml with new examples
3. Optionally update pipeline/pipeline.go and related config files
4. Optionally update README.md to document new connector
5. Commit with message starting with 'feat:'

**Files typically involved**:
- `sources/*.go`
- `sinks/*.go`
- `.config/config.json`
- `.config/config.yaml`
- `pipeline/pipeline.go`
- `README.md`

**Example commit sequence**:
```
Add or update files in sources/ or sinks/ (e.g., kafka.go, mongo.go, elasticsearch.go)
Update .config/config.json or .config/config.yaml with new examples
Optionally update pipeline/pipeline.go and related config files
Optionally update README.md to document new connector
Commit with message starting with 'feat:'
```


## Best Practices

Based on analysis of the codebase, follow these practices:

### Do

- Use camelCase for file names
- Prefer named exports

### Don't

- Don't deviate from established patterns without discussion

---

*This skill was auto-generated by [ECC Tools](https://ecc.tools). Review and customize as needed for your team.*
