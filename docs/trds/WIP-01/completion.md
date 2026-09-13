# WIP-01 full-scope completion

This work supersedes the incremental frame-write fix in #208. Completion means
all applicable WIP-01 requirements are implemented and verified, not merely that
one protocol helper passes tests. RecordBatch is reserved by §3.9 and MUST NOT be
sent; implementing batching is explicitly deferred by this TRD.

## Required implementation and evidence

| Scope | Current evidence / remaining work |
| --- | --- |
| Message formats (§3.1–3.10) | Active message codecs, binary representation, required fields, strict known-field types, integer bounds, enum/range checks, optional omission and empty-value conventions are implemented and tested. The negotiated SessionDrain extension is included. |
| Session negotiation (§2.2, §3.10) | Mux negotiates before publication and data opening. Compatible/rolling/incompatible versions, feature intersection, timeout, early data rejection, and session reuse pass. Same-peer dialing is coalesced with cancellable waiters; unrelated peers make progress independently. Sequential reciprocal reuse, deterministic crossed-dial selection and graceful duplicate retirement pass TCP and mutual TLS tests. Membership endpoint aliases/NAT remain an integration consideration. |
| Stream routing (§2.2, §3.8) | Sender-only headers, first-frame deadline, RegisterTask/AcceptTask routing and unknown-target rejection pass. Worker descriptors now wire network inputs/outputs into TaskSlot, with explicit task IDs and partition indices. Workers listen on and advertise an actual data endpoint. Deployment ordering, distributed completion notifications, and broader multi-worker acceptance remain to audit. |
| Inline ordering (§5 decision 3) | A single output dispatcher distributes records and broadcasts barriers/watermarks/EOP in order to every output. Exact two-partition sequence and terminal-drain tests pass. Final multi-input/runtime ordering audit remains. |
| Control/backpressure (§3.7, §6.3) | One retained control stream dispatches pause/resume by Yamux ID. Writes pause at 80% and resume at 20%; engine input read-ahead reports occupancy. TCP and TLS tests demonstrate blocked writes and recovery. A full-window regression now proves control progress while a 2 MiB data frame is blocked. Active/queued writes cancel and configured stream write deadlines close partial frames. Network execution stress covers buffer saturation. |
| Errors and lifecycle (§6) | Partial-frame completion deadlines preserve idle streams; EOP closes output and rejects later writes; error counters reset independently; corrupt frames avoid payload-copy allocation. Corruption diagnostics now include stream-relative offsets, bounded 64-byte previews and both CRCs; invalid lengths log the remote address and reported length. Streams suppress barriers at or below authoritative global completion or a restored checkpoint. Input readers recheck after queueing; distributed completion notifications still need worker wiring. |
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

Other remaining work includes distributed task/completion wiring,
final message-field validation, reciprocal connection reuse, and full acceptance
coverage. This branch is not a completed WIP and is not ready to merge.

### Goal continuation: CI and diagnostics

The integration CI failure at `01a63d6` came from
`TestInputReader_EventChannelFull_UnblocksOnContextCancel`: it treated an expected
sender error after cancellation as an unconditional failure and did not join the
sender goroutine. It now waits for a full event channel, cancels explicitly,
and joins both goroutines without externally closing the stream to unblock them.
The revised test passes 50 iterations under the race detector.

The current worktree passes `go test -race -tags=integration -timeout 5m ./...`
and golangci-lint v2.5.0 reports zero issues. Corrupt-frame diagnostics have a
regression test proving the preview is bounded, CRC values differ, the payload
is not decoded, and the diagnostic does not alias a pooled buffer.

PR review inspection: no inline comments or submitted reviews were present.
The ECC bot requests security evidence; its unrelated analyzer/RAG/harness corpus
recommendations do not describe Wire's protocol. Attach final CodeQL/TLS/fuzz
validation evidence in the PR when the implementation is complete; do not change
app permissions merely because the bot cannot publish checks.

### Goal continuation: checkpoint replay and dial isolation

