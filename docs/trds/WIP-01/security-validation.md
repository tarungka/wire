# WIP-01 security validation evidence

This records concrete validation for PR #210's transport changes. It does not
claim that WIP-01 is complete, that all vulnerabilities are excluded, or that
scanner results for an earlier commit cover a later revision.

## Scanner evidence

The CodeQL Go analysis for `0a4cce4` passed:
[Analyze (go)](https://github.com/tarungka/wire/actions/runs/34748998620/job/103701965175).
The associated [CodeQL check](https://github.com/tarungka/wire/runs/103702055172)
and GitGuardian secret check also passed. Final merge readiness requires these
checks to pass on the final PR head, not just this recorded revision.

## Focused regression evidence

The current worktree passes `go test -race -tags=integration -timeout 5m ./...`
and golangci-lint v2.5.0. Relevant executable regressions include:

- `TestTLS_MutualAuth`: certificate-authenticated transport setup and data flow.
- `TestTLS_InvalidCert`: rejection of an untrusted certificate.
- `TestTLS_ServerNameAutoInference`: target hostname verification setup.
- `TestTLS_AllMessageTypes`: negotiated data and control traffic over TLS.
- `TestCorruptFrameDiagnosticBounds`: at most 64 diagnostic bytes, no corrupt
  payload decode/copy, and no alias into a reusable frame buffer.
- `TestPartialDataFrameDeadline`: quiet streams remain valid while an incomplete
  frame must finish within its deadline.
- `TestDataWindowWriteTimeout`: a non-reading peer cannot block a data write
  indefinitely; a partially written frame closes its stream.
- `TestControlProgressAndCancellationWithExhaustedDataWindow`: control messages
  pass while data-window credit is exhausted; active and queued writes cancel.
- `TestTaskUnregisterClosesPendingGeneration`: old task registrations cannot
  inject queued streams into a replacement deployment with the same task ID.

The new data-window tests pass ten repetitions with race detection. The network
worker execution test passes 30 repetitions at each of GOMAXPROCS 1, 2, 4 and 8,
including buffer occupancy accounting and resource cleanup.

## Review scope

The ECC bot's security-evidence request is applicable to these transport changes;
scanner and focused regression evidence are linked above. Its analyzer, RAG,
discussion-triage, PR-salvage and AI-harness corpus suggestions concern unrelated
systems and do not define Wire protocol acceptance requirements. No such corpus
has been fabricated to satisfy a filename-based heuristic.

The bot also reports that its installation cannot publish checks. This is an
app-permission issue; this PR does not change installation permissions or bypass
any existing CI gate.
