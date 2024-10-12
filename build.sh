#!/bin/bash

# Get the current branch name
BRANCH=$(git rev-parse --abbrev-ref HEAD)

# Get the latest commit hash
COMMIT=$(git describe --always --dirty --abbrev=7)

# Get the current date
DATE=$(date +"%Y-%m-%dT%H:%M:%S")

# Get the version: the latest tag or commit hash if no tag is present
VERSION=$(git describe --tags --always --dirty)

# Build the binary with dynamic ldflags
go build -o wire -ldflags=" \
    -w -s \
    -X github.com/tarungka/wire/internal/cmd.CompilerCommand=musl-gcc \
    -X github.com/tarungka/wire/internal/cmd.Version=${VERSION} \
    -X github.com/tarungka/wire/internal/cmd.Branch=${BRANCH} \
    -X github.com/tarungka/wire/internal/cmd.Commit=${COMMIT} \
    -X github.com/tarungka/wire/internal/cmd.Buildtime=${DATE}" ./cmd/.
