# Wire Protocol & Serialization Format

> **Feature/Project:** `Wire Protocol & Serialization Format`
>
> **WIP ID:** `WIP-01`
>
> **Author:** `Tarun Ashok`
>
> **Status:** `Draft`
>
> **Created:** `2026-02-22`
>
> **Last Updated:** `2026-02-24`

### Revision History

| Version | Date | Author | Changes |
| -- | -- | -- | -- |
| 0.1 | 2026-02-22 | Tarun Ashok | Initial draft |
| 0.2 | 2026-02-24 | Tarun Ashok | Add Handshake (0x00), CRC32C checksums, RecordBatch (0x06) reservation, StreamID clarification, message type range partitioning. Resolves open questions #1, #3, #5. |

---

## 1. Overview

### 1.1 Problem Statement

Wire nodes communicate over Yamux-multiplexed TCP connections (port 4002) to shuffle data records, propagate checkpoint barriers, advance watermarks, and signal end-of-partition. The codebase already has msgpack encoding utilities (`internal/utils/utils.go`: `EncodeMsgPack`/`DecodeMsgPack` using `hashicorp/go-msgpack/v2`) and a working Yamux multiplexer (`internal/tcp/mux.go`), but there is **no formal wire protocol specification**. Without a spec:

- Developers cannot reason about frame boundaries, message ordering, or version compatibility.
- There is no defined mechanism for distinguishing data records from control messages (barriers, watermarks) on a shared stream.
- Backpressure signaling, corruption detection, and partial-read handling are ad hoc.
- Future cross-language workers or tooling (protocol analyzers, fuzz testers) have nothing to target.

### 1.2 Proposed Solution (Technical Summary)

Define a binary, length-prefixed framing protocol that runs on top of Yamux streams. Every frame carries a 4-byte length prefix, a 1-byte message type discriminator, a 4-byte CRC32C checksum, and an N-byte msgpack-encoded payload. Seven message types cover the control and data plane: `Handshake`, `DataRecord`, `CheckpointBarrier`, `Watermark`, `EndOfPartition`, `Backpressure`, and `RecordBatch` (reserved for future batching). The protocol is designed for zero-copy-friendly reading, minimal allocation, and deterministic parsing.

### 1.3 Goals & Non-Goals

| Goals (In Scope) | Non-Goals (Explicitly Out) |
| -- | -- |
| Define byte-level frame format with length prefix | RPC protocol between Coordinator and Workers (see WIP-07) |
| Specify all message types for data plane communication | Application-level record schema (user payload format) |
| Document msgpack encoding conventions for each message | Compression at the protocol level (deferred) |
| Define error detection for corrupted or truncated frames | Encryption (handled by TLS layer beneath Yamux) |
| Specify backpressure signaling semantics | Multi-language codec implementations |
| Define version negotiation for forward compatibility | Protocol over UDP or QUIC |

### 1.4 Success Metrics

| Metric | Current Baseline | Target | Measurement |
| -- | -- | -- | -- |
| Protocol specification exists | No spec | Complete spec covering all message types | Doc review |
| Frame parsing is unambiguous | Ad hoc | Any developer can implement a parser from the spec alone | Walkthrough test |
| Corruption detected before deserialization | No detection | 100% of truncated/corrupt frames rejected; CRC32C catches all single-bit and burst errors up to 32 bits | Fuzz testing + CRC verification benchmarks |
| CRC32C verification overhead | N/A | < 1% additional latency per frame on hardware with SSE4.2/ARM CRC | Benchmark |
| Throughput overhead from framing | Unmeasured | < 3% overhead vs raw msgpack on 1KB records | Benchmark |

---

## 2. Architecture & System Design

### 2.1 Transport Stack

```
┌──────────────────────────────────────────────────┐
│               Wire Protocol Frames               │  ← This TRD
│  [Length][MsgType][CRC32C][Payload]               │
├──────────────────────────────────────────────────┤
│           Yamux Stream (logical channel)          │  ← Stream multiplexing
│  Per-stream flow control, 1MB window             │
├──────────────────────────────────────────────────┤
│           Yamux Session (per TCP conn)            │  ← Session management
│  Keep-alive 15s, write timeout 10s               │
├──────────────────────────────────────────────────┤
│           TLS 1.3 (optional)                      │  ← Encryption layer
│  Mutual TLS when node-verify-client enabled      │
├──────────────────────────────────────────────────┤
│           TCP (port 4002)                         │  ← Reliable transport
└──────────────────────────────────────────────────┘
```

### 2.2 Yamux Stream Multiplexing

Wire establishes **one TCP connection per worker pair**, managed by the `Mux` struct in `internal/tcp/mux.go`. Each logical data channel (one per upstream-task to downstream-task edge in the ExecutionGraph) maps to a dedicated **Yamux stream** within that connection.

**Stream lifecycle:**

1. **Open:** The upstream task calls `Mux.Dial(addr, timeout)` which reuses an existing Yamux session or creates a new one, then opens a stream via `session.Open()`.
2. **Handshake:** The first frame on a new stream MUST be a `Handshake (0x00)` frame for version and feature negotiation (see Section 3.8). The receiver waits up to 5 seconds for this frame before closing the stream.
3. **Data flow:** Frames are written sequentially. The wire protocol is strictly unidirectional per stream (upstream writes, downstream reads). Control messages (barriers, watermarks) flow inline with data.
4. **Close:** The upstream task closes the stream by sending an `EndOfPartition` frame, then calling `stream.Close()`. The downstream reader observes EOF after processing the final frame.

**Stream assignment:**

```
Worker A                           Worker B
┌──────────┐                      ┌──────────┐
│ Task 0   │──── Stream 1 ──────→│ Task 2   │
│ Task 1   │──── Stream 2 ──────→│ Task 2   │
│ Task 0   │──── Stream 3 ──────→│ Task 3   │
│ Task 1   │──── Stream 4 ──────→│ Task 3   │
└──────────┘                      └──────────┘
         ↑                               ↑
         └── Single TCP connection ──────┘
             (Yamux session)
```

