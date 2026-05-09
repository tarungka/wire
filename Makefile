# Variables
BRANCH=$(shell git rev-parse --abbrev-ref HEAD)
COMMIT=$(shell git rev-parse --short HEAD)
DATE=$(shell date +"%Y-%m-%dT%H:%M:%S")
VERSION=$(shell git describe --tags --always --dirty)

# Packages that participate in benchmarks. The legacy internal/store and
# internal/pipeline packages do not currently compile on master; scope the
# bench targets to packages that are clean so `make bench` is reliable.
BENCH_PKGS ?= ./internal/new/... ./internal/command/... ./internal/cluster/... ./internal/tcp/... ./internal/http/... ./internal/snapshot/... ./internal/db/...

# Help command
help:
	@echo "Available commands:"
	@echo "  build -               Build the project"
	@echo "  format -              Format the code"
	@echo "  run -                 Run the project"
	@echo "  test -                Run the integration tests"
	@echo "  unittest -            Run unit tests"
	@echo "  test-fast -           Run quick unit tests (for pre-commit)"
	@echo "  test-full -           Run full test suite with race detection"
	@echo "  test-coverage -       Run tests with coverage report"
	@echo "  bench -               Run all benchmarks (3 iterations, 3s each)"
	@echo "  bench-save -          Run benchmarks (5 iterations) and archive to docs/benchmarks/runs/"
	@echo "  profile-cpu -         Capture a CPU profile from the bench suite (cpu.prof)"
	@echo "  profile-mem -         Capture a memory profile from the bench suite (mem.prof)"
	@echo "  trace -               Capture a runtime/trace from BenchmarkApply (trace.out)"
	@echo "  profile-live -        Open a 30s pprof CPU profile from a running server"
	@echo "  build-docker -        Build docker image"
	@echo "  lint -                Run linter"
	@echo "  lint-fast -           Run linter on changed files only"
	@echo "  clean -               Remove build artifacts"
	@echo "  pre-commit -          Run pre-commit checks locally"
	@echo "  ci-local -            Simulate CI checks locally"

# Target to build the project
build:
	@echo "Building project..."
	go build -o wire -ldflags=" \
		-w -s \
		-X github.com/tarungka/wire/internal/cmd.CompilerCommand=musl-gcc \
		-X github.com/tarungka/wire/internal/cmd.Version=$(VERSION) \
		-X github.com/tarungka/wire/internal/cmd.Branch=$(BRANCH) \
		-X github.com/tarungka/wire/internal/cmd.Commit=$(COMMIT) \
		-X github.com/tarungka/wire/internal/cmd.Buildtime=$(DATE)" ./cmd/.

GOLANGCI_LINT_VERSION := 1.61.0

lint: check-golangci-lint
	golangci-lint run ./...

check-golangci-lint:
	@if ! command -v golangci-lint > /dev/null || ! golangci-lint version | grep -q "$(GOLANGCI_LINT_VERSION)"; then \
		echo "Required golangci-lint version $(GOLANGCI_LINT_VERSION) not found."; \
		echo "Please install golangci-lint version $(GOLANGCI_LINT_VERSION) with the following command:"; \
		echo "curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.60.1"; \
		exit 1; \
	fi

format:
	go fmt ./...

build-docker:
	docker build --tag wire/wire:latest .

# Target to clean the project build
clean:
	@echo "Cleaning build..."
	rm -f wire

# Quick test for pre-commit hooks
test-fast:
	@echo "Running quick unit tests..."
	go test -short -timeout 30s ./...

# Full test suite with race detection
test-full:
	@echo "Running full test suite with race detection..."
	go test -race -timeout 5m ./...

# Test with coverage report
test-coverage:
	@echo "Running tests with coverage..."
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"
	@go tool cover -func=coverage.out | grep total

# Run unit tests
unittest:
	@echo "Running unit tests..."
	go test -v ./...

# Fast lint for changed files only
lint-fast:
	@echo "Running linter on changed files..."
	@CHANGED_FILES=$$(git diff --name-only --cached | grep "\.go$$" || true); \
	if [ -n "$$CHANGED_FILES" ]; then \
		golangci-lint run $$CHANGED_FILES; \
	else \
		echo "No Go files to lint"; \
	fi

# Pre-commit checks
pre-commit: format lint-fast test-fast
	@echo "✅ Pre-commit checks passed!"

# Simulate CI locally
ci-local:
	@echo "Simulating CI checks locally..."
	@echo "1. Running formatter..."
	@make format
	@echo "2. Running linter..."
	@make lint
	@echo "3. Running tests..."
	@make test-fast
	@echo "✅ Local CI simulation complete!"
	@echo ""
	@echo "💡 To run full tests, use: make test-full"
	@echo "💡 To run security scan, add [security] to commit message"
	@echo "💡 To build binaries, add [build] to commit message"

# Run all benchmarks. Override BENCH_PKGS or BENCH to scope further.
# Examples:
#   make bench BENCH=BenchmarkApply
#   make bench BENCH_PKGS=./internal/new/db/badgerdb/...
BENCH ?= .
# Strip any non-bench framework chatter so bench.out only contains real
# Benchmark*/PASS/ok/header lines that benchstat can parse.
BENCH_FILTER = grep -E '^(Benchmark|goos|goarch|cpu|pkg|PASS|FAIL|ok |---)'
bench:
	@echo "Running benchmarks: $(BENCH) over $(BENCH_PKGS)"
	go test -run=^$$ -bench=$(BENCH) -benchmem -benchtime=3s -count=3 $(BENCH_PKGS) 2>/dev/null | $(BENCH_FILTER) | tee bench.out

# Archive benchmark results into a timestamped file under docs/benchmarks/runs/.
bench-save:
	@mkdir -p docs/benchmarks/runs
	@OUT="docs/benchmarks/runs/$$(date +%Y%m%d-%H%M%S).txt"; \
	echo "Archiving to $$OUT"; \
	go test -run=^$$ -bench=$(BENCH) -benchmem -benchtime=3s -count=5 $(BENCH_PKGS) 2>/dev/null | $(BENCH_FILTER) > $$OUT && \
	echo "Saved $$OUT"

# Capture a CPU profile from the bench suite.
profile-cpu:
	go test -run=^$$ -bench=$(BENCH) -benchtime=10s -cpuprofile=cpu.prof $(BENCH_PKGS)
	@echo "Open with: go tool pprof -http=:6060 cpu.prof"

# Capture a memory profile from the bench suite.
profile-mem:
	go test -run=^$$ -bench=$(BENCH) -benchtime=10s -memprofile=mem.prof $(BENCH_PKGS)
	@echo "Open with: go tool pprof -http=:6060 mem.prof"

# Capture a runtime execution trace from a single representative benchmark.
trace:
	go test -run=^$$ -bench=BenchmarkApply -benchtime=5s -trace=trace.out ./internal/new/store/...
	@echo "Open with: go tool trace trace.out"

# Capture a 30s CPU profile from a running server. Override DURATION or PPROF_HOST.
DURATION ?= 30
PPROF_HOST ?= http://localhost:8081
profile-live:
	@echo "Capturing $(DURATION)s CPU profile from $(PPROF_HOST)"
	go tool pprof -http=:6060 $(PPROF_HOST)/debug/pprof/profile?seconds=$(DURATION)

# Default target
all: build