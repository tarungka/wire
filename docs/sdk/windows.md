# Embedded event-time windows

A linear embedded pipeline can execute tumbling, sliding, or session windows
with `Aggregate`, `Reduce`, or `Apply`:

```go
env.AddSource(source).
    KeyBy(func(e sdk.Event) ([]byte, error) { return e.Key, nil }).
    Window(sdk.TumblingWindow(time.Second)).
    AllowedLateness(500).
    Aggregate(sdk.CountAggregator{}).
    AddSink(sink)
```

Set event timestamps in milliseconds. The source's `GenerateWatermark` is read
after each complete batch has passed through the pipeline. Returning nil from
`ReadBatch` ends input and flushes retained windows. An empty, non-nil batch
can advance watermarks without ending input. No wall-clock timer advances
watermarks while the source is blocked.

Each output carries the key and window end as EventTime, plus byte-string
headers `wire.window.start`, `wire.window.end`, and `wire.window.update`.
Aggregate returns accumulator results as Value. Reduce preserves the reduced
record's Value and user headers. Apply receives WindowInfo and the retained
records, then may produce zero or more results.

The watermark closes a window at its end and purges it at end plus allowed
lateness. Accepted late records update retained results. Records whose assigned
windows have all expired are dropped; named late side outputs remain unfinished.
A merged session can extend a previously emitted window and later emit an
update; old results are not retracted.

Current execution requires a single-source linear graph, parallelism 1, no
checkpoint interval, and no restart strategy. Unsupported settings fail before
connectors open. This path supports Map, FlatMap, Filter, KeyBy, and a terminal
sink alongside windows; Process and branched graphs are not supported here.

Window state is in memory with at most 100,000 retained windows per operator.
Apply buffers serialized records, with an 8 MiB limit per retained window;
it copies that buffer as records arrive and is unsuitable for very large
windows. These bounds are not a global memory budget. Aggregator/reducer output
sizes remain application-controlled. Parallel windows, persistent state,
checkpoint restore, side outputs, and integration with other PRs' error policies
remain future work. MiniCluster exercises this same embedded path.
