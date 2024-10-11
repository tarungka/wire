# Build stage
FROM golang:1.23.1-alpine AS builder

ARG VERSION="unknown"
ARG COMMIT="unknown"
ARG BRANCH="unknown"
ARG DATE="unknown"

# Install dependencies
RUN apk add --no-cache \
    curl \
    gcc \
    gettext \
    git \
    icu-dev \
    make \
    musl-dev \
    pkgconf \
    zlib-dev \
    zip

COPY . /app

# Build the application binary
WORKDIR /app
ENV CGO_ENABLED=1
RUN go build -o /wire -ldflags=" \
    -w -s -X github.com/tarungka/wire/internal/cmd.CompilerCommand=musl-gcc \
    -X github.com/tarungka/wire/internal/cmd.Version=${VERSION} \
    -X github.com/tarungka/wire/internal/cmd.Branch=${BRANCH} \
    -X github.com/tarungka/wire/internal/cmd.Commit=${COMMIT} \
    -X github.com/tarungka/wire/internal/cmd.Buildtime=${DATE}" ./cmd/.

#######################################################################
# Final stage - slim image
FROM alpine:latest

# Set up the work directory
WORKDIR /app

RUN apk add --no-cache icu-libs

# Copy only the built binary from the previous stage
COPY --from=builder /app/docker-entrypoint.sh /bin
COPY --from=builder /app/wire /bin

# Copy the config folder
# COPY ./.config ./.config

RUN mkdir -p /badger/file
VOLUME /badger/file
EXPOSE 4001 4001

# Command to run the application with the config file argument
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["run"]

