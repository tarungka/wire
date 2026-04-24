# GPU Acceleration Landscape

Survey of GPU support across the stream processing and data pipeline ecosystem. True GPU-accelerated stream processing is rare -- this represents a significant opportunity for Wire.

---

## Three Patterns of GPU Usage

1. **GPU-accelerated SQL/ETL** -- Using GPUs to speed up joins, aggregations, filtering (vectorized processing). Spark + RAPIDS is the only mature option.
2. **GPU for ML inference inside pipelines** -- Using GPUs within a pipeline to run DL models or heavy math transforms. Flink/Ray/Beam + Triton/PyTorch.
3. **GPU-native databases** -- Ingest streams, query/aggregate on GPU. HEAVY.AI, Kinetica, SQream.

---

## Production-Ready GPU Support

Frameworks with mature, battle-tested GPU acceleration.

| Framework | Mechanism | Operations Accelerated | Maturity |
|---|---|---|---|
| Spark Structured Streaming | RAPIDS Accelerator plugin (cuDF/CUDA) -- no code changes needed. Translates Spark SQL physical plans into GPU-native operations | Joins, GroupBy, Sort, Filter, Project, Shuffle (UCX), Parquet/ORC/CSV I/O | Mature (NVIDIA + Databricks) |
| Databricks | RAPIDS Accelerator on GPU clusters + GPU for ML training/inference | Same as Spark + ML workloads | Mature |
| Ray | GPU-aware actor scheduling (`@ray.remote(num_gpus=1)`); integrations with PyTorch/TF/XGBoost | Anything -- inference, preprocessing, custom CUDA via PyTorch/CuPy | Mature (de facto standard for scaling Python ML) |
| Apache Beam / Google Dataflow | GPU-attached workers on Dataflow; portable runner with GPU Docker containers | ML inference (TFX/TensorFlow Extended), custom transforms | Mature |
| Apache Flink | GPU resource allocation to task slots for UDFs; first-class support for TensorFlow/PyTorch | ML inference, custom CUDA code. Native SQL acceleration is NOT standard | Mature for inference; Experimental for SQL |

## Experimental / Limited GPU Support

| Framework | Status | Details |
|---|---|---|
| ClickHouse | Experimental | Experimental forks/patches with OpenCL/GPU; production relies on CPU SIMD (AVX-512). Not a primary GPU database |
| Bytewax | Emerging | Rust engine with Python bindings; since logic runs in Python, you can use PyTorch/TensorFlow/JAX/CuPy directly within map/stateful_map operators |
| Pathway | Emerging | Rust engine (LLVM compiled); Python interface allows integrating GPU-accelerated libraries for specific steps (vector embeddings, LLM indexing) |
| Snowflake | Limited | Internal hardware acceleration for some AI features; GPU-accelerated SQL is NOT a user-controlled execution mode |

## No Native GPU Support

The vast majority of stream processing tools rely entirely on CPU (often SIMD-optimized):

Kafka Streams, ksqlDB, Apache Storm, Apache Heron, Apache Samza, Hazelcast Jet, Arroyo, RisingWave, Materialize, Fluvio, Tremor, Vector, Redpanda Connect (Benthos), Watermill, Goka, NiFi, Kafka Connect, Debezium, Airbyte, StreamSets, Striim, Timeplus/Proton, Apache Pinot, Apache Druid, AWS Kinesis, Azure Stream Analytics, Confluent Cloud, Temporal.

Note: Arroyo, RisingWave, and Materialize rely on differential dataflow or heavy internal state management and use SIMD (AVX-512) on CPUs for speed, not GPU.

---

## GPU-Native Data Processing Libraries

Frameworks built from the ground up for GPU computation, outside the traditional stream processing world.

