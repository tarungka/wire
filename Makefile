# Variables
BRANCH=$(shell git rev-parse --abbrev-ref HEAD)
COMMIT=$(shell git rev-parse --short HEAD)
DATE=$(shell date +"%Y-%m-%dT%H:%M:%S")
VERSION=$(shell git describe --tags --always --dirty)

# Help command
help:
	@echo "Available commands:"
	@echo "  build -               Build the project"
	@echo "  format -              Format the code"
	@echo "  run -                 Run the project"
	@echo "  test -                Run the integration tests (alias for test-full)"
	@echo "  unittest -            Run unit tests"
	@echo "  test-fast -           Run quick unit tests (for pre-commit)"
	@echo "  test-full -           Run full test suite with race detection"
	@echo "  test-coverage -       Run tests with coverage report"
	@echo "  bench -               Run all benchmarks (3 iterations, 3s each)"
	@echo "  bench-save -          Run benchmarks (5 iterations) and archive to docs/benchmarks/runs/"
	@echo "  profile-cpu -         Capture a CPU profile from the bench suite (cpu.prof)"
	@echo "  profile-mem -         Capture a memory profile from the bench suite (mem.prof)"
	@echo "  trace -               Capture a runtime/trace from a representative bench (trace.out)"
	@echo "  profile-live -        Open a 30s pprof CPU profile from a running coordinator"
	@echo "  build-docker -        Build docker image"
	@echo "  lint -                Run linter"
	@echo "  lint-fast -           Run linter on changed files only"
	@echo "  clean -               Remove build artifacts"
	@echo "  pre-commit -          Run pre-commit checks locally"
	@echo "  ci-local -            Simulate CI checks locally"
	@echo "  ci -                  Run full CI checks (Lint + Test)"

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

GOLANGCI_LINT_VERSION := 2.5.0

lint: check-golangci-lint
	golangci-lint run ./...

check-golangci-lint:
	@if ! command -v golangci-lint > /dev/null || ! golangci-lint version | grep -q "$(GOLANGCI_LINT_VERSION)"; then \
		echo "Required golangci-lint version $(GOLANGCI_LINT_VERSION) not found."; \
		echo "Please install golangci-lint version $(GOLANGCI_LINT_VERSION) with the following command:"; \
		echo "curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.5.0"; \
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

# Alias for test
test: test-full

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
		PACKAGES=$$(echo "$$CHANGED_FILES" | xargs -I{} dirname {} | sort -u | sed 's|^|./|'); \
		golangci-lint run $$PACKAGES; \
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

# Full CI target
ci: lint test-full

# Benchmark suite. Override BENCH or BENCH_PKGS to scope.
#   make bench BENCH=BenchmarkOperatorChain
#   make bench BENCH_PKGS=./internal/keygroup/...
BENCH ?= .
BENCH_PKGS ?= ./...
# Strip framework chatter so bench.out only contains parseable benchstat lines.
# --line-buffered forces grep to flush per line so bench.out grows live.
BENCH_FILTER = grep --line-buffered -E '^(Benchmark|goos|goarch|cpu|pkg|PASS|FAIL|ok |---)'

bench:
	@echo "Running benchmarks: $(BENCH) over $(BENCH_PKGS)"
	go test -run=^$$ -bench=$(BENCH) -benchmem -benchtime=3s -count=3 -timeout=30m $(BENCH_PKGS) 2>/dev/null | $(BENCH_FILTER) | tee bench.out

bench-save:
	@mkdir -p docs/benchmarks/runs
	@OUT="docs/benchmarks/runs/$$(date +%Y%m%d-%H%M%S).txt"; \
	echo "Archiving to $$OUT"; \
	go test -run=^$$ -bench=$(BENCH) -benchmem -benchtime=3s -count=5 -timeout=60m $(BENCH_PKGS) 2>/dev/null | $(BENCH_FILTER) > $$OUT && \
	echo "Saved $$OUT"

profile-cpu:
	go test -run=^$$ -bench=$(BENCH) -benchtime=10s -cpuprofile=cpu.prof $(BENCH_PKGS)
	@echo "Open with: go tool pprof -http=:6060 cpu.prof"

profile-mem:
	go test -run=^$$ -bench=$(BENCH) -benchtime=10s -memprofile=mem.prof $(BENCH_PKGS)
	@echo "Open with: go tool pprof -http=:6060 mem.prof"

trace:
	go test -run=^$$ -bench=BenchmarkOperatorChain_MapPassthrough -benchtime=5s -trace=trace.out ./internal/engine/...
	@echo "Open with: go tool trace trace.out"

DURATION ?= 30
PPROF_HOST ?= http://localhost:4001
profile-live:
	@echo "Capturing $(DURATION)s CPU profile from $(PPROF_HOST)"
	go tool pprof -http=:6060 $(PPROF_HOST)/debug/pprof/profile?seconds=$(DURATION)

# Default target
all: build
