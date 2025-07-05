# Wire

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.21-blue.svg)](go.mod)
[![Build Status](https://img.shields.io/badge/build-passing-brightgreen.svg)]()

Wire is a high-performance, distributed stream processing framework designed for real-time data ingestion, transformation, and distribution. Built with Go, it provides a modular architecture with extensive connector support and powerful processing capabilities.

## Features

- **Real-time Stream Processing**: Process data streams with minimal latency
- **Distributed Architecture**: Scale horizontally with built-in clustering support
- **Extensive Connectors**: Support for multiple data sources and sinks
- **Flexible Pipeline Configuration**: YAML-based configuration for easy pipeline setup
- **High Performance**: Optimized for throughput with concurrent processing
- **Fault Tolerance**: Built-in retry mechanisms and error handling
- **Monitoring & Observability**: Comprehensive metrics and health checks

## Quick Start

### Installation

#### Using Go

```bash
go install github.com/wire/wire/cmd/wire@latest
```

#### Using Docker

```bash
docker pull wire:latest
docker run -v $(pwd)/config:/config wire:latest
```

#### Building from Source

```bash
git clone https://github.com/wire/wire.git
cd wire
go build -o wire cmd/wire/main.go
```

### Basic Usage

1. Create a configuration file `pipeline.yaml`:

```yaml
name: "my-first-pipeline"
source:
  type: "kafka"
  config:
    brokers: ["localhost:9092"]
    topic: "input-events"
    group: "wire-consumer"
sink:
  type: "elasticsearch"
  config:
    url: "http://localhost:9200"
    index: "processed-events"
```

2. Run Wire:

```bash
./wire --config pipeline.yaml
```

## Configuration

Wire uses YAML configuration files to define pipelines. Each pipeline consists of:

- **Sources**: Where data comes from
- **Transformations**: How data is processed (optional)
- **Sinks**: Where data goes

### Configuration Structure

| Field | Description | Required |
|-------|-------------|----------|
| `name` | Pipeline identifier | Yes |
| `source` | Data source configuration | Yes |
| `sink` | Data destination configuration | Yes |
| `transform` | Data transformation rules | No |
| `workers` | Number of concurrent workers | No |

### Example Configurations

#### Multi-Source Pipeline

```yaml
name: "aggregator"
sources:
  - type: "http"
    config:
      port: 8080
      path: "/ingest"
  - type: "sqs"
    config:
      queue_url: "https://sqs.us-east-1.amazonaws.com/123456789/my-queue"
      region: "us-east-1"
sinks:
  - type: "postgresql"
    config:
      connection: "postgres://user:pass@localhost/db"
      table: "events"
  - type: "s3"
    config:
      bucket: "data-archive"
      region: "us-east-1"
```

## Supported Connectors

### Sources

| Source | Type | Description |
|--------|------|-------------|
| FileWatch | `filewatch` | Monitor files for changes |
| HTTP | `http` | REST API endpoint |
| Kafka | `kafka` | Apache Kafka consumer |
| RabbitMQ | `rabbitmq` | AMQP message queue |
| SQS | `sqs` | AWS Simple Queue Service |
| Webhook | `webhook` | Incoming webhooks |
| MongoDB | `mongodb` | MongoDB change streams |

### Sinks

| Sink | Type | Description |
|------|------|-------------|
| Elasticsearch | `elasticsearch` | Search and analytics engine |
| File | `file` | Local or network filesystem |
| HTTP | `http` | REST API calls |
| Kafka | `kafka` | Apache Kafka producer |
| MongoDB | `mongodb` | NoSQL database |
| PostgreSQL | `postgresql` | Relational database |
| Redis | `redis` | In-memory data store |
| S3 | `s3` | AWS S3 object storage |
| Webhook | `webhook` | Outgoing webhooks |

## Architecture

Wire follows a modular, pipeline-based architecture:

```
[Sources] → [Pipeline Manager] → [Worker Pool] → [Transformers] → [Sinks]
                    ↓
             [Store Service]
                    ↓
             [Cluster Service]
```

For detailed architecture documentation, see [AGENTS.md](AGENTS.md).

## Development

### Prerequisites

- Go 1.21 or higher
- Make (optional)
- Docker (for containerized development)

### Building

```bash
# Build binary
make build

# Run tests
make test

# Build Docker image
make docker-build

# Run linting
make lint
```

### Project Structure

```
wire/
├── cmd/wire/          # Main application entry point
├── internal/
│   ├── pipeline/      # Core pipeline logic
│   ├── service/       # HTTP, Cluster, Store services
│   ├── sources/       # Source implementations
│   ├── sinks/         # Sink implementations
│   └── pkg/           # Shared packages
├── config/            # Configuration examples
├── docs/              # Documentation
└── tests/             # Integration tests
```

## Monitoring

Wire exposes metrics in Prometheus format:

```bash
curl http://localhost:8080/metrics
```

Key metrics include:
- Pipeline throughput
- Processing latency
- Error rates
- Resource utilization

## Clustering

Enable clustering for distributed processing:

```yaml
cluster:
  enabled: true
  node_id: "node-1"
  listen_address: ":7946"
  peers:
    - "node-2:7946"
    - "node-3:7946"
```

## Contributing

We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) for details.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## Documentation

- [AGENTS.md](AGENTS.md) - Detailed component documentation
- [API Reference](docs/api.md) - REST API documentation
- [Configuration Guide](docs/configuration.md) - Full configuration reference
- [Deployment Guide](docs/deployment.md) - Production deployment best practices

## Troubleshooting

### Common Issues

1. **Pipeline not starting**: Check configuration syntax and connectivity
2. **High memory usage**: Adjust worker pool size and batch settings
3. **Data loss**: Verify acknowledgment settings in sources/sinks

For more troubleshooting tips, see our [FAQ](docs/faq.md).

## License

Wire is licensed under the MIT License. See [LICENSE](LICENSE) for details.

## Community

- **GitHub Issues**: [Bug reports and feature requests](https://github.com/wire/wire/issues)
- **Discussions**: [Community forum](https://github.com/wire/wire/discussions)
