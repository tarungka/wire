# Wire[UNDER DEVELOPMENT]

Wire is an distributed open source stream processing framework with powerful stream and batch-processing capabilities.

---

To build the application you can either `go build` it on you machine or build a docker image with the following code:

```bash
docker build -t wire:1.0.0 .
```

### Usage

---

Edit the `config.json` and add the necessary sources and sinks.

| Attribute Name | Attribute Description                                              | Required |
| -------------- | ------------------------------------------------------------------ | -------- |
| `name`         | A name given to the source/sink for ease of identification.        | No       |
| `type`         | Type of source or sink.                                            | Yes      |
| `key`          | Unique identifier to map the source to the sink.                   | Yes      |
| `config`       | A JSON config of all the attributes to connect to the source/sink. Please refer to the `.config/` folder for more details. | Yes      |

### Currently supported platforms

| Platform      | Source | Sink | Type            |
| ------------- | ------ | ---- | --------------- |
| MongoDB       | ✔️     | ❌   | `mongodb`       |
| Elasticsearch | ❌     | ✔️   | `elasticsearch` |
| Kafka         | ✔️     | ✔️   | `kafka`         |

### Pipeline Parallelism

Wire now supports configurable parallelism for its data pipelines, allowing for significantly improved throughput and resource utilization. You can configure the number of concurrent processing workers for each pipeline.

**Key Features:**
- **Configurable Parallelism:** Set a specific number of workers per pipeline or let Wire auto-detect based on CPU cores.
- **Worker Pool:** Efficiently manages goroutines for parallel job processing.
- **Adaptive Parallelism (Experimental):** Allows pipelines to dynamically adjust the number of workers based on processing latency to optimize performance.
- **Performance Metrics:** Detailed metrics are available to monitor pipeline and worker pool performance.

**Configuration:**

Pipeline parallelism is configured in your YAML configuration file (e.g., `.config/config.yaml`) under a `pipelines` section. Each entry in this section defines the parallelism settings for a pipeline, identified by its name (which should correspond to the `key` used in your source and sink configurations).

**Example Configuration:**

```yaml
sources:
  - name: Kafka Source
    type: kafka
    key: my_data_pipeline # This key is used to link to pipeline config
    config:
      # ... source specific config
sinks:
  - name: File Sink
    type: file
    key: my_data_pipeline # This key is used to link to pipeline config
    config:
      # ... sink specific config

pipelines:
  - name: "my_data_pipeline"    # Matches the 'key' from source/sink
    parallelism: 8             # Use 8 worker threads
    buffer_size: 5000          # Buffer for jobs waiting for workers

  - name: "another_pipeline"
    parallelism: 0             # Auto-detect CPU cores (runtime.NumCPU())
    buffer_size: 0             # Auto-set based on parallelism (parallelism * 100)
    adaptive:
      enabled: true
      min_workers: 2
      max_workers: 16
      target_latency_ms: 50    # Target average processing latency in milliseconds
      adjust_interval: "30s"   # How often to check and adjust parallelism
```

Refer to the `internal/pipeline/` directory and the main configuration examples for more details on the implementation.
