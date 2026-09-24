# System configuration reference

Generated from the current config types, defaults, and CLI mapping. Regenerate with
`go test ./internal/config -run TestConfigurationReference -update-config-reference`.

Load order is built-in defaults, configuration files in argument order, string
environment substitution, then explicitly supplied CLI flags. Only the missing
default `.config/config.json` file is ignored. Other missing files are errors.
Duration values are strings such as `50ms`; bare numeric durations are rejected.
Loading does not itself run semantic validation: the CLI applies overrides and
then calls Validate. Unknown fields are currently ignored by the loader.

This table describes accepted configuration, not runtime feature availability.
HTTP authentication and TLS settings are wired into runtime startup. See
[runtime TLS](runtime-tls.md) for their independent security boundaries.
Write-queue settings include fields that are not wired into the runtime.

| Field | Type | Default | CLI override |
| --- | --- | --- | --- |
| `heartbeat.interval` | duration string | `5s` | — |
| `heartbeat.timeout` | duration string | `30s` | — |
| `heartbeat.max_failures` | int | `0` | — |
| `checkpoint.min_pause` | duration string | `0s` | — |
| `checkpoint.tolerable_failure_rate` | float64 | `0` | — |
| `checkpoint.max_consecutive_failures` | int | `0` | — |
| `checkpoint.timeout` | duration string | `10m0s` | — |
| `task_slot.input_buffer_size` | int | `1024` | — |
| `task_slot.output_buffer_size` | int | `1024` | — |
| `task_slot.alignment_buffer_size` | int | `4096` | — |
| `task_slot.checkpoint_upload_concurrency` | int | `1` | — |
| `task_slot.drain_timeout` | duration string | `5s` | — |
| `mode` | string | `coordinator` | `--mode` |
| `listen` | string | `:4002` | `--listen` |
| `node.rpc_advertise_addr` | string | `""` | — |
| `node.id` | string | `""` | `--node-id` |
| `node.data_dir` | string | `data/coordinator` | `--coordinator-data-dir` |
| `node.store_db` | string | `pebble` | — |
| `node.debug` | bool | `false` | `--debug` |
| `http.addr` | string | `:4001` | `--http-listen` |
| `http.adv_addr` | string | `""` | — |
| `http.allow_origin` | string | `""` | — |
| `http.tls.cert` | string | `""` | `--http-cert` |
| `http.tls.key` | string | `""` | `--http-key` |
| `http.tls.ca_cert` | string | `""` | `--http-ca-cert` |
| `http.tls.verify_client` | bool | `false` | `--http-verify-client` |
| `http.tls.verify_server_name` | string | `""` | — |
| `node_tls.cert` | string | `""` | `--node-cert` |
| `node_tls.key` | string | `""` | `--node-key` |
| `node_tls.ca_cert` | string | `""` | `--node-ca` |
| `node_tls.verify_client` | bool | `false` | `--node-verify-client` |
| `node_tls.verify_server_name` | string | `""` | — |
| `auth.file` | string | `""` | `--auth` |
| `write_queue.capacity` | int | `1024` | — |
| `write_queue.batch_size` | int | `128` | — |
| `write_queue.timeout` | duration string | `50ms` | — |
| `write_queue.transactional` | bool | `false` | — |
| `election.kubernetes.api_server` | string | `""` | — |
| `election.kubernetes.namespace` | string | `""` | — |
| `election.kubernetes.lease_name` | string | `wire-coordinator` | — |
| `election.kubernetes.token_file` | string | `""` | — |
| `election.kubernetes.ca_file` | string | `""` | — |
| `election.kubernetes.lease_duration` | duration string | `10s` | — |
| `election.kubernetes.renew_deadline` | duration string | `6s` | — |
| `election.kubernetes.retry_period` | duration string | `1s` | — |
| `election.backend` | string | `noop` | `--election-backend` |
| `election.lock_path` | string | `data/coordinator/leader.lock` | `--election-lock-path` |
| `worker.peer_tls.cert` | string | `""` | — |
| `worker.peer_tls.key` | string | `""` | — |
| `worker.peer_tls.ca_cert` | string | `""` | — |
| `worker.discovery_http.ca_cert` | string | `""` | — |
| `worker.discovery_http.client_cert` | string | `""` | — |
| `worker.discovery_http.client_key` | string | `""` | — |
| `worker.discovery_http.api_key_file` | string | `""` | — |
| `worker.discovery_http.username` | string | `""` | — |
| `worker.discovery_http.password_file` | string | `""` | — |
| `worker.coordinator_seeds` | slice | `[]` | — |
| `worker.epoch_path` | string | `data/worker/epoch` | — |
| `worker.checkpoint_replica.listen_addr` | string | `""` | — |
| `worker.checkpoint_replica.advertise_addr` | string | `""` | — |
| `worker.checkpoint_replica.store_root` | string | `""` | — |
| `worker.checkpoint_replica.artifact_root` | string | `""` | — |
| `worker.checkpoint_replica.staging_root` | string | `""` | — |
| `worker.checkpoint_replica.concurrency` | int | `1` | — |
| `worker.coordinator_addr` | string | `""` | `--coordinator-addr` |
| `worker.worker_id` | string | `""` | `--worker-id` |
| `worker.listen_addr` | string | `:4003` | `--worker-listen` |
| `worker.task_slots` | int | `4` | `--task-slots` |
