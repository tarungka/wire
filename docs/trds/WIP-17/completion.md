# WIP-17 full-scope completion tracking

The user requested completion of the full TRD, not another isolated increment.
This checklist supplements the proposal; it does not reduce its scope. WIP-17
remains partially implemented until every applicable requirement has evidence.
The existing PR is #197. Other merged TRDs will use linked follow-up PRs.

| Requirement | Implementation / remaining verification |
| --- | --- |
| HTTP TLS and optional client certificates | Runtime wiring and certificate tests exist; rerun the complete secure deployment walkthrough on the merged runtime. |
| Inter-node mutual TLS | Coordinator/worker RPC TLS exists. Verify identities, invalid/expired certificates, secure data-plane transport, and the documented trust boundaries. |
| Authentication file (§3.4, §4.1) | Startup loader added: JSON, unique users/keys, roles, one credential, bcrypt cost >=10, bounded file/user count. Expand negative tests and verify all schema constraints. |
| Basic and Bearer authentication (§3.5) | Runtime middleware added; authentication failures return 401. Verify certificate+auth combinations through an actual HTTPS server. |
| Endpoint RBAC (§3.6) | Admin/operator/viewer matrix tested in middleware. Verify every actual registered endpoint, including redirects, escaped paths, metrics, and probes. |
| Brute-force protection (§6) | Bounded global authentication admission added (20 burst, 10 checks/second). Document operational behavior and test concurrent admission. |
| Authentication logs (§7.3) | Successful identity and failed source/Basic username logging added. Add capture tests proving passwords and API keys never appear. |
| Connector secret substitution (§3.7) | Pending: coordinator-time resolution, missing-variable rejection, unresolved references persisted, resolved credentials kept only in memory and delivered to workers. |
| Credential redaction (§3.7) | Pending: verify job API, persistence, recovery, errors, and logs cannot expose resolved secrets. |
| Certificate/auth revocation (§4.2, §8.1) | Pending: restart/revocation integration tests and operational instructions. Rotation automation is explicitly out of scope. |
| Encryption at rest strategy (§1.3) | Documented in [storage security](../../storage-security.md): all runtime storage surfaces, temporary files, backups, key rotation and operator acceptance checks. Actual encrypted-volume deployment remains an operator verification requirement. |
| Flag/config documentation (§1.4) | Pending: cross-reference every existing security flag and supply tested example files and certificate commands. |
| Unit coverage (§8) | Pending: achieve the specified 100% coverage of authentication logic, without treating route-only tests as full system validation. |
| Integration/negative/security tests (§8) | Pending: all roles/endpoints, invalid credentials/certificates, revocation, missing secrets, and protocol/cipher verification. |

Proposal discrepancies must be recorded explicitly rather than silently deleting
acceptance criteria: the current runtime has no Raft transport; a missing client
certificate fails at the TLS layer rather than yielding an HTTP 401; metrics are
explicitly public in §3.6. The secured deployment guide must explain these facts.

## Current-stack integration

The existing WIP-17 branch now integrates the WIP-13–16 stack. Conflicts retain
the current HA term ownership, RPC certificate identity checks, worker discovery,
checkpoint cleanup, heartbeat fencing and lifecycle configuration. The old
worker TLS fixture now uses RPCTLSConfig. Focused TLS/authentication race tests
pass in cmd, coordinator and transport; the worker package compiles but had no
matching tests in that filtered run. Full repository compilation also passes.

The merge identifies an unresolved production gap: HA startup returns through
NewHAService before standalone HTTP TLS/auth setup, and term handlers currently
construct unconfigured HTTPServer instances. HA HTTPS/auth must be wired before
this WIP can be considered complete or ready to merge. Data-plane/replica TLS,
client credentials, secret handling and the original acceptance matrix remain
open as tracked above.


## HA HTTP security integration

`HAService.ConfigureHTTP` now installs a cloned TLS configuration and immutable
authentication policy before listening. Authentication wraps term dispatch, so
standby redirects and new leadership handlers cannot bypass it or reset its
admission limiter. The HA listener uses ServeTLS when configured; command startup
loads credentials before choosing HA or standalone execution. Redirects received
over HTTPS retain HTTPS and their original path/query.

`TestHAHTTPAuthenticationSurvivesTakeover` uses real TLS listeners and file-lock
leadership takeover with shared Pebble metadata. It verifies unauthenticated
requests fail, viewer mutations are forbidden, authorized reads work after
takeover, health remains public, TLS 1.3 is negotiated, plaintext is rejected,
and standby redirects retain HTTPS. Invalid auth configuration installs no partial
policy, and configuration changes after Listen are rejected. This resolves the
HA startup gap identified during stack integration; the other requirements above
remain open.

## Authenticated management CLI

