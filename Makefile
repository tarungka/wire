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
	@echo "  test -                Run the integration tests"
	@echo "  unittest -            Run unit tests"
	@echo "  build-docker -        Build docker image"
	@echo "  lint -                Run linter"
	@echo "  clean -               Remove build artifacts"

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

# Default target
all: build