# Selecting managed state storage

The node configuration supplies defaults for **new structured job submissions**
to that coordinator:

```yaml
state:
  default_backend: hashmap  # pebble by default
  hashmap:
    max_memory_mb: 256      # logical key/value payload per managed instance
  pebble:
    data_dir: /var/lib/wire/state  # a worker-local root, not coordinator metadata
```

Both `Process` and window operators receive the resolved selection. Sources,
sinks and ordinary maps are not given a state backend. Registered Process/window
implementations must support Wire's backend factory injection to honor a selected
backend; the SDK adapters do. This does not configure application-owned storage.

The coordinator resolves only omitted operator specifications, before writing
job metadata. Explicit `SetStateBackend` SDK selections are preserved. The
resolved graph is persisted, so changing the node default does not change
existing jobs on recovery or worker replacement. Each managed instance gets its
own backend; the worker adds job/operator/subtask/attempt isolation beneath the
Pebble root. Provision that root as writable on every eligible worker.

Two coordinator CLI flags override node file/environment settings:

```sh
wire --config wire.yaml --state-backend hashmap \
  --state-hashmap-max-memory-mb 128
```

`WIRE_STATE_BACKEND` is the WIP-18 alias for `state.default_backend`.
`WIRE_STATE_DEFAULT_BACKEND` also works through the normal WIP-13 field mapping;
setting both to different values is rejected. Use
`WIRE_STATE_HASHMAP_MAX_MEMORY_MB` for the limit and `WIRE_STATE_PEBBLE_DATA_DIR`
for the worker-local root. Node precedence is defaults, files, environment,
then explicitly supplied CLI flags. Unchanged CLI defaults do not replace file
or environment values. Unknown backend names, negative limits and limits that
cannot be converted to bytes are rejected before node startup.

A zero HashMap limit explicitly means unlimited. The configured limit counts key
and value bytes only: Go objects, map/index storage, iterator copies and snapshot
buffers consume additional memory. It is not a worker RSS cap or aggregate
admission budget. Aggregate admission and the full pipeline-YAML/CLI precedence
contract remain tracked in [WIP-18's audit](trds/WIP-18/completion.md).

## Defaults and compatibility

The node default is Pebble, with a worker-local root at `/var/lib/wire/state`.
This default becomes explicit in newly submitted jobs. Existing persisted jobs
are not rewritten. Programmatic coordinators that do not supply
`CoordinatorConfig.DefaultStateBackend` retain existing graph behavior.
MiniCluster uses HashMap with a 256 MiB logical payload limit per managed
instance; call `SetStateBackend` on its returned environment to override it.
Ordinary `sdk.New()` continues to default to Pebble.

Backend migration is not supported. A savepoint upgrade that changes a managed
operator from Pebble to HashMap, or the reverse, is rejected before the successor
is deployed. Omitted legacy backend specifications mean Pebble. A different
Pebble storage directory is allowed because it changes placement rather than
the snapshot format. If the node default changed since a savepoint was taken,
select the original backend explicitly when submitting its successor.

Coordinator metadata and checkpoint replica storage are separate from managed
operator state. Selecting HashMap does not make a cluster disk-free or disable
checkpoint replication. See [state storage](state-backend.md) and
[storage security](storage-security.md) for those boundaries.
