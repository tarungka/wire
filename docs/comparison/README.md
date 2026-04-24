# Competitive Landscape

Wire's positioning in the stream processing and data pipeline ecosystem.

## Documents

- [**competitors.md**](competitors.md) -- Full competitor landscape across 12 categories, from major stream processing frameworks to iPaaS automation tools. Covers 90+ projects and services.

- [**gpu-support.md**](gpu-support.md) -- GPU acceleration support across the competitive landscape. Covers which competitors support GPU, GPU-native frameworks, and the opportunity for Wire.

## Key Positioning

Wire fills the **"Go-native distributed Flink"** gap. There is no distributed (coordinator/worker) stream processing engine written in Go with checkpointing, exactly-once semantics, and a rich connector ecosystem. Benthos/Redpanda Connect is the closest Go competitor but runs as single processes, not as a distributed cluster. Wire's closest architectural peers are Apache Flink (Java) and Arroyo (Rust).

## Research Sources

Findings synthesized from three AI models (Claude Opus 4.6, GPT-5.2, Gemini 3 Pro) in March 2026, cross-referenced for accuracy and completeness.
