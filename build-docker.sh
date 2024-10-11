#!/bin/bash

# Get the current branch name
BRANCH=$(git rev-parse --abbrev-ref HEAD)

# Get the latest commit hash
COMMIT=$(git rev-parse --short HEAD)

# Get the current date
DATE=$(date +"%Y-%m-%dT%H:%M:%S")

# Get the version: the latest tag or commit hash if no tag is present
VERSION=$(git describe --tags --always --dirty)

# Build the binary with dynamic ldflags
docker build \
    --build-arg VERSION="$VERSION" \
    --build-arg COMMIT="$COMMIT" \
    --build-arg BRANCH="$BRANCH" \
    --build-arg DATE="$DATE" \
    -t wire/wire:latest .