The coordinator now exposes its globally completed checkpoint, advancing only
when all pending task ACKs have arrived. TaskSlot connects network inputs to that
watermark and initializes them from RestoredCheckpointID. Streams suppress stale
barriers, and the input reader checks again after buffered reads. The regression
uses a real two-task coordinator and network stream: partial ACK still permits
the active barrier, full completion drops older/equal barriers, and a restored
watermark rejects replay without regressing on stale notifications.

Mux now coalesces only dials for the same address. A waiting caller may cancel
without waiting for another caller's network timeout, and a stalled worker does
not block a healthy worker. The test holds a real TCP connection open without
handshaking and verifies both cancellation and independent peer progress.

Validation: `go test -race -tags=integration -timeout 5m ./...` passes; the Mux
concurrency/cancellation regressions pass five repetitions; golangci-lint v2.5.0
reports zero issues. Full WIP-01 completion is still pending the remaining gates.

### Goal continuation: network-backed task execution

Workers now open a data Mux before registering and advertise its bound endpoint.
TaskDescriptor upstream/downstream channels accept explicit task IDs and input
partition indices; the executor opens declared outputs and places accepted inputs
in descriptor order, rejecting unexpected or duplicate sources. Non-source tasks
can execute from these network inputs. Cancellation/error cleanup closes streams
and unregisters the receiving task. A new registration generation cannot consume
streams queued for an earlier deployment with the same task ID.

The engine previously let output writers compete for a shared channel, delivering
control frames to only one partition. The output dispatcher now distributes data
round-robin and broadcasts ordered controls to all outputs. Successful source
completion drains output despite application backpressure; failure/external
cancellation interrupts it. A transport EOF without EOP fails the input rather
than leaving its operator chain waiting indefinitely.

Evidence: `TestTaskExecutorProcessesAcrossWorkerStreams` sends 2048 records
through distinct worker transports, a source task and a network-backed map/sink
task; it verifies count, single operator initialization/closure, terminal delivery
and removal of routing state. Ten race-enabled repetitions pass. Additional tests
verify exact record/control sequences on two partitions, stale registration
isolation, and premature EOF. `go test -race -tags=integration -timeout 5m ./...`
passes, and golangci-lint v2.5.0 reports zero issues.

This proves explicit-descriptor network execution, not automatic cross-worker
JobGraph planning: the coordinator's existing planner still produces fused local
chains and rejects shuffle edges. Remaining completion work must not treat this
as evidence of a full distributed deployment/recovery acceptance run.

### Goal continuation: exhausted windows and Linux CI accounting

A real exhausted-window test reproduced a missing cancellation path: receiving
only a frame header and leaving its 2 MiB body unread blocked the writer forever,
even after cancellation. Pause/resume still traversed the control stream. Writes
now use cancellable serialization, an explicit stream deadline, and a synchronized
context callback to interrupt Yamux window waits without leaking a deadline into
a later write. Successful source completion retains its dedicated output drain
context; explicit cancellation no longer silently continues unpaused writes.

Linux CI on `0a4cce4` found `transport: invalid buffer occupancy` in the network
worker test. Channel receive frees a slot before the consumer's count decrement,
so the producer could temporarily count six messages against five slots. Explicit
slot reservation now prevents reuse until the occupancy decrement has occurred.
The fix passed 120 network executions (30 each at CPU counts 1, 2, 4 and 8) under
race detection. Full integration-tagged race tests and v2.5.0 lint pass locally.

[Security validation evidence](security-validation.md) addresses the applicable
ECC bot request with actual scanner and focused test evidence. Final-head CI and
remaining WIP requirements still gate merge readiness.

### Outgoing frame limits

All transport writes (session handshake, stream header, data, rejection and
backpressure) now enforce Config.MaxFrameSize before writing any frame bytes.
The length includes type and CRC and excludes the four-byte prefix, matching
ReadFrame. Boundary tests cover all seven active message types and prove that
an oversized frame produces no output and the exact-limit frame round-trips.
Protocol/transport race tests and v2.5.0 lint pass. Encoding now rejects writes that would exceed the payload budget before
copying those bytes. Protocol decoding rejects trailing MessagePack objects or
garbage after the message. Final field-convention validation remains open. The low-level raw frame writer remains available
for protocol fixtures and checks uint32 representability.