The management CLI now accepts private CA roots, optional HTTPS client
certificate/key, Bearer key files, and Basic username/password files. Its shared
internal API client requires HTTPS when credentials/TLS options are supplied,
uses TLS 1.3 and hostname verification, binds requests to one origin, and refuses
redirect following. It never retries mutations. Credential reads are bounded and
errors do not include credential contents. Tests exercise trusted HTTPS with both
authentication methods, CLI flag propagation, unchanged caller headers, foreign
origin/Host rejection, redirect isolation, plaintext refusal and invalid files.
SDK submission and worker discovery still need this client configuration wired
through; this increment does not claim those paths are secured.

## SDK HTTP authentication

`SetCoordinatorSecurity` now wires private trust roots, optional client
certificates and file-backed Bearer/Basic credentials into remote submission and
polling through one origin-bound client. Offline ExportSubmission neither reads
nor exports credentials or their paths. Tests verify authenticated submission and
completion polling, decoded-graph secret exclusion, plaintext rejection before
network activity, and refusal to replay a redirected submission. Worker discovery
configuration remains the next client integration gap.

## Secured worker discovery

Node `worker.discovery_http` and SDK `WorkerConfig.DiscoverySecurity` now configure
private trust roots, optional client certificates and file-backed API credentials
for discovery. Public workers also expose CoordinatorSeeds and durable EpochPath.
Readiness/epoch confirmation remains mandatory. HTTPS or credential-configured
leader hints are limited to explicitly configured HTTPS seed origins; bare hints
inherit HTTPS, and downgrade/unlisted hints are rejected without sending a request.
Legacy unauthenticated discovery behavior is retained.

Worker race tests cover authenticated standby/leader confirmation, unlisted and
plaintext hints, bare secure hints, and plaintext seed refusal. A public SDK
worker test discovers through private-CA HTTPS with credentials and registers over
the real coordinator RPC server. Full SDK and cmd race suites passed; the initial
config run identified the generated reference update, which was regenerated and
verified. Original data-plane/replica TLS and remaining security acceptance work
are still required.

## Worker peer mTLS transport wiring

Worker PeerTLSConfig / node worker.peer_tls now covers data mux connections,
checkpoint replica listeners, reconnecting uploads and restore fetches. Explicit
CA trust, certificates and mutual verification are required when enabled. Node
configuration builds a symmetric server/client trust policy; SDK workers clone
the supplied configuration. The configuration reference includes the new fields.

Real data-frame exchange and replica publication/fetch/restore pass with mTLS.
Negative tests reject plaintext, missing client certificates, wrong hostnames,
untrusted data peers and incomplete mutual-TLS configurations. The full cmd,
config, worker and SDK race suites passed. This establishes transport wiring,
not full peer certificate-to-claimed-worker identity binding or the remaining
revocation/security matrix; those requirements remain open.


## Data-session certificate identity binding

Worker peer TLS enables transport RequirePeerIdentity. Session negotiation now
requires a verified chain and a leaf certificate Common Name matching the claimed
NodeID, before publishing negotiated peer identity/address state. The same check
runs on client and server handshakes. Worker data tests cover trusted-certificate
client impersonation and server identity mismatch, alongside plaintext/untrusted
certificate rejection and successful mutual-TLS frame exchange.

Checkpoint RPCs do not use this session negotiation. Their certificate-to-request
identity/assignment binding remains separate work, as does the complete original
security acceptance matrix. This increment does not claim those paths are done.


## Checkpoint certificate identities

Replica sessions now carry the verified nonempty certificate Common Name in a
private connection context. Secure fetches reject a mismatched WorkerID before
archive lookup/assignment authorization. Upload publication authorization sends a
new SourceWorkerID attestation, derived by the receiving replica from that context,
to the coordinator; it must match the checkpoint task owner. This does not add
an uploader-controlled identity field to the archive/receipt. Secure replicas
reject missing connection identity before making the authorization RPC.

Tests exercise the real TLS replica service forwarding the verified uploader,
coordinator rejection of a different task owner, missing-context rejection and
fetch impersonation despite an otherwise-permissive test authorizer. The latter
asserts no authorization callback and no returned bytes; streaming rejection may
surface as EOF. Coordinators must be upgraded before enabling this peer policy
because legacy coordinators ignore the additional attestation field. Other
original security gates remain open.

### Authentication acceptance: admission, audit logs and revocation

`http_auth_security_test.go` adds executable evidence for concurrent admission
(the 20-request burst is shared across 200 concurrent callers), token refill,
duplicate Authorization header rejection, and clearing plaintext API keys after
loading. Captured DEBUG-enabled logs and HTTP responses are checked for both
accepted and rejected credentials; audit records retain source IP and username.
A replacement-server test verifies the documented restart-based revocation
contract: the running server retains its immutable credential snapshot, while
its replacement rejects the removed key and accepts the new key.

These tests do not establish full HTTP-route coverage or complete WIP-17:
connector secret resolution/redaction and the remaining security acceptance
requirements still need implementation or verification.

