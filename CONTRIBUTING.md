# Contributing to Wire

Thank you for your interest in contributing to Wire! This document provides guidelines and instructions for contributing to the project.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [How to Contribute](#how-to-contribute)
- [Development Setup](#development-setup)
- [Making Changes](#making-changes)
- [Testing](#testing)
- [Submitting Changes](#submitting-changes)
- [Coding Standards](#coding-standards)
- [Documentation](#documentation)
- [Community](#community)

## Code of Conduct

By participating in this project, you agree to abide by our Code of Conduct:

- Be respectful and inclusive
- Welcome newcomers and help them get started
- Focus on constructive criticism
- Accept feedback gracefully
- Prioritize the community's success over personal goals

## Getting Started

1. **Fork the repository** on GitHub
2. **Clone your fork** locally:
   ```bash
   git clone git@github.com:YOUR_USERNAME/wire.git
   cd wire
   ```
3. **Add upstream remote**:
   ```bash
   git remote add upstream git@github.com:wire/wire.git
   ```

## How to Contribute

### Reporting Issues

- Check if the issue already exists
- Use the issue templates when available
- Include relevant information:
  - Wire version
  - Operating system
  - Steps to reproduce
  - Expected vs actual behavior
  - Error messages and logs

### Suggesting Features

- Open a discussion first for major features
- Explain the use case and benefits
- Consider implementation complexity
- Be open to feedback and alternatives

### Contributing Code

We welcome contributions in many forms:

- Bug fixes
- New features
- Performance improvements
- Documentation updates
- Test coverage improvements
- Code refactoring

## Development Setup

### Prerequisites

- Go 1.21 or higher
- Git
- Make (optional but recommended)
- Docker (for integration tests)

### Initial Setup

1. **Install dependencies**:
   ```bash
   go mod download
   ```

2. **Install development tools**:
   ```bash
   # Install golangci-lint
   curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.61.0
   ```

3. **Install Git hooks** (recommended):
   ```bash
   ./scripts/install-hooks.sh
   ```

4. **Verify setup**:
   ```bash
   make test-fast
   ```

## Making Changes

### Branch Naming

Use descriptive branch names:
- `feature/add-s3-sink`
- `fix/memory-leak-pipeline`
- `docs/update-api-reference`
- `refactor/simplify-cluster-logic`

### Development Workflow

1. **Create a feature branch**:
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Make your changes**:
   - Write clean, readable code
   - Follow existing patterns
   - Add/update tests
   - Update documentation

3. **Commit your changes**:
   ```bash
   git add .
   git commit -m "feat: Add new S3 sink connector"
   ```

### Commit Message Format

Follow conventional commits specification:

```
<type>(<scope>): <subject>

<body>

<footer>
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `style`: Code style changes (formatting, etc.)
- `refactor`: Code refactoring
- `test`: Test additions or modifications
- `chore`: Maintenance tasks

Example:
```
feat(sink): Add S3 sink connector

Implements AWS S3 sink with support for:
- Batch uploads
- Compression
- Custom key patterns
- Retry logic

Closes #123
```

### CI/CD Triggers

To optimize CI/CD costs, we use commit message triggers to control when expensive workflows run:

| Trigger | Description | Example |
|---------|-------------|---------|
| `[test]` | Run full test suite with race detection (10 min) | `fix: memory leak [test]` |
| `[build]` | Build binaries for all platforms (10 min) | `feat: new feature [build]` |
| `[release]` | Create a GitHub release | `feat: v1.2.0 [release]` |
| `[security]` | Run security vulnerability scans (5 min) | `chore: update deps [security]` |
| `[skip ci]` | Skip all CI workflows | `docs: fix typo [skip ci]` |

**Notes:**
- Without triggers, only minimal validation runs (3 minutes)
- Multiple triggers can be combined: `[test] [build]`
- For releases, include version: `feat: v1.2.0 release [release]`
- Use `[major]` or `[minor]` with `[release]` for version bumps

## Testing

### Running Tests

```bash
# Quick tests (for pre-commit)
make test-fast

# Full test suite with race detection
make test-full

# Test with coverage report
make test-coverage

# Run specific package tests
go test ./internal/pipeline/...

# Simulate CI locally
make ci-local
```

### Writing Tests

- Write unit tests for all new functionality
- Aim for >80% code coverage
- Use table-driven tests where appropriate
- Mock external dependencies
- Write integration tests for end-to-end flows

Example test structure:
```go
func TestPipelineStart(t *testing.T) {
    tests := []struct {
        name    string
        config  Config
        wantErr bool
    }{
        {
            name:    "valid configuration",
            config:  Config{Name: "test"},
            wantErr: false,
        },
        {
            name:    "missing name",
            config:  Config{},
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

## Submitting Changes

### Pre-submission Checklist

- [ ] Code follows project style guidelines
- [ ] All tests pass locally (`make ci-local`)
- [ ] New tests added for new functionality
- [ ] Documentation updated if needed
- [ ] Commit messages follow convention
- [ ] Branch is up to date with main
- [ ] Consider which CI triggers to use

### Pull Request Process

1. **Update your fork**:
   ```bash
   git fetch upstream
   git rebase upstream/main
   ```

2. **Push your changes**:
   ```bash
   git push origin feature/your-feature-name
   ```

3. **Create Pull Request**:
   - Use a descriptive title
   - Reference related issues
   - Describe what changes were made and why
   - Include testing instructions
   - Add screenshots for UI changes

### Pull Request Template

```markdown
## Description
Brief description of changes

## Related Issue
Fixes #(issue number)

## Type of Change
- [ ] Bug fix
- [ ] New feature
- [ ] Breaking change
- [ ] Documentation update

## Testing
- [ ] Unit tests pass
- [ ] Integration tests pass
- [ ] Manual testing completed

## Checklist
- [ ] Code follows style guidelines
- [ ] Self-review completed
- [ ] Documentation updated
- [ ] Tests added/updated
```

## Coding Standards

### Go Style Guide

Follow the official Go style guide and these project-specific conventions:

- Use `gofmt` for formatting
- Use `golint` for linting
- Use meaningful variable names
- Keep functions small and focused
- Handle errors explicitly
- Add comments for exported functions

### Project Structure

```
wire/
├── cmd/              # Command-line applications
├── internal/         # Private application code
│   ├── pipeline/     # Core pipeline logic
│   ├── service/      # Services (HTTP, Cluster, Store)
│   ├── sources/      # Source implementations
│   ├── sinks/        # Sink implementations
│   └── pkg/          # Shared internal packages
├── pkg/              # Public packages
├── config/           # Configuration examples
├── docs/             # Documentation
└── tests/            # Integration tests
```

### Error Handling

```go
// Good
if err != nil {
    return fmt.Errorf("failed to connect to kafka: %w", err)
}

// Bad
if err != nil {
    return err
}
```

### Testing Conventions

- Test files should be in the same package
- Use descriptive test names
- Mock interfaces, not concrete types
- Use subtests for related test cases

## Documentation

### Code Documentation

- Document all exported types, functions, and packages
- Use complete sentences
- Include examples for complex functionality

```go
// Pipeline represents a data processing pipeline that reads from sources,
// applies transformations, and writes to sinks. It manages the lifecycle
// of all components and ensures graceful shutdown.
type Pipeline struct {
    // ...
}

// Start begins pipeline execution. It initializes all components in order:
// sources, transformers, sinks, and then starts the worker pool.
// Returns an error if any component fails to initialize.
func (p *Pipeline) Start(ctx context.Context) error {
    // ...
}
```

### README Updates

Update README.md when:
- Adding new features
- Changing configuration format
- Adding new connectors
- Modifying installation steps

### API Documentation

- Document all REST endpoints
- Include request/response examples
- Note authentication requirements
- List possible error codes

## Community

### Getting Help

- Check existing documentation
- Search closed issues
- Ask in GitHub Discussions
- Join our community chat

### Staying Updated

- Watch the repository for updates
- Subscribe to release notifications
- Follow project announcements
- Participate in discussions

### Recognition

Contributors are recognized in:
- Release notes
- Contributors file
- Project statistics

## Thank You!

Your contributions make Wire better for everyone. We appreciate your time and effort in improving the project.

If you have questions or need help, don't hesitate to ask in:
- GitHub Issues
- GitHub Discussions
- Community forums