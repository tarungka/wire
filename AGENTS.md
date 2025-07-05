# Wire Agents and Tools Documentation

## Overview

Wire is a real-time data streaming platform designed for efficient data ingestion, transformation, and distribution. It provides a modular architecture with various agents and tools that work together to create powerful data pipelines.

### Core Architecture
- **Pipeline-based processing** with configurable sources and sinks
- **Distributed architecture** with clustering support
- **Multi-protocol support** for various data sources and destinations
- **Real-time data transformation** capabilities

## Main Executable

### Wire CLI (`cmd/wire/main.go`)
The main entry point for the Wire application. Handles initialization, configuration loading, and service orchestration.

**Key responsibilities:**
- Configuration management
- Service initialization (HTTP, Cluster, Store)
- Pipeline lifecycle management
- Graceful shutdown handling

**Usage:**
```bash
wire [flags]
```

## Core Services

### HTTP Service (`internal/service/http/`)
REST API and web interface for Wire management and monitoring.

**Endpoints:**
- Pipeline management (CRUD operations)
- Health checks and metrics
- Configuration management
- Real-time monitoring

**Key files:**
- `server.go` - HTTP server implementation
- `handlers/` - Request handlers for various endpoints
- `middleware/` - Authentication, logging, and other middleware

### Cluster Service (`internal/service/cluster/`)
Distributed coordination service for multi-node Wire deployments.

**Features:**
- Node discovery and registration
- Leader election
- Distributed configuration sync
- Health monitoring across nodes

**Key components:**
- Cluster manager
- Node registry
- Consensus protocol implementation

### Store Service (`internal/service/store/`)
Persistence layer abstraction for Wire's data and metadata.

**Supported backends:**
- SQLite (default for development)
- PostgreSQL (recommended for production)
- MySQL

**Key interfaces:**
- `Store` - Main storage interface
- `Transaction` - Transactional operations
- `Migration` - Database schema management

## Pipeline Architecture

### Pipeline Manager (`internal/pipeline/`)
Core component managing data flow from sources to sinks.

**Components:**
- **Pipeline** - Main orchestrator for data flow
- **Worker Pool** - Concurrent processing of data
- **Transformer** - Data transformation logic
- **Router** - Intelligent data routing between components

**Pipeline lifecycle:**
1. Configuration parsing
2. Source initialization
3. Sink preparation
4. Worker pool creation
5. Data flow orchestration
6. Graceful shutdown

## Data Connectors

### Sources (`internal/sources/`)
Input connectors for data ingestion.

#### Available Sources:
- **FileWatch** - Monitor files for changes
  - Supports multiple file patterns
  - Configurable polling intervals
  - File rotation handling

- **HTTP** - REST API endpoint for data ingestion
  - Webhook support
  - Authentication options
  - Request validation

- **Kafka** - Apache Kafka consumer
  - Topic subscription
  - Consumer group management
  - Offset management

- **RabbitMQ** - AMQP message queue consumer
  - Queue binding
  - Exchange configuration
  - Message acknowledgment

- **SQS** - AWS Simple Queue Service
  - Long polling support
  - Dead letter queue handling
  - Batch processing

- **Webhook** - Incoming webhook receiver
  - Custom endpoint configuration
  - Request authentication
  - Payload validation

### Sinks (`internal/sinks/`)
Output connectors for data distribution.

#### Available Sinks:
- **Elasticsearch** - Search and analytics engine
  - Index management
  - Bulk operations
  - Document mapping

- **File** - File system writer
  - Rotation policies
  - Compression options
  - Custom formatting

- **HTTP** - REST API caller
  - Retry logic
  - Authentication
  - Request batching

- **Kafka** - Apache Kafka producer
  - Topic routing
  - Partitioning strategies
  - Compression

- **MongoDB** - NoSQL database
  - Collection management
  - Bulk operations
  - Index creation

- **PostgreSQL** - Relational database
  - Table management
  - Batch inserts
  - Transaction support

- **Redis** - In-memory data store
  - Key patterns
  - TTL management
  - Pub/sub support

- **S3** - AWS S3 object storage
  - Bucket management
  - Multipart uploads
  - Object tagging

