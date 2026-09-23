# Configuration loading and validation

Load order is defaults, files in argument order, `WIRE_*` environment overrides,
`${VAR}` substitution in string values, then explicitly supplied CLI flags.
`--config first.yaml,second.json` merges both files. Only the missing default
`.config/config.json` is skipped; other missing files and unsupported extensions
fail. `Load` decodes configuration; the CLI applies flags and calls `Validate`.

## Environment

Every system-file leaf has an environment override: uppercase the dotted key,
replace dots with underscores, and prefix `WIRE_`. For example:

```sh
export WIRE_MODE=worker
export WIRE_WORKER_COORDINATOR_ADDR=localhost:4002
export WIRE_WORKER_TASK_SLOTS=8
export WIRE_HEARTBEAT_INTERVAL=5s
export WIRE_WORKER_COORDINATOR_SEEDS='["localhost:4002"]'
```

Booleans and numbers are parsed as their field types, durations use Go duration
strings, and lists use JSON string arrays. An empty variable is an explicit
value, not absence. Unrelated environment names are ignored. The `--config`,
`--version`, and metrics flags are CLI-only, with no generated environment key.
Explicit flags win over environment overrides; flag defaults do not.

Substitution works in every system string field and string-list element,
including HA seeds, Kubernetes settings and replica storage paths. `${VAR}`
fails when unset; `${VAR:-fallback}` uses the fallback only when unset, not when
set to empty. Replacement values are not recursively expanded. Substitution
itself does not parse numbers or durations; use the corresponding `WIRE_*`
override for those fields. Pipeline connector configuration is a separate API:
see [the YAML parser reference](../sdk/pipeline_yaml.md).

## Schemas and semantic checks

The [node schema](schemas/wire.schema.json) and
[pipeline schema](schemas/pipeline.schema.json) are JSON Schema 2020-12 documents
for editor/CI authoring checks on JSON or YAML converted to JSON. They reject
unknown keys; the node loader still ignores unknown keys for compatibility.
The node schema describes partial files before default/overlay merging. It is
structural, not a replacement for semantic validation or runtime capability
checks. The pipeline schema leaves connector and transform `config` objects to
the parser/factories, which validate their types, expressions and graph edges.

After merging, the node validator collects errors for:

- Frame sizes below five bytes (type plus CRC).
- Nonpositive heartbeat interval, timeout not greater than interval, or negative
  maximum failures.
- Negative checkpoint minimum pause, nonpositive timeout, negative consecutive
  limit, or failure rate outside `[0,1]` (including NaN/Inf).
- Replica listeners without positive concurrency or nonempty store, artifact
  and staging roots.
- Negative task buffers, upload concurrency or drain timeout. Zero selects the
  lower-level default where supported.
- Unknown modes; workers without a coordinator address or seed, nonpositive
  slots, or HA seeds without an epoch path.
- Unpaired TLS certificate/key files, and missing configured certificate, key,
  CA or authentication paths. This checks existence, not certificate contents;
  permission errors are not currently classified as missing files.
- Negative write-queue capacity, batch size or timeout; batch size above capacity.
- Unknown election backends. Allowed: empty, `noop`, `filelock`, `kubernetes`.
- Kubernetes leases not satisfying whole-second lease duration greater than
  renew deadline greater than positive retry period (lease seconds must fit
  int32), or coordinators without routable HTTP/RPC advertised addresses.

Exact diagnostic strings are in [validate.go](../internal/config/validate.go).
Address reachability and free ports are runtime checks. Node TLS is active;
HTTP TLS, authentication, and write-queue tuning include settings not yet wired
into the runtime. Accepting them does not enable security or tuning features.
See [WIP-17](trds/WIP-17/README.md) for the security implementation work.

The [generated field reference](configuration-reference.md) records every
accepted field/default/flag mapping. Regenerate it with
`go test ./internal/config -run TestConfigurationReference -update-config-reference`.
Regenerate the node schema with
`go test ./internal/config -run TestConfigurationSchema -update-config-schema`.
Both files have drift checks; the pipeline schema has a YAML field-coverage test.