| Framework | Description | Maturity |
|---|---|---|
| NVIDIA RAPIDS (cuDF, cuIO, RMM, cuML) | CUDA-based pandas-like DataFrame on GPU; the core GPU data processing library. Virtually all SQL verbs (Join, GroupBy, Rolling Windows, Strings, RegEx) | Mature |
| Dask + cuDF (dask-cudf) | Wraps cuDF for distributed GPU processing; the "Spark" of the GPU-native world. Distributed joins, aggregations, shuffling across multi-GPU nodes | Mature |
| CuPy | GPU ndarray (NumPy-like) with CUDA | Mature |
| BlazingSQL | SQL engine on top of cuDF (CUDA) | Abandoned (team moved to Voltron Data). Modern equivalents: Dask-SQL or Spark RAPIDS |
| DuckDB + GPU | Research/experimental GPU efforts | Experimental |

## GPU-Accelerated Databases

Databases that run queries on GPU and can ingest from streaming sources.

| Database | Mechanism | Key Operations | Maturity |
|---|---|---|---|
| HEAVY.AI (OmniSci/MapD) | GPU-native SQL; compiles queries to CUDA kernels via LLVM; stores data in VRAM (spills to RAM/NVMe) | Massive geospatial joins, server-side rendering of billion-point scatterplots, high-cardinality aggregations | Mature (best for visual analytics) |
| Kinetica | Distributed GPU-accelerated database with vectorized processing | Complex geospatial filtering, window functions, stream ingestion with real-time views | Mature (government/defense/logistics) |
| SQream | GPU-accelerated data warehouse; uses GPUs for compression and join processing | Batch/micro-batch ETL rather than low-latency event streaming | Mature |

## GPU Inference Frameworks

Commonly used as external stages within stream processing pipelines.

| Framework | Description | Maturity |
|---|---|---|
| NVIDIA Triton Inference Server | GPU inference serving; commonly used as an external stage from Flink/Spark/Kafka Streams pipelines | Mature (widely deployed) |
| NVIDIA Morpheus | AI streaming pipeline framework (RAPIDS + cuStreamz + Triton) for cybersecurity/AI: digital fingerprinting, anomaly detection, log parsing with BERT on GPU | Production (niche) |
| TensorRT | GPU inference optimization/runtime | Mature |
| Numba (CUDA) | Write custom CUDA kernels in Python | Mature (requires careful engineering) |

---

## Summary: GPU Support Across All Competitors

| Category | GPU Support | Examples |
|---|---|---|
| Full GPU-accelerated SQL/ETL | Spark + RAPIDS only | Spark Structured Streaming, Databricks |
| GPU for ML inference in pipelines | Several mature options | Flink, Beam/Dataflow, Ray |
| GPU-native databases | Specialized vendors | HEAVY.AI, Kinetica, SQream |
| GPU via Python bindings | Emerging | Bytewax, Pathway (use PyTorch/CuPy in operators) |
| No GPU support | The vast majority | Kafka Streams, Arroyo, RisingWave, Materialize, all Go tools, all ETL tools, all cloud managed services |

---

## Opportunity for Wire

True GPU-accelerated stream processing -- where the engine itself runs operators on GPU -- is extremely rare. Only Spark + RAPIDS achieves this at production scale, and it requires the JVM + Python + CUDA stack.

Key observations:

1. **No non-JVM stream processor has native GPU acceleration.** Arroyo (Rust), RisingWave (Rust), and all Go tools are CPU-only.
2. **Go has viable GPU paths.** Go can call CUDA/OpenCL via cgo bindings, and projects like gorgonia/cu demonstrate GPU compute from Go.
3. **The sweet spot is GPU-accelerated operators** -- joins, aggregations, windowing -- not just GPU for inference. This is what Spark + RAPIDS does, and no one else offers it in a lightweight, non-JVM package.
4. **Streaming + GPU is underserved.** GPU databases (HEAVY.AI, Kinetica) handle queries but not arbitrary streaming DAGs. Spark RAPIDS handles DAGs but requires the Spark/JVM stack.

A Wire implementation with optional GPU-accelerated operators (via CUDA/OpenCL) would be unique in the landscape: a Go-native distributed stream processor with GPU acceleration and no JVM dependency.
