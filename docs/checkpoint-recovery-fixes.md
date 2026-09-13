# Checkpoint recovery failure handling

Follow-up to PRs #211 and #212.

- Upload errors, refused ACKs and report RPC failures share the configured consecutive checkpoint failure budget. Zero remains unlimited; a rejected report is logged and does not independently kill the task.
- Worker heartbeat expiry makes running/deploying jobs recoverable. Restart still waits for terminal status from live workers, but does not wait for an expired owner.
- Reconnect permits the configured task drain timeout plus five seconds for cleanup. Workers still refuse to reconnect if an old task does not join, preserving execution fencing.
- Source checkpoint triggers coalesce to the latest identity instead of canceling on a full mailbox. Unexpected task cancellation transitions running/deploying jobs to failure handling.
- Coordinator restart defaults are three attempts with exponential backoff starting at one second (capped at 64 times the base delay). CoordinatorConfig.RestartMaxAttempts and RestartBackoff customize these defaults. Counters and transition timestamps survive in job metadata.
- Each replica upload owns a fresh connection, so a peer restart cannot poison all future checkpoints.
- Checkpoint admission requires a replica for every task; missing assignments return HTTP 503 without reserving an in-progress checkpoint.

Regression tests cover refused ACK failure budgets, trigger saturation, expired worker recovery, restart limits/backoff, configured drain time, and replica peer restart. Coordinator and worker race suites pass.