At head 76f5fda, lint, unit tests, integration tests, build and Docker CI passed.
Go and Python CodeQL jobs failed after reaching upload, with no error annotation;
the failed jobs were requested to rerun. This is not yet security-check clearance.

The bounded encoder and trailing-payload checks pass the full integration-tagged
race suite and golangci-lint v2.5.0. A 16 MiB binary value with a 32-byte budget
is rejected without copying the binary value into the output buffer.

### Reverse streams on outbound sessions

Mux now accepts peer-opened data streams on sessions it originally dialed. The
accept loop belongs to the mux lifetime, joins during Close, and routes through
the same task registry as incoming connections. A regression test sends records
in both directions on one session, ends the reverse partition, and proves the
original stream is still usable. It passed 20 race iterations; the full
integration-tagged race suite and v2.5.0 lint also pass. Address-to-peer lookup
and safe simultaneous-dial arbitration remain necessary for complete reciprocal
Mux.Dial reuse; this test deliberately does not claim those are implemented.

### Listening endpoint advertisement and reverse Dial reuse

SessionHandshake has an optional `lp` listening port, populated from the actual
bound Mux listener. Incoming sessions are indexed by observed peer IP plus this
port, allowing sequential reciprocal Mux.Dial calls to reuse the connection.
Dial-only peers omit the field; no advertised address causes an automatic dial.
All live sessions are tracked separately from address cache entries so concurrent
connections cannot become unowned when an entry is replaced. Accept-loop exit
removes only entries belonging to that session.

The reciprocal-dial regression checks the actual Session pointers on both ends
and asserts one live connection per mux; it passed 20 race iterations. Safe
simultaneous-first-dial arbitration and membership endpoint aliases remain open.
The full integration-tagged race suite and golangci-lint v2.5.0 pass with endpoint
advertisement and session ownership tracking.

### Crossed connection selection

Mux indexes negotiated sessions by NodeID and deterministically selects the
connection initiated by the lower NodeID. A shared initiator-endpoint tie break
handles same-direction alias dials. Cache aliases follow the selected session.
This changes only future stream placement; active connections are not closed.
A forced test opens both TCP connections before handshaking, verifies that both
workers select the same physical connection, sends on both original connections,
and verifies a later reverse Dial uses the selected one. It passed 30 race runs.
Safe retirement of the duplicate after active streams drain is still required;
selection alone does not prove one live TCP connection per pair after a cross-dial.
The full integration-tagged race suite and golangci-lint v2.5.0 pass with the
selection change.

### Graceful duplicate retirement

SessionDrain (0x08) is a control-only extension gated by negotiated feature bit
2. Losing connections stop new data opens atomically, exchange drain intent,
and continue serving existing streams and backpressure. Each side sends Ready
only after its opening operations finish and Yamux retains only the control
stream; both readiness confirmations are required before closing TCP. A local
stream count alone is not sufficient because a remote open may be in flight.
Mux.Dial retries when selection races with retirement and waits cancellably for
session publication rather than exposing a retirement error to the task.

The forced cross-dial test now holds a losing-stream record across selection,
proves it survives, and waits for one live connection per mux after EOP. It passed
20 race iterations. Public simultaneous reciprocal Dial passed 120 race runs
(30 each at CPU counts 1, 2, 4, 8). Older peers without the negotiated feature
continue data flow; sending the extension without negotiation closes the session.
The full integration-tagged race suite passes. The extension is included in
frame-limit and decoder fuzz seeds; golangci-lint v2.5.0 passes after its deadline
cleanup check was corrected. Remaining WIP gates, including performance and the
broader acceptance audit, are not implied complete by these connection tests.
Decoder fuzzing with the SessionDrain seed passed 1,380,331 executions in the
10-second fuzz run (11.5 seconds including setup/finish).

### CRC type-state optimization and mutual TLS retirement

CRC32C now precomputes the 256 message-type starting states. Every frame still
checks type plus payload, but avoids a one-byte allocation and a separate CRC
update. A regression compares all 256 types at six payload lengths (including
zero) against a checksum of the complete concatenated message. A same-process
1 KiB microbenchmark measured median 84.33 ns/1 allocation for two CRC updates
versus 80.89 ns/0 allocations for the precomputed start.

