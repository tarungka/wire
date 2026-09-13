# WIP-01 acceptance evidence

This maps the original §8.1 scenarios to executable evidence. A passing scenario
is not a substitute for the separate performance targets or final PR checks.
Tests are under `internal/protocol` (P), `internal/transport` (T), and
`internal/engine` (E). Historical runs and implementation details are recorded in
[completion.md](completion.md).

| # | Scenario | Evidence and limits |
|---|---|---|
| 1 | Full record round trip | P `TestWriteReadFrame_Roundtrip`; binary markers independently checked by `TestDataRecordUsesMessagePackBinary`. |
| 2 | Minimal optional fields | P `TestMinimalRecordOmitsOptionalFields`; canonical empty binary and caller ownership tests. |
| 3 | Maximum frame | P `TestMaximumLegalRecordFrame` checks exact total limit and decoded value length. The payload must leave room for metadata; a literal 16 MiB Value exceeds the 16 MiB frame limit. |
| 4 | Oversized frame | P `TestReadFrame_OversizedRejected`; ReadFrame rejects the length before taking/allocating a body buffer. |
| 5 | Under-minimum frame | P `TestReadFrame_UnderMinimumRejected`. |
| 6 | Unknown type | T `TestUnknownMsgType_Skipped` covers 0x06, 0x40 and 0xFF on TCP and mutual TLS, followed by a valid record. |
| 7 | Partial frame | P `TestReadFrame_PartialRead` and `TestReadFrame_PartialLengthField`; T `TestPartialDataFrameDeadline`. |
| 8 | Barrier ordering | T `TestBarrierOrdering` and exact 100-stream mixed-message comparisons; E output-router ordering tests cover each output partition. |
| 9 | Watermark regression | T `TestWatermarkMonotonicity` and `TestWatermarkMonotonicity_MultipleSourceIDs`. |
| 10 | EOP termination | T `TestEndOfPartition_TerminatesStream`, `TestPostEOP_ReturnsEOF`, and post-EOP writer rejection in `TestConcurrentStreams`. |
| 11 | Pause/resume | T `TestBackpressure_PauseResume`, `TestControlProgressAndCancellationWithExhaustedDataWindow`, and TLS message tests exercise real blocked writers. |
| 12 | Routing/rejection | T `TestSessionDataStreamRouting`, `TestMuxRoutesNamedTasks`, `TestDataStreamInvalidFirstFrame`, and automatic rejection observation tests. Registration must precede deployment; Dial is not a positive routing ACK. |
| 13 | 100 mixed streams | T `TestConcurrentStreams` compares 301 messages on each of 100 streams, over TCP and mutual TLS, using one session. |
| 14 | TLS transparency | Valid-message, 100-stream, failure-threshold/reset, task rejection and crossed-connection retirement tests run with mutual TLS. Final parity review of remaining boundary/deadline cases is still open. |
| 15 | Independent CRC | P `TestCRC32C_Validation` uses a separate bitwise Castagnoli recurrence; `TestCRCTypeSeedsMatchWholeMessage` covers all 256 discriminators and multiple lengths. |
| 16 | Corruption detection | P single-bit payload/type corruption tests and bounded-corruption diagnostics; T repeated corruption thresholds over TCP/TLS. |
| 17 | Hardware acceleration | The same native benchmark runs normally and with `GODEBUG=cpu.crc32=off`: approximately 84 ns versus 321 ns on Apple M4. This proves acceleration, not the <1% latency gate. |
| 18 | Compatible versions/data | T `TestSessionNegotiationVersionsAndFeatures/same_version` checks inherited parameters and actual record/EOP flow. |
| 19 | Incompatible versions | T `TestSessionNegotiationVersionsAndFeatures/incompatible` checks both errors and session closure; 1,000 race runs cover the reply/close race. |
| 20 | Feature intersection | T version/feature test verifies exact CRC-only intersection from differing feature sets. |
| 21 | Handshake timeout | T `TestSessionNegotiationTimeoutClosesSession` exercises a shortened configured timeout; DefaultHandshakeTimeout is five seconds. |
| 22 | Rolling upgrade/data | T `TestSessionNegotiationVersionsAndFeatures/rolling_upgrade` checks v2/min1 with v1, inherited v1 parameters, and record/EOP flow. |

## Separate merge gates

- Final TLS boundary/deadline parity and remaining runtime ordering audit.
- Framing throughput <3% and CRC verification latency <1%. Recorded CPU framing
  overhead remains above target; CPU-only versus TCP measurement clarification
  is pending. No percentage target has been relaxed.
- Final-head CI and applicable review/security evidence. A prior green commit
  does not validate subsequent edits.
- Final status/PR description must match these results before marking ready.
