# Safe rescale deployment

Follow-up to PR #212.

A rescale retains the previous configuration, default parallelism and checkpoint
in persisted rollback metadata. If the attempted deployment fails before all
new tasks reach RUNNING, recovery cancels that attempt and restores the original
topology using ordinary checkpoint restore. Reaching RUNNING accepts the new
topology and clears rollback metadata. This permits recovery from unsupported
opaque-state redistribution or an operator lacking KeyGroupStateRestorer; it does
not add support for redistributing those state formats. Three failed placement
attempts also arm rollback when the larger deployment cannot fit. Recovery then
uses the normal retry budget from #214; an exhausted budget leaves the original
configuration restored with the job FAILED. Job responses retain a
`rescale_failure` explanation until the next accepted rescale request.

Global rescale requests preserve source and sink parallelism, resolving inherited
values against the old job parallelism. Every operator Forward-connected to a
source or sink keeps that boundary's count as well. An all-Forward pipeline
therefore keeps its physical parallelism; only shuffle-separated processing
groups change. Explicit per-operator requests must still satisfy Forward edge
equality and are validated before changing the running job. To explicitly
change selected operators, use the existing rescale endpoint with:

```json
{"savepoint_id":"saved-id","operators":{"map-operator":8}}
```

The `operators` map and global `parallelism` are mutually exclusive. Only named
operators change; sources and sinks require explicit entries. Applications remain
responsible for source partitioning and sink concurrency when explicitly scaling
them. KeyBy SDK nodes inherit an input count unless explicitly configured.
Inputs with different counts use Rebalance before selecting the key; the outgoing
hash shuffle always partitions using the selected key.

Workers register incoming task routes before fetching state. Each registration reserves
queue capacity for its expected input count, including deployments exceeding 64
inputs. Excess streams are rejected without blocking the peer accept loop.
Failed initialization unregisters the route.

Tests cover opaque source rollback with restored offsets and original topology,
a six-second state fetch with live upstreams, source/sink count preservation,
durable rollback metadata, per-operator requests, and unequal KeyBy parallelism.