### 2.3 Connection Topology

Wire uses a **partial mesh** topology. Connections are established on demand: Worker A opens a TCP/Yamux session to Worker B only if at least one task on A sends data to a task on B. Within a session, streams are opened per logical channel (per task-to-task edge).

**Connection establishment policy:**

- The **sender** always initiates the connection (calls `Mux.Dial`).
- If a session already exists (checked via `peers` map), a new stream is opened on the existing session.
- If no session exists, a new TCP connection is dialed, Yamux client handshake runs, and the session is stored for reuse.
- The **receiver** accepts streams from any session via `Mux.Accept()`, which returns streams from the shared channel fed by all active sessions.

**Yamux configuration (from `internal/tcp/mux.go`):**

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| `KeepAliveInterval` | 15 seconds | Detect dead peers within ~30s (2 missed keep-alives) |
| `ConnectionWriteTimeout` | 10 seconds | Prevent indefinite blocking on slow peers |
| `MaxStreamWindowSize` | 1 MB (1,048,576 bytes) | Allow sufficient buffering for bursty traffic without unbounded memory growth |

---

## 3. API Design

This is the core of the TRD: the binary wire protocol specification.

### 3.1 Frame Format

Every message transmitted on a Yamux stream is wrapped in a frame with the following layout:

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        Length (4 bytes)                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|  MsgType (1)  |                                               |
+-+-+-+-+-+-+-+-+         CRC32C (4 bytes)                      +
|                               +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+                               |
|                     Payload (N bytes)                          |
|                        (msgpack)                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

| Field | Offset | Size | Encoding | Description |
|-------|--------|------|----------|-------------|
| **Length** | 0 | 4 bytes | Big-endian uint32 | Total number of bytes following this field: `1 + 4 + len(Payload)` = `5 + len(Payload)`. Does **not** include the 4-byte length field itself. Maximum value: 16,777,215 (16 MB - 1). Minimum valid value: 5 (MsgType + CRC32C, zero-length payload). |
| **MsgType** | 4 | 1 byte | uint8 | Message type discriminator. See Section 3.2. |
| **CRC32C** | 5 | 4 bytes | Big-endian uint32 | CRC-32C (Castagnoli) checksum computed over the `MsgType` byte concatenated with the `Payload` bytes. Uses the polynomial `0x1EDC6F41`. Hardware-accelerated via SSE4.2 (x86-64) or CRC instructions (ARM64). Detects all single-bit errors, all double-bit errors, and all burst errors up to 32 bits. **Always active** — every frame includes a valid CRC32C; receivers MUST always verify. |
| **Payload** | 9 | N bytes | msgpack | Message-type-specific payload. Encoded using `hashicorp/go-msgpack/v2` with `codec.MsgpackHandle{}`. Length is `Length - 5` bytes. |

**Total frame size:** `4 + 1 + 4 + N = 9 + N` bytes, where `N = len(Payload)`.

**Reading algorithm (pseudocode):**

```
1. Read exactly 4 bytes → parse as big-endian uint32 → frameLen
2. If frameLen < 5 or frameLen > MAX_FRAME_SIZE: → protocol error, close stream
3. Read exactly frameLen bytes into buffer
4. msgType = buffer[0]
5. crc32c_received = big-endian uint32(buffer[1:5])
6. payload = buffer[5:]
7. crc32c_computed = CRC32C(buffer[0:1] || buffer[5:])   // CRC over MsgType + Payload
8. If crc32c_received != crc32c_computed: → CRC mismatch error, drop frame (see Section 6.2)
9. Dispatch on msgType, decode payload via DecodeMsgPack(payload, &msg)
```

### 3.2 Message Types

| MsgType | Value | Direction | Description |
|---------|-------|-----------|-------------|
| `Handshake` | `0x00` | Bidirectional | Protocol version and feature negotiation. Must be the first frame on every new stream. See Section 3.8. |
| `DataRecord` | `0x01` | Upstream → Downstream | A user data record flowing through the pipeline |
| `CheckpointBarrier` | `0x02` | Upstream → Downstream | Checkpoint barrier dividing epochs (see execution-model.md Section 5) |
| `Watermark` | `0x03` | Upstream → Downstream | Watermark advancement notification |
| `EndOfPartition` | `0x04` | Upstream → Downstream | Signals that the upstream partition is exhausted (bounded sources) |
| `Backpressure` | `0x05` | Downstream → Upstream | Explicit backpressure signal (supplements Yamux flow control) |
| `RecordBatch` | `0x06` | Upstream → Downstream | **Reserved.** Batch of DataRecords in a single frame. See Section 3.9. |

**Message type range allocation:**

| Range | Purpose |
|-------|---------|
| `0x00` | Handshake (this protocol) |
| `0x01`-`0x05` | Core protocol messages (this protocol) |
| `0x06` | RecordBatch (reserved, see Section 3.9) |
| `0x07`-`0x3F` | Reserved for future core protocol extensions |
| `0x40`-`0x7F` | Reserved for user-defined / experimental extensions |
| `0x80`-`0xFF` | Reserved (must not be used) |

A receiver encountering an unknown message type MUST skip the frame (it already knows the length from the Length field) and log a warning, rather than terminating the connection. This enables forward compatibility: older receivers gracefully ignore message types added in newer protocol versions.

User-defined extensions in the `0x40`-`0x7F` range allow custom message types for domain-specific use cases without risking collision with future core protocol types. These extensions are not subject to cross-implementation compatibility guarantees.

### 3.3 Message Type: DataRecord (0x01)

The primary data-carrying message. Each DataRecord represents a single event flowing through the stream processing pipeline.

