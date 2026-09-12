# Configuration loading and validation

Use `--config first.yaml,second.json` to merge files in order. Only explicitly
set flags override file values. The default missing `.config/config.json` is
skipped; other missing files and unsupported extensions fail loading.

Environment substitution applies to string fields after decoding. `${VAR}`
fails if unset; `${VAR:-fallback}` uses the fallback only when unset. A variable
set to an empty string remains empty. Substitution does not parse booleans,
integers, or durations, and does not implement automatic `WIRE_*` overrides.
Unknown keys are currently ignored. Durations must be quoted strings such as
`50ms`; numeric duration values fail loading.

After loading and CLI overrides, `WireConfig.Validate` aggregates these rules:

- Mode is `coordinator`, `worker`, or empty.
- Worker mode requires a nonempty `worker.coordinator_addr` and positive
  `worker.task_slots`.
- Each TLS certificate/key pair must either both be set or both be empty.
- Configured TLS certificate, key, CA, and authentication paths must exist.
  This check does not validate certificate contents or ensure a regular file.
- Write-queue capacity, batch size, and timeout must be nonnegative; batch
  size must not exceed capacity.
- Election backend is `noop`, `filelock`, or empty.

These checks do not prove address reachability or enable configured features.
Runtime TLS/authentication remain unwired. Filesystem permission errors are
not classified as missing files by the current validation routine.

For exact diagnostic messages, see
[`internal/config/validate.go`](../internal/config/validate.go). The field table
and checked runnable configuration files are linked from
[the WIP-13 reference](trds/WIP-13/README.md).
