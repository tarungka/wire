# Replayable custom file source

This example uses only public SDK imports. It demonstrates a bounded source,
versioned offsets, replay validation, named worker registration and graph export.
The sink prints records; stdout is **not transactional**, so job recovery may
print a record more than once.

From the Wire repository root:

```sh
printf 'hello\nworld\n' > /tmp/wire-input.txt
go run ./sdk/examples/file-connector -mode embedded -file /tmp/wire-input.txt
```

The output contains `hello` and `world` (runtime log messages may also appear).
To submit to a running coordinator, start this application worker first:

```sh
go run ./sdk/examples/file-connector -mode worker -worker-id files-1 \
  -rpc localhost:4002
```

In another terminal:

```sh
go run ./sdk/examples/file-connector -mode submit \
  -http http://localhost:4001 -file /tmp/wire-input.txt
```

For replicated checkpoints and failover, run a second application worker with a
unique `-worker-id`. Every eligible worker must have the immutable input file at
the same path and with the same bytes. The example deliberately rejects source
parallelism other than one; it does not partition a file between subtasks.

Exporting creates a job submission document without contacting the cluster:

```sh
go run ./sdk/examples/file-connector -mode export \
  -file /tmp/wire-input.txt > /tmp/file-job.json
wire jobs submit --file /tmp/file-job.json --coordinator http://localhost:4001
```

`Open` reads a maximum of 8 MiB, rejects lines reaching the scanner's 1 MiB token
limit, and computes a SHA-256 digest. `ReadBatch` returns at most 32 lines and
advances a consumed-line cursor. Checkpoints encode format version 1, the digest
and cursor. Recovery opens the file first, then validates the saved digest and
cursor before emitting records after that cursor. A changed, missing or truncated
file fails recovery; silently replaying different data would be incorrect.
Read-ahead after a checkpoint is replayed by a fresh instance, as the tests show.

This is an immutable-file teaching example, not a tailing or durable ingestion
service. It loads the file into memory, has no filesystem watch, and does not
implement log rotation, file locks or multi-file discovery. A real connector
must define its external retention and partition assignment contracts.

```sh
go test -race ./sdk/examples/file-connector
```

The tests cover checkpoint replay, rejection of changed data and invalid offsets,
cancellation, embedded output and exported graph contents. The production files
also build and run when copied into a separate module that depends on Wire;
they contain no `internal/` imports. See the
[connector development guide](../../../docs/connector-development.md) for the
runtime lifecycle and transactional sink requirements.