**Payload structure (msgpack map):**

| Field | msgpack Key | Type | Required | Description |
|-------|-------------|------|----------|-------------|
| **Key** | `"k"` | `bin` (bytes) | No | Partition key. `nil` for unkeyed streams. Used for hash-based partitioning in `KeyBy` shuffles. |
| **Value** | `"v"` | `bin` (bytes) | Yes | The event payload. Opaque bytes; serialization format is user-defined. |
| **EventTime** | `"t"` | `int64` | Yes | Event timestamp in Unix milliseconds. Used for watermark tracking and window assignment. |
| **Headers** | `"h"` | `map[str]bin` | No | Optional key-value metadata. Omitted from the msgpack encoding when empty (not present in the map, not encoded as an empty map). |

**Compact msgpack key rationale:** Single-character keys minimize per-record overhead. At 100K records/sec, saving 10 bytes per key name saves ~1 MB/sec of bandwidth per stream.

**Example encoding (hex):**

```
Frame:
  Length:   00 00 00 23              (35 bytes follow: 1 MsgType + 4 CRC32C + 30 payload)
  MsgType:  01                       (DataRecord)
  CRC32C:   xx xx xx xx              (CRC32C over MsgType + Payload)
  Payload (30 bytes, msgpack map with 4 entries):
    84                               (fixmap, 4 entries)
    A1 6B                            (fixstr "k")
    C4 04 75 73 72 31               (bin8, 4 bytes: "usr1")
    A1 76                            (fixstr "v")
    C4 08 7B 22 61 22 3A 31 7D 0A  (bin8, 8 bytes: {"a":1}\n)
    A1 74                            (fixstr "t")
    D3 00 00 01 8E 5A 3C D4 00     (int64: 1708819200000 = 2024-02-25T00:00:00Z)
    A1 68                            (fixstr "h")
    80                               (fixmap, 0 entries - empty headers)
```

**Go struct for codec:**

```go
type DataRecordMsg struct {
    Key       []byte            `codec:"k"`
    Value     []byte            `codec:"v"`
    EventTime int64             `codec:"t"`
    Headers   map[string][]byte `codec:"h,omitempty"`
}
```

### 3.4 Message Type: CheckpointBarrier (0x02)

A control message injected by source operators when the Coordinator triggers a checkpoint. Barriers flow inline with data records and divide the stream into epochs. See `execution-model.md` Section 5 for the Asynchronous Barrier Snapshot algorithm.

**Payload structure (msgpack map):**

| Field | msgpack Key | Type | Required | Description |
|-------|-------------|------|----------|-------------|
| **CheckpointID** | `"c"` | `uint64` | Yes | Monotonically increasing checkpoint identifier. Assigned by the Coordinator. |
| **EpochID** | `"e"` | `uint64` | Yes | Epoch number. `EpochID == CheckpointID` in the normal case. Records before this barrier belong to epoch `EpochID`; records after belong to `EpochID + 1`. |
| **Timestamp** | `"ts"` | `int64` | Yes | Wall-clock time (Unix milliseconds) when the Coordinator triggered this checkpoint. Used for checkpoint duration tracking, not for event-time semantics. |

**Invariants:**
- Barriers are **never reordered** relative to data records on a given stream. If record R was emitted before barrier B, R must be delivered before B.
- An operator with multiple input streams must perform **barrier alignment** before snapshotting state (see execution-model.md Section 5.2).
- CheckpointIDs are globally unique and strictly increasing.

**Go struct:**

```go
type CheckpointBarrierMsg struct {
    CheckpointID uint64 `codec:"c"`
    EpochID      uint64 `codec:"e"`
    Timestamp    int64  `codec:"ts"`
}
```

**Example frame (hex):**

```
Frame:
  Length:   00 00 00 16              (22 bytes follow: 1 MsgType + 4 CRC32C + 17 payload)
  MsgType:  02                       (CheckpointBarrier)
  CRC32C:   xx xx xx xx              (CRC32C over MsgType + Payload)
  Payload (17 bytes, msgpack map with 3 entries):
    83                               (fixmap, 3 entries)
    A1 63                            (fixstr "c")
    CF 00 00 00 00 00 00 00 2A     (uint64: 42)
    A1 65                            (fixstr "e")
    CF 00 00 00 00 00 00 00 2A     (uint64: 42)
    A2 74 73                         (fixstr "ts")
    D3 00 00 01 8E 5A 3C D4 00     (int64: 1708819200000)
```

### 3.5 Message Type: Watermark (0x03)

Declares that no further events with `EventTime < Timestamp` will arrive on this stream. Operators propagate the minimum watermark across all inputs (see execution-model.md Section 2.2).

**Payload structure (msgpack map):**

| Field | msgpack Key | Type | Required | Description |
|-------|-------------|------|----------|-------------|
| **Timestamp** | `"t"` | `int64` | Yes | Watermark value in Unix milliseconds. All future events on this stream will have `EventTime >= Timestamp`. |
| **SourceID** | `"s"` | `str` | Yes | Identifier of the source operator or subtask that generated this watermark. Format: `"{operator_name}-{subtask_index}"` (e.g., `"api-source-0"`). Used for debugging and multi-input watermark tracking. |

**Go struct:**

```go
type WatermarkMsg struct {
    Timestamp int64  `codec:"t"`
    SourceID  string `codec:"s"`
}
```

**Watermark progression rules:**
1. Watermarks are monotonically non-decreasing per stream. A watermark with `Timestamp < previous_watermark.Timestamp` on the same stream is a protocol violation and MUST be dropped with a warning.
2. A special sentinel value of `math.MaxInt64` (`9223372036854775807`) represents the "end-of-time" watermark, meaning no more events will ever arrive.

### 3.6 Message Type: EndOfPartition (0x04)