- **Webhook** - Outgoing webhook sender
  - Custom headers
  - Retry policies
  - Response handling

## Network Components

### TCP Multiplexing (`internal/pkg/mux/`)
Efficient network communication layer.

**Features:**
- Connection pooling
- Stream multiplexing
- Protocol negotiation
- Compression support

### Transport Layer (`internal/pkg/transport/`)
Abstraction for various transport protocols.

**Supported protocols:**
- TCP
- HTTP/HTTPS
- WebSocket
- gRPC

## Key Interfaces

### Pipeline Interface
```go
type Pipeline interface {
    Start(ctx context.Context) error
    Stop() error
    GetStatus() Status
    GetMetrics() Metrics
}
```

### Source Interface
```go
type Source interface {
    Connect() error
    Read() (<-chan Message, error)
    Close() error
}
```

### Sink Interface
```go
type Sink interface {
    Connect() error
    Write(messages []Message) error
    Close() error
}
```

### Store Interface
```go
type Store interface {
    Get(key string) ([]byte, error)
    Put(key string, value []byte) error
    Delete(key string) error
    Transaction(fn func(tx Transaction) error) error
}
```

## Background Processes

### Health Monitor
- Periodic health checks for all components
- Automatic recovery attempts
- Alert generation

### Metrics Collector
- Performance metrics gathering
- Resource utilization tracking
- Custom metric support

### Configuration Watcher
- Dynamic configuration updates
- Hot reload support
- Validation before applying changes

## Usage Examples

### Creating a Simple Pipeline
```yaml
name: "file-to-elasticsearch"
source:
  type: "filewatch"
  config:
    path: "/var/log/app.log"
    pattern: "*.log"
sink:
  type: "elasticsearch"
  config:
    url: "http://localhost:9200"
    index: "app-logs"
```

### Multi-Source Pipeline
```yaml
name: "multi-source-aggregator"
sources:
  - type: "kafka"
    config:
      brokers: ["localhost:9092"]
      topic: "events"
  - type: "http"
    config:
      port: 8080
      path: "/ingest"
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

### Cluster Configuration
```yaml
cluster:
  enabled: true
  node_id: "node-1"
  listen_address: ":7946"
  peers:
    - "node-2:7946"
    - "node-3:7946"
```

## Development Guidelines

### Adding New Sources/Sinks
1. Implement the appropriate interface (`Source` or `Sink`)
2. Add configuration struct with validation
3. Register with the factory
4. Add documentation and examples
5. Write comprehensive tests

### Testing Components
- Unit tests for individual components
- Integration tests for end-to-end flows
- Performance benchmarks for critical paths
- Chaos testing for distributed scenarios

### Debugging Tools
- Detailed logging with configurable levels
- Metrics exposition for monitoring
- Debug endpoints for component inspection
- Trace propagation for distributed debugging

## Best Practices

1. **Configuration Management**
   - Use environment variables for sensitive data
   - Validate all configuration before applying
   - Provide sensible defaults

2. **Error Handling**
   - Implement retry logic with backoff
   - Log errors with context
   - Fail fast for unrecoverable errors

3. **Performance Optimization**
   - Use connection pooling
   - Implement batching where applicable
   - Monitor resource usage

4. **Security**
   - Encrypt data in transit
   - Use secure credential storage
   - Implement proper authentication/authorization

## Troubleshooting

### Common Issues
1. **Pipeline not starting**: Check configuration validity and source/sink connectivity
2. **Data loss**: Verify acknowledgment settings and retry policies
3. **Performance degradation**: Monitor metrics and adjust worker pool sizes
4. **Cluster split-brain**: Check network connectivity between nodes

### Debug Commands
```bash
# Check pipeline status
curl http://localhost:8080/api/pipelines

# View cluster members
curl http://localhost:8080/api/cluster/members

# Export metrics
curl http://localhost:8080/metrics
```

## Contributing

When adding new agents or tools:
1. Follow the existing code structure
2. Include comprehensive documentation
3. Add unit and integration tests
4. Update this AGENTS.md file
5. Submit PR with clear description

For more information, see the main project documentation and contribution guidelines.