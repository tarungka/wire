# WIP-01 full-scope completion

This work supersedes the incremental frame-write fix in #208. Completion means
all applicable WIP-01 requirements are implemented and verified, not merely that
one protocol helper passes tests. RecordBatch is reserved by §3.9 and MUST NOT be
sent; implementing batching is explicitly deferred by this TRD.

## Required implementation and evidence

| Scope | Current evidence / remaining work |
| --- | --- |
| Message formats (§3.1–3.10) | Added StreamHeader 0x00 and SessionHandshake 0x07 codecs and round trips. Finalize required/optional field conventions and boundary validation across all messages. |
| Session negotiation (§2.2, §3.10) | Mux negotiates before publication and data opening. Compatible/rolling/incompatible versions, feature intersection, timeout, early data rejection, and session reuse pass. Further audit: reciprocal worker connections and cancellation while waiting for the dial lock. |
| Stream routing (§2.2, §3.8) | Sender-only headers, first-frame deadline, RegisterTask/AcceptTask routing and unknown-target rejection pass. Worker task descriptors/executor still need distributed input/output wiring; the current executor requires a local source and creates TaskSlot with no network streams. |
| Inline ordering (§5 decision 3) | Pending final audit: records/barriers/watermarks remain ordered on data streams and reach task inputs in order. |
| Control/backpressure (§3.7, §6.3) | One retained control stream dispatches pause/resume by Yamux ID. Writes pause at 80% and resume at 20%; engine input read-ahead reports occupancy. TCP and TLS tests demonstrate blocked writes and recovery. Still verify control progress under a fully exhausted data window and runtime buffer saturation end to end. |
| Errors and lifecycle (§6) | Partial-frame completion deadlines preserve idle streams; EOP closes output and rejects later writes; error counters reset independently; corrupt frames avoid payload-copy allocation. Completed-barrier suppression and detailed diagnostics remain. |
| TLS (§7) | Existing TLS/mTLS positive and negative tests pass with session negotiation; TLS all-message test now exercises actual control-stream pause/resume. |
| Resource bounds (§7.3, §8.2) | Verify bounded allocations and session reuse, closure during negotiation, malformed streams and fuzz inputs. |
| CRC overhead (§1.4) | Native CRC: median 80.75 ns/1025 bytes; independent software recurrence: 1850 ns. Hardware acceleration is evident. The <1% verification-latency acceptance target is not demonstrated. |
| Framing overhead (§1.4) | FAILS target: median raw encoding + write 504.3 ns vs framed encoding + write 620.8 ns, +23.1%. See benchmark evidence below. Do not mark Implemented. |

## Section 8.1 acceptance scenarios

All 22 scenarios must have executable evidence before changing the WIP status:

1. Full DataRecord round trip.
2. Nil key/empty headers omitted from minimal encoding.
3. Maximum legal frame round trip (account for msgpack and frame overhead).
4. Oversized length rejected before allocation.
5. Under-minimum length rejected.
6. Unknown message type skipped.
7. Partial frame rejected.
8. Exact record/barrier ordering.
9. Backward watermark dropped.
10. EOP terminates data flow.
11. Actual sender slowdown and recovery on pause/resume.
12. Valid task routing, unknown target rejection, missing-header rejection.
13. 100 concurrent mixed-message streams without corruption/deadlock.
14. TLS interoperability of the complete protocol.
15. Independent CRC32C header verification.
16. Bit corruption detected before decoding.
17. Hardware/software CRC benchmark.
18. Compatible session versions and subsequent data flow.
19. Incompatible versions tear down the session.
20. Feature intersection.
21. Missing session handshake times out and closes the session.
22. Rolling-upgrade negotiation and subsequent data flow.

## Specification reconciliation required

Section 3.8's introductory paragraph, routing struct, revision 0.3, and decision 5
require routing-only StreamHeader fields. Its version-field table and negotiation
steps are stale copies of the old per-stream handshake and must move to §3.10.
The receiver error reply for an unknown task conflicts with the normal strict
unidirectional data-flow rule; define the rejection path explicitly. The maximum
frame test's 16 MiB value exceeds the stated frame-length limit once metadata is
included; test the exact legal total and document that distinction. These are
specification corrections, not permission to discard acceptance scenarios.

## Benchmark evidence — 2026-09-13

Go 1.25.0, darwin/arm64, Apple M4. Three samples, 300 ms per sample:

```text
go test ./internal/protocol -run '^$' -bench 'BenchmarkFramingOverhead|BenchmarkCRC32CImplementation' -benchmem -benchtime=300ms -count=3
raw_msgpack: 514.4, 504.3, 504.1 ns/op; 3443 B/op; 6 allocs/op
wire_frame: 604.9, 632.1, 620.8 ns/op; 3459 B/op; 8 allocs/op
CRC software: 1867, 1841, 1850 ns/op; 0 allocs/op
CRC native: 80.78, 80.75, 80.74 ns/op; 0 allocs/op
```

Both framing cases encode the same record and write to a reused bytes.Buffer.
The raw baseline includes encoding and the destination write. The software CRC
uses an independent bytewise Castagnoli recurrence. These are local CPU costs,
not end-to-end network latency; network delay must not be used to hide framing
overhead. The performance targets remain unchanged and unmet/unproven.

## Validation and remaining gates

Protocol, transport, and engine race suites pass after explicit cancellation of
Yamux reads. The final repository suite (`go test ./...`) passes, as do the transport and
engine race suites after input-buffer integration and paused-output cancellation.
Decoder fuzzing passed 1,763,490 executions in the recorded 10-second run. The merged
checkpoint fixture now supplies EpochID=1 to match the barrier it created.

Other remaining work includes completed-checkpoint barrier suppression, detailed corruption diagnostics,
final message-field validation, reciprocal connection reuse, and full acceptance
coverage. This branch is not a completed WIP and is not ready to merge.