Signals that the upstream operator has finished producing records for this stream. Sent by bounded sources (e.g., file sources) when input is exhausted, or by any operator when the job is canceling gracefully.

**Payload structure (msgpack map):**

| Field | msgpack Key | Type | Required | Description |
|-------|-------------|------|----------|-------------|
| **SourceID** | `"s"` | `str` | Yes | Identifier of the upstream operator/subtask. |
| **Reason** | `"r"` | `uint8` | Yes | Reason code: `0x00` = input exhausted (normal), `0x01` = job canceling, `0x02` = error. |

**Go struct:**

```go
type EndOfPartitionMsg struct {
    SourceID string `codec:"s"`
    Reason   uint8  `codec:"r"`
}

const (
    EndReasonExhausted uint8 = 0x00
    EndReasonCanceling uint8 = 0x01
    EndReasonError     uint8 = 0x02
)
```

**Semantics:**
- After sending `EndOfPartition`, the upstream MUST NOT send any further frames on this stream.
- The downstream operator should treat this stream as closed. Once all input streams have received `EndOfPartition`, the operator can finalize its own output and propagate `EndOfPartition` downstream.
- The stream is closed at the Yamux level after this frame is sent.

### 3.7 Message Type: Backpressure (0x05)

An explicit backpressure signal sent from the downstream operator to the upstream operator. This supplements Yamux's built-in window-based flow control with application-level semantics, allowing the upstream to proactively reduce its send rate rather than blocking on TCP writes.

**Payload structure (msgpack map):**

| Field | msgpack Key | Type | Required | Description |
|-------|-------------|------|----------|-------------|
| **StreamID** | `"id"` | `uint32` | Yes | Identifies which logical stream this backpressure applies to (the Yamux stream ID). |
| **State** | `"st"` | `uint8` | Yes | `0x00` = resume (backpressure lifted), `0x01` = pause (apply backpressure). |
| **BufferUsage** | `"bu"` | `float32` | No | Current downstream buffer utilization as a fraction `[0.0, 1.0]`. Informational; allows the upstream to implement graduated rate limiting. |

**Go struct:**

```go
type BackpressureMsg struct {
    StreamID    uint32  `codec:"id"`
    State       uint8   `codec:"st"`
    BufferUsage float32 `codec:"bu,omitempty"`
}

const (
    BackpressureResume uint8 = 0x00
    BackpressurePause  uint8 = 0x01
)
```

**StreamID semantics:** The `StreamID` field MUST reference the Yamux stream ID as seen by the **sender of the Backpressure message** (the downstream node). Yamux uses distinct ID spaces for client-initiated vs server-initiated streams: client-initiated stream IDs are odd, server-initiated stream IDs are even. Since data streams are always initiated by the upstream (client), the downstream (server) sees them with the client-assigned (odd) stream IDs. Both sides must use consistent stream ID mapping. The upstream receiving a Backpressure message matches `StreamID` against its own record of opened streams.

**Flow control interaction:**
- Yamux provides **transport-level** flow control via stream windows (1 MB default). When a receiver's buffer fills, Yamux stops granting window credit, which blocks the sender's `Write()` call.
- The `Backpressure` message provides **application-level** flow control. The downstream can send a `Pause` signal before its buffer is completely full (e.g., at 80% capacity), giving the upstream time to slow down gracefully rather than hard-blocking.
- `Backpressure` messages travel on a **dedicated control stream** (Yamux stream opened specifically for control traffic on each session), not on the data streams themselves. This prevents head-of-line blocking where a backpressure signal would be stuck behind a queue of data frames.

### 3.8 Message Type: Handshake (0x00)

The first frame sent on any newly opened data stream MUST be a `Handshake` frame. This dedicated message type cleanly separates connection negotiation from data processing, avoiding the brittleness of in-band magic keys.

**Payload structure (msgpack map):**

| Field | msgpack Key | Type | Required | Description |
|-------|-------------|------|----------|-------------|
| **ProtocolVersion** | `"v"` | `uint16` | Yes | Protocol version offered by the sender. Current version: `1`. |
| **MinVersion** | `"min_v"` | `uint16` | Yes | Minimum protocol version the sender supports. Current: `1`. |
| **Features** | `"f"` | `uint32` | No | Bitmask of feature flags. Bit 0: CRC32C (reserved — CRC32C is always active, see Section 3.1; this bit exists for forward compatibility). Bit 1: LZ4 compression (reserved). Bits 2-31: reserved (must be 0). Omitted if no optional features are requested. |

**Go struct:**

```go
type HandshakeMsg struct {
    ProtocolVersion uint16 `codec:"v"`
    MinVersion      uint16 `codec:"min_v"`
    Features        uint32 `codec:"f,omitempty"`
}

const (
    FeatureCRC32C      uint32 = 1 << 0
    FeatureCompression uint32 = 1 << 1
)
```

**Negotiation rules:**

1. The initiator (upstream/sender) sends a `Handshake` frame as the very first frame on a new stream.
2. The receiver validates version compatibility: if `sender.ProtocolVersion < receiver.MinVersion` or `receiver.ProtocolVersion < sender.MinVersion`, the versions are incompatible. The receiver closes the stream with `EndOfPartition(Reason=Error)`.
3. The effective protocol version is `min(sender.ProtocolVersion, receiver.ProtocolVersion)`.
4. Feature flags are negotiated by bitwise AND: `active = sender.Features & receiver.Features`. This applies to future optional features (e.g., LZ4 compression). **CRC32C checksums are always active** — the CRC32C field in every frame header is always computed and verified regardless of the negotiated feature set. Bit 0 (`FeatureCRC32C`) is reserved for forward compatibility; implementations MUST NOT treat CRC32C as optional.
5. A receiver that does not receive a `Handshake` frame within 5 seconds of stream open MUST close the stream.
6. If the first frame on a stream has a `MsgType` other than `0x00`, the receiver MUST close the stream immediately (protocol violation).