### Secret configuration parser groundwork

`internal/secretconfig.Resolve` expands required `${VAR}` and optional
`${VAR:-default}` references in JSON string values using an injected lookup.
It preserves the original bytes, escapes substituted credentials as JSON,
retains exact numeric values, rejects malformed references without echoing
configuration, and never recursively expands credential contents. Tests also
cover JSON-escaped reference markers and opaque configurations without references.

This helper is not yet connected to submission, deployment or recovery. It does
not establish secret-management completion: coordinator-owned resolution,
reference-only persistence, secure worker delivery and redaction remain open.

### Submission-time secret validation

Normal and savepoint-upgrade submissions now validate environment references in
operator and named-DLQ configurations on the coordinator before reserving names
or writing metadata. Missing variables and malformed references return
`ErrInvalidConfig`; temporary resolved byte slices are cleared and never replace
the supplied graph. Regression tests verify both rejection paths leave no job
reservation or metadata, and successful submission preserves the original
references in both the job metadata and separate configuration record.

Deployment-time delivery is still pending. This change validates references but
does not yet supply resolved configurations to worker factories; do not treat it
as end-to-end secret support. Recovery must reconstruct runtime-only credentials
without modifying persisted task descriptors, and worker errors need redaction.

### Runtime credential snapshot lifetime

Submission now retains a coordinator-local snapshot of expanded configurations,
separate from `JobMeta`, so later retries can use submission-time values. The
lookup reads one environment snapshot per submission. Normal and savepoint
submissions install the cache only with their in-memory job ownership; rejected
submissions and failed persistence clear temporary values. Terminal transitions
and leadership recovery clear the cached byte slices. A regression test changes
the environment after submission, verifies the original cached value, then
checks terminal cleanup removes the entry and zeroes its owned bytes.

Worker deployment does not consume this cache yet. Reconstruction after
coordinator recovery and secure delivery/redaction remain required. Clearing
owned byte slices is lifecycle hygiene, not a guarantee that Go runtime memory,
environment strings or operating-system dumps contain no other copies.

### Recovery reconstruction and isolated delivery copies

Before publishing a recovered deployment, the scheduler reconstructs a missing
runtime credential snapshot from the replacement coordinator's environment.
Missing required references fail that job before assignments or deployment
commands are published. Every coordinator eligible for leadership therefore
needs the job's required environment variables; values cannot be recovered from
metadata because only references are persisted.

The resolved-task copy helper gives each task and named DLQ independent config
buffers. Tests verify reconstruction, missing-variable failure, unchanged job
metadata, and that clearing one delivery copy changes neither sibling tasks nor
the runtime cache. The helper is not yet used to send credentials: authenticated
connection binding and worker error redaction must be completed first.

### Worker connection provenance

Worker registration now retains whether the current reverse RPC connection was
bound to the same nonempty verified client-certificate identity. This state is
installed under the registration ownership lock, excluded from metadata and
JSON, and cleared when that exact connection disconnects. A new plaintext
registration replaces rather than inherits the earlier connection's verified
state. Tests cover replacement, disconnect, metadata round-trip, wrong identity,
missing connection and missing session lifetime.

This is provenance for the upcoming credential-delivery gate; it does not yet
send resolved configurations or claim worker-side redaction is complete.

### Credential-aware diagnostic filtering groundwork

The secret resolver can now return the exact substituted values, including
fallback defaults, without collecting unrelated environment entries. This is
needed because a connector may report just the token from a larger configured
string such as `Bearer ${TOKEN}`. Failed resolution returns neither partial
configuration nor partial credential lists.

`secretconfig.Redactor` filters literal, JSON-escaped, URL-escaped and standard /
URL-safe base64 forms, handles overlapping credentials longest-first, and retains
`errors.Is` / `errors.As` classification through sanitized error wrappers. Tests
cover those forms, concurrent use, defaults, duplicates and partial failure.
Callers must separately sanitize panic stacks or other unwrapped fields.

This remains groundwork: worker logger, task-status and DLQ integration is not
yet enabled. Arbitrary custom connector transformations or independent logging
cannot be covered merely by filtering known representations.

### Structured log filtering and task logger propagation

Task slots now accept the worker's task-scoped logger instead of silently
creating a separate unfiltered runtime logger. The worker executor passes the
same logger used for factory contexts into its task slot. A real source-failure
regression test confirms runtime diagnostics go through the supplied credential
filter while retaining task identity.

`Redactor.LogWriter` filters decoded structured fields (including context,
nested values and messages), preserves exact JSON numbers and log severity,
and emits a constant safe diagnostic for malformed input. It reports destination
write failures. Filtering decoded values avoids corrupting JSON when credentials
contain quotes, newlines or backslashes.

Automatic construction of per-task filtered loggers from delivered credentials
is still pending, along with task-status, panic-stack and DLQ redaction. No
resolved credentials are being dispatched yet.
