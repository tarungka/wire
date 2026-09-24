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
| Endpoint RBAC (§3.6) | Authenticated HTTPS acceptance covers every registered coordinator route, all roles, HEAD variants, public probes, escaped paths and canonical redirects; the test enforces route-inventory coverage. A subprocess acceptance test also verifies the production metrics listener stays public while the API rejects anonymous requests. |
| Brute-force protection (§6) | Bounded global authentication admission added (20 burst, 10 checks/second). Document operational behavior and test concurrent admission. |
| Authentication logs (§7.3) | Successful identity and failed source/Basic username logging added. Add capture tests proving passwords and API keys never appear. |
| Connector secret substitution (§3.7) | Implemented for structured JSON connector settings: submission-time snapshots, missing-variable rejection, reference-only metadata, recovery reconstruction and mTLS/capability-gated worker delivery. Live worker acceptance passes; complete recovery acceptance remains. |
| Credential redaction (§3.7) | Implemented runtime diagnostic filters and reference-only persistence; authenticated HTTPS tests verify submission/list/detail/task projections omit configs and credentials. Complete recovery and broader diagnostic acceptance remain. |
| Certificate/auth revocation (§4.2, §8.1) | Pending: restart/revocation integration tests and operational instructions. Rotation automation is explicitly out of scope. |
| Encryption at rest strategy (§1.3) | Documented in [storage security](../../storage-security.md): all runtime storage surfaces, temporary files, backups, key rotation and operator acceptance checks. Actual encrypted-volume deployment remains an operator verification requirement. |
| Flag/config documentation (§1.4) | Runtime guide maps actual security flags and config-only fields. The secure-cluster script generates certificate/config files and verifies a real two-worker startup with OpenSSL; HA takeover and data/recovery acceptance are separate tests. |
| Unit coverage (§8) | Measured 100% statement coverage of every function in http_auth.go in the race-enabled authentication acceptance run. This is not a claim of full-system coverage. |
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

### Task-status and DLQ diagnostic redaction

Task descriptors can carry runtime-only redaction values for secure deployment
copies. Worker admission builds an immutable task redactor. Failure status RPCs
filter error messages and panic stacks, observer Error() text is sanitized, and
the final worker failure log uses a filtered error. Error wrapping preserves
checkpoint-unavailable classification. Trusted observers must not unwrap and
serialize original error fields themselves.

Worker error policies now supply the same filter for DLQ diagnostic strings.
Both synchronous destinations and channel-based DLQs retain the original record,
retry count and operator identity. Tests exercise real status RPC serialization,
panic stacks, observer diagnostics, recovery classification and both DLQ paths.

The coordinator still does not dispatch resolved configurations. Automatic
filtered-logger construction, secure connection gating and worker capability
negotiation remain prerequisites; old workers must not receive credentials under
an assumption that they implement these new diagnostic protections.

### Automatic task logger filtering

A task with runtime redaction values now automatically receives a filtered logger
in both worker-managed execution and direct executor calls. Factory contexts and
the task slot share that filtered logger. The adapter retains the original
logging destination, severity, structured fields and exact numeric values;
unfiltered context is not reattached and destination sampling is not repeated.
The source-failure regression now passes an ordinary logger and verifies both
factory diagnostics and runtime failures are filtered automatically.

Caller-supplied logger hooks and output destinations are trusted application
code; they must not independently emit credentials. This filtering cannot
protect a connector that deliberately writes raw configuration through an
unrelated logger. Secure deployment gating and capability negotiation are still
pending, so resolved configurations remain undispatched.

### Secure connector credential delivery

Resolved task copies are now dispatched after reference-only assignment metadata
is durable. Each affected worker must have a current certificate-bound RPC peer,
reservation support and the new secret-config capability. Capability state is
not recovered from metadata. The scheduler places restricted tasks first on
eligible secure capacity, respects per-worker slot counts, and rechecks session
ownership before publishing. Secret payloads never enter fallback command queues.
Only each task's actual substituted values accompany its runtime config for
redaction, in deterministic order. Deployment RPC errors and checkpoint failure
reports are filtered too.

The live mutual-TLS acceptance test now runs a real worker factory with a
coordinator-resolved credential and completes its job. It inspects job, config
and assignment records for plaintext leaks. Scheduler/RPC tests reject plaintext,
old-capability and command-fallback workers; verify submission-time snapshot
values survive environment changes; and check constrained mixed-cluster placement.
The runtime guide documents required environments, rolling upgrades, pending jobs
without eligible capacity, and trusted connector/logging responsibilities.

Remaining security work includes the complete HTTP endpoint/credential/certificate
acceptance matrix, API credential-field redaction, authentication coverage and
end-to-end recovery/revocation verification. This is not full WIP-17 completion.

### HTTPS inspection projection and authentication validation

The job API already projects status/task metadata without exposing connector
configuration. A new authenticated HTTPS test exercises submission, listing and
inspection with nested literal credentials and an environment reference. It
checks response text, encoded config/graph forms and configuration field names,
while confirming non-sensitive task details remain present and stored graph
bytes remain unchanged. No new config-bearing API surface was added.

Authentication regression tests now cover duplicate/empty/oversized identities,
duplicate and malformed API keys, conflicting credentials, excess users,
unreadable/oversized/malformed files and unchanged handlers after failed policy
installation. Audit tests verify raw peer attribution and rejection of spoofed
X-Forwarded-For attribution.