The framing benchmark now includes the bounded writer actually used by
transport. Three 500 ms samples on Apple M4, darwin/arm64:

```text
raw_msgpack: 505.2, 504.2, 508.1 ns/op; 6 allocations
wire_frame: 603.0, 597.7, 601.0 ns/op; 7 allocations
wire_bounded: 604.5, 607.8, 602.9 ns/op; 7 allocations
```

Bounded framing therefore still adds 19.7% CPU time at the medians. This is not
a passing performance gate. The WIP does not define CPU-only versus network
measurement methodology; clarification was requested, without relaxing either
percentage target or claiming a passing network benchmark.

The held-stream duplicate retirement test now runs over both TCP and mutual TLS
and passed 20 iterations each under race detection. The test CA must be trusted
for both roles because each worker is a TLS client and server. Full integration
race tests and v2.5.0 lint pass. All CI checks, including CodeQL, passed on the
preceding commit ec42d17; the optimized commit still requires its own CI.

### Concurrent mixed-message and TLS failure-path acceptance

Section 8.1 scenario 13 previously only counted DataRecord reads. It now runs
100 streams over one session, on both TCP and mutual TLS, comparing every field
of 100 records, 100 barriers, 100 watermarks, and EOP per stream in exact order.
It verifies routing headers, rejects post-EOP writes, checks terminal EOF, and
asserts one session per endpoint. Five runs per transport passed under race
checking (301,000 messages total).

Unknown-type skipping, CRC/decode failure thresholds, and independent counter
reset tests now each run over TCP and mutual TLS. Ten iterations of all five
cases passed. This strengthens scenario 14 with failure-path evidence instead
of inferring TLS transparency only from a successful handshake.

Frame payload decoding now enforces the MessagePack map envelope specified for
all active message types; nil, positional arrays, strings, and scalars are
rejected. Typed nil outgoing messages fail before any bytes reach the wire.
Tests cover all eight active types, including the negotiated drain extension.
Required-field presence/type conventions still need the final schema audit.
Full integration-tagged race tests and golangci-lint v2.5.0 pass for this change;
protocol tests were rerun after the linter's equivalent boolean simplification.

### Binary encoding and required-field presence

The shared protocol codec was still in legacy MessagePack mode, which encoded
byte slices as raw strings despite WIP-01 specifying binary fields. WriteExt is
now enabled. A byte-level test checks bin8 markers and non-UTF-8 contents in
record keys, values, and header values; the framed payload matches the encoded
message. The complete integration race suite, including RPC users of this
codec, passes with current MessagePack encoding.

An allocation-free boundary scanner rejects malformed maps, missing required
fields, repeated required fields, impossible lengths/counts, and nesting beyond
64 levels before typed decoding. It skips unknown fields without copying record
payloads. Each active message is tested with each required field removed; valid
zero values and nested forward extensions still decode. Final value/type and
nil-versus-empty conventions remain to audit; field presence alone does not
prove those semantics.

Full integration-tagged race tests and v2.5.0 lint pass. Decoder fuzzing with the
scanner passed 1,783,310 executions in the 10-second run (11.5 seconds overall).

### Field types, ranges and nil/empty conventions

Known fields now require their declared MessagePack type before typed decoding:
strings, binary values, integers, floats, booleans, and string-to-binary header
maps are distinguished. The typed codec checks integer width/sign bounds. EOP
reason values outside 0–2 and backpressure state/usage values outside their
specified ranges, including NaN and infinity, are rejected.

Nil Go record values and nil header values encode as zero-length binary. Optional
nil keys and empty header maps remain omitted. Normalization does not mutate the
caller-owned record or header map, and ordinary nonempty records take the direct
path. Byte-level tests cover both pointer and value records and ownership.

Field-type tests cover every active message, integer overflow and negative values
for unsigned fields, wrong wire types, and invalid header values. Full integration
race tests and v2.5.0 lint pass. Decoder fuzzing passed 1,637,305 executions in the
10-second run (11.5 seconds overall). Performance methodology clarification and
other runtime/acceptance gates remain open; schema validation does not settle
those gates.

