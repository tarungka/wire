# WIP-08 runtime contract

This contract specifies the current heartbeat and worker-loss behavior. It replaces the older count-only sender policy and the historical draft's ambiguous timing. The wire representation remains the WIP-07 msgpack RPC protocol.

## Configuration and authority

```yaml
heartbeat:
  interval: 5s
  timeout: 30s
  max_failures: 0
```

Both node modes load these settings from YAML/JSON. Interval must be positive, timeout must exceed interval, and max_failures must be nonnegative. Configure a consistent timeout across coordinators and workers; these settings are not negotiated with older peers. Zero failures means elapsed-time detection only. A positive value lets the worker terminate earlier after that many consecutive failed heartbeat attempts; one accepted response resets the count. The coordinator observes elapsed receipt time, not a worker's local RPC failure count.

The 2-second heartbeat RPC timeout is separate from the 30-second contact deadline. A request is bounded by both. Timestamps in payloads are diagnostic only: neither endpoint compares remote wall clocks. Local receipt/contact timestamps retain Go's monotonic clock information. Workers conservatively renew contact from the send time of a successful request, rather than its later reply time, so network round-trip delay cannot extend their processing authority beyond the coordinator's receipt-based timeout.

The coordinator checks health at most every 250ms (more frequently for short configured timeouts), independently of scheduling or deployment RPCs. A heartbeat arriving after expiry is refused even if the periodic detector has not run. Expired workers cannot refresh their authority with an old heartbeat; they must re-register. Heartbeats on an obsolete registration session are rejected, including legacy workers. Epoch checks precede liveness/capacity updates and command draining.

## Loss and recovery

A worker changes from ALIVE to LOST once per registration. Its available capacity becomes zero. Unfinished tasks on it become FAILED, and their active jobs enter FAILING. The scheduler cancels surviving old tasks and waits for their terminal reports before attempting checkpoint restoration elsewhere. Missing/lost workers do not hold recovery waiting for acknowledgements they cannot send.

Recovery retains the existing restart backoff, attempt limits, checkpoint integrity verification and transactional-sink fallback restrictions. No checkpoint or exhausted restart budget leads to FAILED, consistent with WIP-02/06 and the merged restart policy; the original draft's CANCELED label is superseded. With a usable checkpoint but no available workers, placement waits without consuming deployment attempts. Re-registration establishes fresh capacity and may make recovery schedulable again.

The worker has a process-wide contact watchdog spanning failed connection attempts, registration and session reconnects. A closed coordinator TCP/Yamux session cancels old executions and initiates reconnect promptly. Successful registration or an accepted heartbeat confirms contact. A newer epoch cancels old work before commands from that epoch can run. If contact is not restored before the deadline (or the configured consecutive failure limit is reached), admission stops, task contexts are cancelled, data, coordinator and checkpoint-replica sessions close, and Worker.Run returns ErrCoordinatorContactLost. The CLI exits nonzero for the supervisor to restart it. Cooperative task cleanup is joined within its existing drain/shutdown budgets; expiry closes transports before that join. Arbitrary user code that ignores cancellation cannot be forcibly interrupted inside a Go process.

## Heartbeat payload

Each heartbeat carries worker identity/epoch, sender timestamp, used/total slots, task summaries and the latest resource sample. Used slots include unconsumed reservations and handles still tearing down.

Task summaries carry job/task/attempt/epoch identity, lifecycle status and uptime. RecordsIn/BytesIn count data consumed by the operator chain; RecordsOut/BytesOut count logical data emitted to its output queue or successfully written by its terminal sink. Keys and values contribute bytes; wire framing does not. Retries do not double-count successful sink writes, and dropped records are not successful output. Counters reset for each new attempt. BackpressureMs reports operator-chain time waiting for output queue space since the preceding heartbeat request was built, including waits ended by cancellation. Watermarks, barriers and EOP do not count as records. These are health observations; fenced UpdateTaskStatus remains authoritative for task transitions.

Resource collection runs outside the heartbeat loop. It samples host CPU utilization between samples, host memory used/total, usage of the checkpoint-store filesystem (or working-directory filesystem without checkpoint storage), and this process's goroutine count. CPU and memory load fractions mirror those reports. These are host measurements visible to the process, not container quota accounting. SampledAt identifies the sample age. Unavailable names missing measurements explicitly; the first CPU sample has no preceding interval and is unavailable. A slow OS sample cannot block liveness: heartbeats carry the previous snapshot. Resources and task reports remain coordinator-internal and are not added to the public cluster response.

## State and metrics

Heartbeat receipt times, resource samples and health status are ephemeral. Production no longer flushes heartbeat keys periodically or serializes last_heartbeat into worker registration metadata. Legacy advisory keys can still be read by older code but are not trusted for recovery. Durable worker identity, task ownership, checkpoints and job transitions remain unchanged. A recovered coordinator starts with stale workers that must re-register.

The cluster status API adds `status: ALIVE|LOST` to worker summaries and retains last_heartbeat for diagnosis. Metrics are wired to the normal OTel/Prometheus provider:

| Metric | Meaning |
| --- | --- |
| wire_heartbeat_latency_ms | Heartbeat round-trip latency histogram, including failures, in milliseconds |
| wire_heartbeat_failures_total | Failed/rejected heartbeat attempts, excluding owner cancellation |
| wire_workers_alive | Currently registered, unexpired workers; callback removed on leadership exit |
| wire_workers_lost_total | Fresh registrations subsequently declared lost; repeated checks do not increment again |

The RPC HeartbeatTracker also uses elapsed receipt time for death detection; SUSPECT remains an intermediate diagnostic state. Production coordinator presentation uses ALIVE/LOST.