**Example frame (hex):**

```
Frame:
  Length:   00 00 00 0D              (13 bytes follow: 1 MsgType + 4 CRC32C + 8 payload)
  MsgType:  00                       (Handshake)
  CRC32C:   xx xx xx xx              (CRC32C over MsgType + Payload)
  Payload (8 bytes, msgpack map with 2 entries):
    82                               (fixmap, 2 entries)
    A1 76                            (fixstr "v")
    CD 00 01                         (uint16: 1)
    A5 6D 69 6E 5F 76               (fixstr "min_v")
    CD 00 01                         (uint16: 1)
```

### 3.9 Message Type: RecordBatch (0x06) -- Reserved

`RecordBatch` is reserved for a future protocol version that supports batching multiple `DataRecord` payloads into a single frame. This section documents the planned structure to ensure forward compatibility; implementations MUST NOT send `RecordBatch` frames until a future WIP formally specifies the semantics.

**Planned payload structure (msgpack map):**

| Field | msgpack Key | Type | Required | Description |
|-------|-------------|------|----------|-------------|
| **RecordCount** | `"n"` | `uint32` | Yes | Number of DataRecord entries in this batch. |
| **Records** | `"rs"` | `array` | Yes | Array of DataRecord payloads. Each element is a msgpack map with the same schema as Section 3.3 (`"k"`, `"v"`, `"t"`, `"h"`). |

**Planned Go struct:**

```go
type RecordBatchMsg struct {
    RecordCount uint32           `codec:"n"`
    Records     []DataRecordMsg  `codec:"rs"`
}
```

**Design notes:**
- Batching amortizes per-frame overhead (9 bytes header + msgpack map overhead) across N records. For 100-byte records batched in groups of 64, header overhead drops from ~9% to ~0.14%.
- Ordering semantics: records within a batch are ordered by their array index. A batch is atomic for framing purposes but individual records are processed sequentially.
- Checkpoint barriers MUST NOT appear inside a batch. A barrier must be its own frame, ensuring clean epoch boundaries.
- Implementation is deferred to a future WIP. Receivers encountering `MsgType=0x06` before that WIP is ratified MUST skip the frame per the unknown-type rule in Section 3.2.

---

## 4. Data Model

### 4.1 Byte-Level Frame Layout

**Minimum valid frame (EndOfPartition with minimal payload):**

```
Offset  Hex                          Field
------  ---------------------------  -----------
0x00    00 00 00 0F                  Length = 15 (1 MsgType + 4 CRC32C + 10 payload)
0x04    04                           MsgType = EndOfPartition
0x05    xx xx xx xx                  CRC32C (over MsgType + Payload)
0x09    82                           fixmap(2)
0x0A    A1 73                        fixstr "s"
0x0C    A3 73 2D 30                  fixstr "s-0"
0x0F    A1 72                        fixstr "r"
0x11    00                           uint8 0x00
                                     Total: 18 bytes on wire (9 header + 10 payload)
```

**Typical DataRecord frame (~100 byte payload):**

```
Offset  Hex                          Field
------  ---------------------------  -----------
0x00    00 00 00 72                  Length = 114 (1 MsgType + 4 CRC32C + 109 payload)
0x04    01                           MsgType = DataRecord
0x05    xx xx xx xx                  CRC32C (over MsgType + Payload)
0x09    83                           fixmap(3) [Key, Value, EventTime]
0x0A    A1 6B                        fixstr "k"
0x0C    C4 10 ...                    bin8(16): partition key (16-byte hash)
0x1E    A1 76                        fixstr "v"
0x20    C5 00 50 ...                 bin16(80): event payload (80 bytes)
0x72    A1 74                        fixstr "t"
0x74    D3 xx xx xx xx xx xx xx xx   int64: event time
                                     Total: 119 bytes on wire (9 header + 110 payload)
```

### 4.2 Maximum Frame Size

The `Length` field is 4 bytes (uint32), allowing a theoretical maximum of ~4 GB. However, the protocol enforces a **configurable maximum frame size** to prevent memory exhaustion. The minimum valid `Length` value is `5` (1 byte MsgType + 4 bytes CRC32C + 0 bytes payload).

| Configuration | Default | Range | Description |
|---------------|---------|-------|-------------|
| `wire.protocol.max_frame_size` | 16 MB (`16777216`) | 1 KB - 256 MB | Maximum allowed value for the `Length` field |

Frames with `Length < 5` or exceeding the configured limit are rejected with a protocol error, and the stream is closed.

### 4.3 Byte Order

All multi-byte integer fields in the frame header (i.e., the `Length` and `CRC32C` fields) use **big-endian** (network byte order) encoding, consistent with `binary.BigEndian` as used in `internal/utils/utils.go` (`ConvertUint64ToBytes`). Payload fields are encoded by msgpack, which has its own endianness rules (big-endian for integers).

### 4.4 Message Ordering on a Stream

Messages on a single Yamux stream obey **strict total order**. The following ordering constraints apply:

