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
| Encryption at rest strategy (§1.3) | Pending: document encrypted storage for coordinator metadata, worker state, checkpoints, and backups, including operator responsibilities. |
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