The race-enabled authentication test run reports 100% statement coverage for
ConfigureAuth, allow, user, apiRoleAllowed and authenticate, and 97.2% for
readAPIAuth. Its remaining uncovered statement is the dummy bcrypt generation
error return. The explicit 100% target is therefore still open; these numbers
also do not replace the full real-route authorization acceptance matrix.

### Real HTTPS endpoint authorization matrix

`TestHTTPSRolesCoverRegisteredRoutes` exercises every coordinator route registered
in `http.go` over HTTPS for anonymous, viewer, operator and admin callers. It also
checks HEAD variants of GET routes, public probes, operator-only savepoint reads,
checkpoint inspection and admin-only node deletion. An AST-derived inventory
comparison fails if a registered route lacks an explicit authorization case.
Authorized requests retain the actual endpoint's normal validation/not-found
behavior; rejected requests return 401 or 403 before the endpoint runs.

Additional cases cover escaped savepoint/cluster paths, encoded slashes and a
canonical-path redirect followed by another authorization check. These tests and
the live HA authentication/takeover test pass with the race detector; coordinator
lint is clean. The separate metrics listener and remaining certificate/auth
combinations still need their own acceptance evidence.

### Authentication loader correctness and coverage target

Header-only bcrypt validation accepted unusable salts/digests. The loader now
checks canonical bcrypt encoding and supported 2/2a/2b/2y formats before installing
credentials. Regression fixtures demonstrate those malformed hashes pass
bcrypt.Cost but are rejected by the loader. Unknown-user password checks reuse
the highest-cost configured password hash instead of generating an unrelated
startup hash; matching that hash's password never authenticates an unknown
identity. An API-key-only file has no valid Basic identities.

The race-enabled authentication acceptance run now measures 100% statement
coverage for every function in http_auth.go: ConfigureAuth, readAPIAuth,
validBcryptEncoding, allow, user, apiRoleAllowed and authenticate. This satisfies
the authentication-logic statement target, not whole-package coverage or the
remaining end-to-end certificate/recovery acceptance requirements.

### HTTPS certificate acceptance

Real HTTPServer listeners now verify certificate and API authentication as
independent layers: a valid client certificate without valid API credentials
gets 401, while missing, expired, untrusted or wrong-purpose client certificates
fail TLS even with a valid API key. Client-side checks reject expired/untrusted
server certificates and hostname mismatches. TLS 1.2 is refused and successful
requests assert TLS 1.3 with an allowed TLS 1.3 cipher suite.

A listener replacement test admits the old client issuer before restart and
only the new issuer afterwards, while keeping server identity unchanged. This
is trust-anchor replacement, not CRL/OCSP or individual-certificate revocation.
The runtime guide explicitly describes that boundary and existing-session
handling. These HTTPS tests do not prove the separate RPC/data/replica expiry
or credential recovery requirements; those acceptance gates remain open.

### Separate metrics listener acceptance

`TestMetricsListenerRemainsPublicWithAPIAuthentication` runs production
observability.Init in an isolated process and scrapes its real listener while
an authenticated coordinator API returns 401 to anonymous requests. Both
anonymous and invalid-Bearer scrapes return Prometheus data over plain HTTP.
This verifies the explicitly public metrics contract; it does not claim API
TLS or RBAC applies to metrics. The runtime guide now states the separate
listener's network/proxy protection requirements.

### HTTP security CLI wiring

The walkthrough audit found that the proposal's HTTP TLS and auth flags were
absent despite their config-file runtime support. The CLI now defines and maps
`--auth`, `--http-cert`, `--http-key`, `--http-ca-cert` and
`--http-verify-client`. A command-package regression test uses the actual flag
parser and config overlay to verify unchanged defaults preserve file settings
and explicit values override them, including an explicit false boolean.
The generated reference and runtime guide list the actual security flags and
config-only fields, and distinguish historical nonexistent node flags.
The full multi-node walkthrough is still pending.

### Reproducible secured cluster startup

[The walkthrough](../../secure-cluster.md) builds the real command binary and
runs a coordinator with file-lock election plus two workers, using separate
HTTP/node CAs and unique worker identities. The Python standard-library script
generates private test files, configures HTTPS credentialed discovery, RPC mTLS
and worker data/replica mTLS, and waits for both workers to register ALIVE.
OpenSSL s_client verifies IP SANs, trust and TLS 1.3 on all six listeners, checks
TLS 1.2 protocol rejection and missing-client-certificate rejection. The API
returns 401 with a valid client certificate but no API credential.

This is startup/transport evidence, not an archive upload or data-frame test.
The full testssl.sh cipher scan and remaining recovery/authorization acceptance
gates remain open. Generated keys, binary, logs and runtime data stay outside
the repository; the retained acceptance record contains no credentials.

### Authenticated upstream ownership

The coordinator now includes each upstream task's assigned worker ID while
resolving deployment addresses under the placement lock. Both early worker
registration (before checkpoint fetch) and direct task-executor registration
install an immutable task/partition/worker policy on the receiving queue.
Peer-TLS deployments require ownership metadata; an old coordinator's missing
field is rejected instead of weakening the policy. Plaintext mode retains
task/partition validation without claiming authenticated ownership.

A real mTLS mux test uses a valid client certificate to claim a source assigned
to another worker, an unexpected partition and an unknown source. Each is
rejected before input queueing, then a legitimate source succeeds on the same
connection. Re-registering the target with a different owner rejects the old
worker. Address-resolution tests also assert the coordinator supplies ownership.
