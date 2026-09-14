# Safe rescale deployment

Follow-up to PR #212.

A rescale retains the previous configuration, default parallelism and checkpoint
in persisted rollback metadata. If the attempted deployment fails before all
new tasks reach RUNNING, recovery cancels that attempt and restores the original
topology using ordinary checkpoint restore. Reaching RUNNING accepts the new
topology and clears rollback metadata. This permits recovery from unsupported
opaque-state redistribution or an operator lacking KeyGroupStateRestorer; it does
not add support for redistributing those state formats.

Global rescale requests preserve source and sink parallelism, resolving inherited
values against the old job parallelism. Forward edges still require equal counts;
incompatible requests are rejected before changing the running job. To explicitly
change selected operators, use the existing rescale endpoint with:

```json
{"savepoint_id":"saved-id","operators":{"map-operator":8}}
```

The `operators` map and global `parallelism` are mutually exclusive. Only named
operators change; sources and sinks require explicit entries. Applications remain
responsible for source partitioning and sink concurrency when explicitly scaling
them. KeyBy SDK nodes inherit their input parallelism to compute keys before the
outgoing hash shuffle.

Workers register incoming task routes before fetching state. Streams can queue
under bounded backpressure while restoration takes longer than the transport's
registration timeout. Failed initialization unregisters the route.

Tests cover opaque source rollback with restored offsets and original topology,
a six-second state fetch with live upstreams, source/sink count preservation,
durable rollback metadata, per-operator requests, and unequal KeyBy parallelism.