### Same-worker routing and incompatible-handshake CI race

A self-dial gives one mux the two Yamux endpoints of a single TCP connection.
The duplicate selector previously marked one endpoint for draining, eventually
closing both. Retirement now recognizes reverse-matching TCP endpoints as the
same physical connection. A regression first reproduced the draining flag, then
verified ten routed local streams, payloads and EOP with neither endpoint retiring.
Loopback, crossed-connection and concurrent reciprocal tests pass 20 race runs.

Linux CI on 0d95928 failed the incompatible-version case: the initiator received
the reply and closed before the acceptor's Yamux write returned, so the acceptor
reported only session shutdown. Once both version ranges have been validated,
reply-write failure now retains ErrVersionIncompatible along with the underlying
write error. The existing incompatible negotiation regression passed 1,000 race
runs (250 each at CPU counts 1, 2, 4 and 8), including closure of both sessions.
Full integration-tagged race tests and golangci-lint v2.5.0 pass locally; the new
head still requires Linux CI verification.

### Executable specification examples and dependency audit

The DataRecord, CheckpointBarrier, StreamHeader and EndOfPartition byte examples
contained inconsistent lengths, timestamp bytes or string lengths, and placeholder
CRCs. They now show complete frames with actual checksums. A golden test checks
the encoder bytes, their presence in the specification, and their decoded values.
The documentation distinguishes integer value ranges from MessagePack's compact
encoded widths and explains map ordering. Protocol tests and v2.5.0 lint pass.

The original scope table explicitly excludes Coordinator–Worker RPC and TLS
implementation beneath Yamux. Their integration dependencies must remain visible:
worker bootstrap does not yet wire node TLS flags through to transport, and
record-level recovery is not provided by framing alone. The TLS API and its tests
are real evidence; CLI flags, membership discovery, or CRC are not proof of
transport authentication or replay protection. The specification now states these
limits accurately instead of asserting unimplemented guarantees. The completed
barrier receiver hook remains part of WIP-01; whole RPC checkpoint orchestration
must be evaluated under its owning WIPs.

### Sender observation of task rejection

Unknown-target tests previously consumed reverse EOP manually, while worker
outputs only write. Mux-managed senders now monitor the reverse direction and
close rejected outputs, preserving ErrTargetTaskRejected for writers (including
races with queued writes). The monitor uses the same CRC/decode thresholds and
unknown-message handling as normal frame reads. Explicit ReadMessage callers
can still retrieve the cached rejection once. Monitoring is joined during mux
shutdown. Normal reverse EOF does not interrupt the output owner's final EOP.

TCP/mutual-TLS rejection and 100-stream mixed-message tests passed five race runs.
The complete integration race suite passes, including existing explicit rejection
readers. A first-byte/Close race was also corrected so the frame completion
deadline cannot overwrite Close's immediate read deadline; cancellation/closure
stress tests passed ten runs. Lint v2.5.0 reports zero issues.

This does not add a positive header acknowledgment or make Dial prove that the
target is registered. Deployment ordering remains the caller's responsibility;
rejection handling is asynchronous and must not be described as a delivery ACK.

### Acceptance mapping and independent checks

[acceptance.md](acceptance.md) maps all 22 original scenarios to actual tests and
calls out unproven parity/performance gates. Compatible and rolling-upgrade tests
now send records and EOP over a routed stream and verify inherited negotiated
parameters, instead of stopping after opening an empty raw stream. CRC header
validation now uses an independent bitwise Castagnoli recurrence rather than the
production helper. Unknown-type tests include reserved 0x06 and 0xFF over TCP/TLS.

The same native CRC benchmark measured 84.43, 84.84 and 84.24 ns normally versus
325.9, 321.1 and 318.3 ns with GODEBUG=cpu.crc32=off on Apple M4. This confirms
hardware dispatch, without claiming the separate <1% verification latency target.
Full integration race tests passed after the negotiation/CRC changes; the final
unknown-type extension passed targeted protocol/transport race runs. Lint v2.5.0
passes. Current PR review inspection found no submitted reviews; automated
security-evidence recommendations still require final-head evidence at completion.