1. **DataRecords** appear in the order they were emitted by the upstream operator.
2. **CheckpointBarrier(N)** appears after all DataRecords belonging to epoch N and before all DataRecords belonging to epoch N+1.
3. **Watermark(T)** appears after all DataRecords with `EventTime < T` (within the bounds of the watermark strategy's allowed out-of-orderness).
4. **EndOfPartition** is always the last message on a stream.

```
Stream timeline:
  Handshake → DataRecord → DataRecord → Watermark(100) → DataRecord → CheckpointBarrier(1)
  → DataRecord → DataRecord → Watermark(200) → CheckpointBarrier(2)
  → DataRecord → EndOfPartition
```

---

## 5. Design Decisions & Trade-offs

### Decision 1: msgpack for payload serialization (not Protocol Buffers, not FlatBuffers)

|  |  |
| -- | -- |
| **Context** | Need a serialization format for encoding message payloads within frames. The format must be fast, compact, and easy to use from Go without code generation. |
| **Options Considered** | (A) msgpack, (B) Protocol Buffers (protobuf), (C) FlatBuffers, (D) JSON, (E) CBOR |
| **Decision** | Option A: msgpack |
| **Rationale** | (1) Already in use: `hashicorp/go-msgpack/v2` is a dependency and `EncodeMsgPack`/`DecodeMsgPack` are implemented in `internal/utils/utils.go`. Zero new dependencies. (2) Schema-less: no `.proto` files to maintain, no code generation step. Payloads are plain Go structs with codec tags. (3) Compact: msgpack is typically 15-30% smaller than JSON and comparable to protobuf for small messages. (4) Fast: the hashicorp codec is well-optimized and avoids reflection for registered types. (5) Debuggable: msgpack can be inspected with standard tools (`msgpack-inspect`, Python `msgpack` library). |
| **Options Rejected** | Protobuf: requires `.proto` files and code generation. Adds build complexity. FlatBuffers: zero-copy reads are attractive but add significant complexity and require schema files. JSON: too verbose for high-throughput binary data (2-3x overhead). CBOR: similar to msgpack but less ecosystem support in Go. |
| **Trade-offs Accepted** | No static schema enforcement (typos in codec tags cause silent failures). No built-in schema evolution rules (protobuf has field numbering). We mitigate by keeping payloads small (3-5 fields) with extensive tests. |
| **Revisit Trigger** | If Wire adds cross-language workers (Python/Rust), protobuf with shared `.proto` files may be preferable. If zero-copy performance matters (records > 1 MB), FlatBuffers should be re-evaluated. |

### Decision 2: Length-prefixed framing (not delimiter-based, not fixed-size)

|  |  |
| -- | -- |
| **Context** | Need a mechanism to delineate message boundaries on a byte-stream transport (TCP/Yamux). |
| **Options Considered** | (A) Length prefix (4 bytes), (B) Delimiter-based (e.g., newline), (C) Fixed-size frames, (D) Self-describing (msgpack streaming) |
| **Decision** | Option A: 4-byte length prefix |
| **Rationale** | (1) Simplicity: the reader knows exactly how many bytes to read before parsing begins, enabling `io.ReadFull` in a single call. (2) Binary-safe: no escaping needed for payloads containing arbitrary bytes (which msgpack inherently produces). (3) Bounded reads: the reader can pre-allocate a buffer of exactly the right size, avoiding incremental scanning. (4) Widely adopted: gRPC (HTTP/2 DATA frames) and many message protocols use length-prefixed framing. |
| **Options Rejected** | Delimiter-based: requires escaping binary payloads, adds complexity, O(n) scanning. Fixed-size: wastes space for small messages, truncates large ones. Self-describing msgpack: would work but requires the msgpack decoder to do its own framing, making it harder to skip unknown message types. |
| **Trade-offs Accepted** | 8 bytes of fixed overhead per frame (4 bytes length + 4 bytes CRC32C, with the 1-byte MsgType included in the Length). For small messages (< 20 bytes), this is ~40% overhead. Acceptable because CRC32C is hardware-accelerated (SSE4.2/ARM) and even at 1M messages/sec, the overhead is only ~8 MB/sec. |
| **Revisit Trigger** | If Wire needs to support streaming records larger than 256 MB, chunked framing should be added. |

### Decision 3: Inline control messages (not out-of-band)

|  |  |
| -- | -- |
| **Context** | Checkpoint barriers and watermarks must be ordered relative to data records. They could be sent inline (same stream) or on a separate control channel. |
| **Options Considered** | (A) Inline on data streams with MsgType discriminator, (B) Separate control stream per task pair, (C) Separate control TCP connection |
| **Decision** | Option A: Inline on data streams |
| **Rationale** | (1) Ordering guarantee: barriers MUST appear at precise positions in the data stream (between epoch N and epoch N+1). Inline delivery provides this naturally. A separate channel would require sequence numbers and synchronization. (2) Simplicity: one stream to read, one parsing loop. (3) Yamux already provides stream multiplexing, so adding more streams adds session management overhead without benefit. |
| **Trade-offs Accepted** | A large data record being written can delay a barrier's transmission. Mitigated by the fact that individual frames are bounded (16 MB max) and Yamux provides per-stream flow control. |
| **Exception** | `Backpressure` messages use a separate control stream (see Section 3.7) because they flow in the reverse direction and must not be blocked by data queue buildup. |
| **Revisit Trigger** | If barrier latency becomes a bottleneck for checkpoint performance. |

### Decision 4: Single-byte message type (not varint, not multi-byte)

|  |  |
| -- | -- |
| **Context** | Need to discriminate between message types within the framing layer. |
| **Options Considered** | (A) Fixed 1-byte type field, (B) Varint-encoded type, (C) Include type in the msgpack payload |
| **Decision** | Option A: Fixed 1 byte |
| **Rationale** | 256 possible message types is far more than Wire will ever need. A fixed-size field means the CRC32C always starts at byte offset 5 and the payload always starts at byte offset 9, enabling constant-time access without varint decoding. |
| **Trade-offs Accepted** | Cannot exceed 256 message types. This is not a realistic concern. |

---

## 6. Edge Cases & Failure Modes

### 6.1 Partial Reads

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | TCP connection drops mid-frame (partial length field) | `io.ReadFull` returns `io.ErrUnexpectedEOF`. Reader closes the Yamux stream. Task manager detects stream loss and triggers task failure/recovery. | Task restarts from last checkpoint | Medium |
| 2 | Yamux stream reset mid-frame | Same as above. Yamux delivers a stream reset error to the reader's `Read()` call. | Task restarts from last checkpoint | Medium |
| 3 | Partial payload (length says 100 bytes, only 50 arrive before timeout) | `io.ReadFull` with a deadline returns `os.ErrDeadlineExceeded`. Reader treats as a failed stream. | Task restarts from last checkpoint | Medium |

**Mitigation:** All reads use `io.ReadFull` with explicit deadlines. Partial frames are never processed. The checkpoint/recovery mechanism ensures no data loss.

### 6.2 Corrupted Frames

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 4 | Length field is unreasonably large (> `max_frame_size`) | Reader rejects immediately, logs error with remote address and reported length, closes stream. | Stream closed; task may restart | Medium |
| 5 | Length field is < 5 (below minimum valid: 1 MsgType + 4 CRC32C) | Protocol violation. Reader closes stream. | Stream closed | Low |
| 5a | CRC32C mismatch (frame intact but bits flipped in transit or memory) | Reader computes CRC32C over received MsgType + Payload and compares with header CRC32C. On mismatch: log error with frame offset, hex dump of first 64 bytes, expected vs actual CRC. Frame is dropped. Stream remains open. After 10 consecutive CRC mismatches, the stream is closed (indicates persistent corruption or a buggy sender). | Records lost (recovered at next checkpoint) | Medium |
| 6 | MsgType is unknown (not in allocated ranges) | Reader skips the frame (it knows the payload length) and logs a warning. Does NOT close the stream. This enables forward compatibility. | Frame skipped | Low |
| 7 | Payload is valid length, CRC32C matches, but msgpack decoding fails | Reader logs the error with a hex dump of the first 64 bytes of the payload for debugging. Frame is dropped. Stream remains open (transient corruption should not kill a long-running stream). After 10 consecutive decode failures, the stream is closed. | Records lost (recovered at next checkpoint) | Medium |

### 6.3 Flow Control Scenarios

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 8 | Downstream buffer at 80% capacity | Downstream sends `Backpressure(State=Pause)` on the control stream. Upstream reduces send rate by sleeping between frames. | Graceful slowdown | Low |
| 9 | Downstream buffer drains below 20% | Downstream sends `Backpressure(State=Resume)`. Upstream resumes full-speed sending. | Normal operation restored | Low |
| 10 | Upstream ignores Backpressure signal | Yamux window-based flow control is the hard backstop. When the receiver stops reading, Yamux stops granting window credit. Upstream's `Write()` blocks. | Upstream goroutine blocks (backpressure still works) | Low |
| 11 | Backpressure control stream itself is congested | The control stream has minimal traffic (small messages, infrequent). If it blocks, the worst case is that the data stream hits the Yamux hard limit instead of the soft application limit. | Slightly less graceful backpressure | Low |

### 6.4 Protocol Violation Scenarios

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 12 | DataRecord sent after EndOfPartition | Receiver drops the record and logs a protocol violation warning. | Record silently dropped | Low |
| 13 | Watermark goes backward (timestamp < previous watermark) | Receiver drops the watermark and logs a warning. Watermark state is not regressed. | Stale watermark ignored | Low |
| 14 | CheckpointBarrier with ID <= last completed checkpoint | Receiver drops the barrier. This can happen during recovery when old messages are replayed. | Duplicate barrier ignored | Low |
| 15 | Handshake (0x00) missing on new stream | Receiver waits up to 5 seconds for a `Handshake` frame. If the first frame is not `MsgType=0x00`, or no frame arrives within 5 seconds, the receiver closes the stream. | Stream rejected | Medium |

---

## 7. Security & Compliance

### 7.1 TLS Wrapping

The wire protocol runs on top of TCP, which can optionally be wrapped in TLS. When TLS is enabled (via `--node-cert` and `--node-key` flags), the entire Yamux session (and therefore all streams and wire protocol frames) are encrypted.

**TLS configuration (from `internal/tcp/mux.go: NewTLSMux`):**

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| `MinVersion` | TLS 1.3 | TLS 1.2 and below have known weaknesses. TLS 1.3 is faster (1-RTT handshake) and more secure. |
| `ClientAuth` | `RequireAndVerifyClientCert` (when `--node-verify-client` is set) | Mutual TLS ensures both ends are authenticated cluster members. |

**Security properties when TLS is enabled:**
- **Confidentiality:** All wire protocol frames are encrypted. An eavesdropper sees only TLS records.
- **Integrity:** TLS AEAD ciphers provide per-record authentication. Tampered frames are rejected by the TLS layer before reaching the wire protocol parser.
- **Authentication:** Mutual TLS ensures that only nodes with valid certificates can establish connections.

**Security properties when TLS is NOT enabled:**
- Wire protocol frames are transmitted in plaintext over TCP.
- Any network observer can read data records, checkpoint barriers, and all metadata.
- This mode is intended for development and trusted-network deployments only.

**Note on CRC32C and TLS:** CRC32C and TLS serve different purposes. CRC32C detects corruption that occurs *before* TLS encryption (e.g., memory bit flips in the sender, kernel buffer corruption) or *after* TLS decryption. TLS AEAD provides integrity over the wire. Both layers are complementary; CRC32C is not a substitute for TLS integrity, nor vice versa.

### 7.2 No Protocol-Level Authentication

The wire protocol itself does not include authentication fields (e.g., tokens, signatures). Authentication is handled at the TLS layer (certificate-based) or at the cluster membership layer (cluster membership registration; see WIP-09). A node that is not a cluster member cannot discover worker addresses and therefore cannot open data plane connections.

### 7.3 Denial of Service Considerations

| Attack Vector | Mitigation |
|---------------|------------|
| Oversized frame (memory exhaustion) | `max_frame_size` enforced at the reader (default 16 MB). Frames exceeding the limit are rejected without allocating a buffer. |
| Connection flood | Yamux session limit per peer. Listener-level rate limiting (not part of this spec, handled at the infrastructure layer). |
| Slowloris (slow reads) | `ConnectionWriteTimeout` (10s) in Yamux config. Writers that cannot flush within the timeout are disconnected. |
| Replay attacks | Not applicable in the data plane context. Replayed records are idempotent with respect to the checkpoint/recovery protocol. |

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | Frame encoding/decoding, each message type | Go `testing`, table-driven | 100% of message types and edge cases |
| Roundtrip Tests | Encode → decode for every message type | Go `testing` with property-based checks | All field combinations |
| Fuzz Tests | Random bytes fed to frame parser | Go `testing/fuzz` | Parser never panics, never allocates > max_frame_size |
| Integration Tests | Two goroutines communicating via Yamux with wire protocol | `internal/tcp/mux.go` + loopback | All message types flow end-to-end |
| Benchmark Tests | Throughput and latency of frame encode/decode | Go `testing.B` | Establish baseline, detect regressions |

### 8.1 Key Test Scenarios

1. **Frame roundtrip:** Encode a `DataRecord` with all fields populated, decode it, and assert field equality.
2. **Minimal frame:** Encode a `DataRecord` with `nil` Key and empty Headers. Verify the payload omits those fields.
3. **Maximum frame:** Encode a `DataRecord` with a 16 MB Value. Verify it encodes and decodes correctly.
4. **Oversized frame rejected:** Attempt to decode a frame with `Length > max_frame_size`. Verify error returned, no allocation.
5. **Under-minimum frame rejected:** Send a frame with `Length < 5` (e.g., `Length = 0` or `Length = 3`). Verify protocol error.
6. **Unknown message type:** Send a frame with `MsgType = 0xFF`. Verify the receiver skips it without crashing.
7. **Partial read:** Write half a frame to a pipe, close the write end. Verify the reader returns `io.ErrUnexpectedEOF`.
8. **Barrier ordering:** Send `DataRecord, DataRecord, CheckpointBarrier, DataRecord`. Verify the downstream receives them in exact order.
9. **Watermark monotonicity:** Send `Watermark(100)`, then `Watermark(50)`. Verify the second is dropped.
10. **EndOfPartition terminates stream:** Send `EndOfPartition`, then `DataRecord`. Verify the second is dropped.
11. **Backpressure pause/resume:** Send `Backpressure(Pause)`, verify upstream reduces rate. Send `Backpressure(Resume)`, verify upstream resumes.
12. **Handshake negotiation:** Open a stream, send a `Handshake(0x00)` frame with `ProtocolVersion=1, MinVersion=1`. Verify the receiver accepts it. Send a handshake with `ProtocolVersion=99, MinVersion=99`. Verify the receiver rejects it with `EndOfPartition(Error)`. Verify that sending a `DataRecord` as the first frame (no handshake) causes the stream to be closed.
13. **Concurrent streams:** Open 100 Yamux streams, send mixed message types on each concurrently. Verify no data corruption or deadlock.
14. **TLS interop:** Run the full test suite over TLS-wrapped Yamux to verify the protocol is TLS-transparent.
15. **CRC32C validation:** Encode a valid `DataRecord`, verify the CRC32C in the header matches a manually computed CRC32C over MsgType + Payload.
16. **CRC32C corruption detection:** Flip a single bit in the payload of an encoded frame, without updating the CRC32C. Verify the reader detects the mismatch and drops the frame.
17. **CRC32C hardware acceleration:** Benchmark CRC32C computation with and without SSE4.2/ARM CRC instructions to verify hardware acceleration is active.

### 8.2 Fuzz Testing Strategy

```go
func FuzzFrameDecode(f *testing.F) {
    // Seed with valid frames
    f.Add(encodeDataRecord(validRecord))
    f.Add(encodeCheckpointBarrier(validBarrier))
    f.Add(encodeWatermark(validWatermark))

    f.Fuzz(func(t *testing.T, data []byte) {
        reader := bytes.NewReader(data)
        // Must not panic, must not allocate > maxFrameSize.
        // CRC32C mismatches are expected for random input and should
        // result in a clean error return, not a panic.
        _, _ = ReadFrame(reader, maxFrameSize)
    })
}
```

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | ~~Should we add a CRC32 checksum?~~ **Resolved:** Adopted CRC32C (Castagnoli) in the frame header. 4 bytes per frame, computed over MsgType + Payload. Hardware-accelerated via SSE4.2/ARM CRC. See Section 3.1. | Tarun | Resolved |
| 2 | Should the `Backpressure` message carry a `Credit` field (number of bytes the receiver is willing to accept) instead of binary pause/resume, to enable credit-based flow control? | Tarun | Open |
| 3 | ~~Should we support frame batching?~~ **Resolved (type reserved):** `RecordBatch (0x06)` reserved in the message type table. Structure documented in Section 3.9. Full specification deferred to a future WIP. | Tarun | Resolved |
| 4 | The `max_frame_size` default of 16 MB may be too large for memory-constrained workers. Should this be auto-tuned based on available memory? | Tarun | Open |
| 5 | ~~Should the version handshake be a separate message type?~~ **Resolved:** Adopted `Handshake (0x00)` as a dedicated message type with feature negotiation support. See Section 3.8. | Tarun | Resolved |
| 6 | Risk: msgpack's lack of a schema means field additions/removals are invisible at compile time. A codec tag typo could cause silent data loss. Mitigation: exhaustive roundtrip tests. | -- | Acknowledged |
| 7 | Risk: the protocol currently has no support for compression. At high throughput with compressible payloads (JSON events), this could waste significant bandwidth. Compression (LZ4/Snappy per-frame or per-batch) should be considered in a future protocol version. | -- | Acknowledged |
| 8 | How should the protocol handle Yamux session-level failures (as distinct from stream-level failures)? If the entire TCP connection drops, all streams on that session are lost simultaneously. The task manager needs a clear contract for this. | Tarun | Open |
