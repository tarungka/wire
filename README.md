# Wire

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.21-blue.svg)](go.mod)

Wire is a high-performance, distributed stream processing framework designed for real-time data ingestion, transformation, and distribution. Built with Go, it provides a modular architecture with extensive connector support and powerful processing capabilities.

## What Wire Does

Wire helps you:
- Move data between different systems
- Transform data as it flows
- Handle large amounts of data efficiently
- Set up data pipelines with simple configuration files

## Documentation

For detailed documentation, check out:
- **[Technical Documentation](docs/)** - Architecture, design, and roadmaps
- **[AGENTS.md](AGENTS.md)** - Detailed component reference
- **[CONTRIBUTING.md](CONTRIBUTING.md)** - How to contribute

## Quick Start

### Installation

The easiest way to get started:

```bash
# Using Go
go install github.com/wire/wire/cmd/wire@latest

# Or clone and build
git clone https://github.com/wire/wire.git
cd wire
go build -o wire cmd/wire/main.go
```

### Your First Pipeline

1. Create a file called `pipeline.yaml`:

```yaml
name: "my-first-pipeline"
source:
  type: "file"
  config:
    path: "/path/to/input.txt"
sink:
  type: "file"
  config:
    path: "/path/to/output.txt"
```

2. Run Wire:

```bash
./wire --config pipeline.yaml
```

That's it! Wire will read from `input.txt` and write to `output.txt`.

## Available Connectors

### Sources (Where data comes from)
- **file** - Read from files
- **http** - Receive data via HTTP
- **kafka** - Read from Kafka topics
- **mongodb** - Read from MongoDB
- **rabbitmq** - Read from RabbitMQ
- **sqs** - Read from AWS SQS
- **webhook** - Receive webhooks

### Sinks (Where data goes to)
- **file** - Write to files
- **http** - Send via HTTP
- **kafka** - Write to Kafka
- **elasticsearch** - Send to Elasticsearch
- **mongodb** - Write to MongoDB
- **postgresql** - Write to PostgreSQL
- **redis** - Write to Redis
- **s3** - Upload to AWS S3
- **webhook** - Send webhooks

## More Examples

### Reading from HTTP and writing to a database:

```yaml
name: "http-to-database"
source:
  type: "http"
  config:
    port: 8080
    path: "/data"
sink:
  type: "postgresql"
  config:
    connection: "postgres://user:pass@localhost/mydb"
    table: "events"
```

### Reading from Kafka and writing to S3:

```yaml
name: "kafka-to-s3"
source:
  type: "kafka"
  config:
    brokers: ["localhost:9092"]
    topic: "events"
sink:
  type: "s3"
  config:
    bucket: "my-bucket"
    region: "us-east-1"
```

## Development

To work on Wire:

```bash
# Get dependencies
go mod download

# Run tests
make test

# Build
make build
```

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Wire is licensed under the MIT License. See [LICENSE](LICENSE) for details.

## Community

- **Issues**: [Report bugs or request features](https://github.com/wire/wire/issues)
- **Discussions**: [Ask questions and share ideas](https://github.com/wire/wire/discussions)