### 2026-09-13 — barrier ordering and TLS parity

A deterministic engine regression exposed a checkpoint ordering bug: control
priority snapshotted before queued pre-barrier records, while side-buffered
post-barrier records were emitted before the forwarded barrier. The chain now
drains pre-barrier input before snapshotting and forwards the barrier before
releasing post-barrier data. Buffer transfer/reset and reader classification are
atomic; full buffers wait for change or cancellation, and a fast next barrier
waits for its previous alignment to finish.

The four new ordering/concurrent-buffer regressions passed 30 race-enabled runs.
The legacy test that required post-barrier data before the barrier was corrected.
The full `go test -race -tags=integration -timeout 5m ./...` suite passes.
TCP/mutual-TLS parity now covers maximum legal frames, invalid lengths, partial
frame deadlines, exhausted-window deadlines, negotiation, routing and invalid
first frames. Performance gates remain open; these correctness tests do not
prove the percentage targets.

### 2026-09-13 — single-write framing performance

The bounded encoder reserves the nine-byte frame header in its encoding buffer,
fills length/type/CRC in place, then writes the complete frame once. This avoids
a separate transport write and header allocation. Size rejection still happens
before wire output; exact-boundary, golden-format, nil-message and short-write
checks pass. The full race/integration suite and golangci-lint v2.5.0 pass.

Apple M4, Go 1.25, five one-second benchmark samples (medians):

| Benchmark | Raw msgpack | Bounded Wire | Time overhead |
|---|---:|---:|---:|
| CPU encode/write, unchanged baseline | 499.2 ns | 456.0 ns | -8.65% |
| TCP encode/deliver/decode, full value-byte validation | 2276 ns | 2315 ns | 1.71% |

Commands: `go test ./internal/protocol -run '^$' -bench '^BenchmarkFramingOverhead$' -benchmem -count=5 -benchtime=1s`
and the same command with `BenchmarkTCPFramingThroughput`. The TCP benchmark
uses identical unbatched 1 KiB records, equal reader buffering, and includes
receiver draining. Connection setup is excluded. Both paths validate key,
value and timestamp. Raw uses the MessagePack stream decoder; Wire uses the
production frame reader and payload decoder. It measures loopback TCP, not
Yamux or deployment-network throughput. One Wire TCP sample was 2507 ns,
so the median comparison is not a guarantee for every run or environment.

These results improve on the earlier failing CPU result and fall below the 3%
median throughput target in this setup. The independent <1% CRC latency gate
remains unproven; hardware acceleration alone does not establish that result.

### 2026-09-13 — isolated CRC latency measurements

`BenchmarkCPUCRCVerificationLatency` and `BenchmarkTCPCRCVerificationLatency`
compare identical frame readers with checksum computation as the only variable.
The comparison reader is test-only; production always verifies CRC. A parity
test checks the verifying reference against production for valid, corrupted,
truncated, undersized and oversized frames. Both timed variants deserialize the
payload. TCP sends one pre-encoded frame at a time and acknowledges only after
decode, so its result includes the network/scheduling round trip; it is not a
CPU verification measurement.

Apple M4, Go 1.25, 1 KiB values, five one-second samples:

| Measurement | Without CRC median | With CRC median | Added latency |
|---|---:|---:|---:|
| CPU parse/decode | 399.3 ns | 482.3 ns | 20.79% (83 ns) |
| Acknowledged loopback TCP | 14493 ns | 14533 ns | 0.28% (40 ns) |

TCP samples without CRC: 14300, 14364, 14509, 14505, 14493 ns.
TCP samples with CRC: 14508, 14587, 14533, 14213, 16701 ns.
The sample spread is larger than the median difference; these five runs do not
establish a robust sub-percent bound. CPU samples consistently fail a CPU-only
interpretation of the <1% target. The acceptance measurement requires an explicit
decision; the target has not been relaxed and is not marked complete.

Reproduce with `go test ./internal/protocol -run '^TestCRCComparisonMatchesProduction$' -bench '^Benchmark(CPU|TCP)CRCVerificationLatency$' -count=5 -benchtime=1s`.
