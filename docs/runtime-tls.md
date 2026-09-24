# Runtime TLS

Configure coordinator HTTPS and node RPC TLS independently in the system file:

```yaml
http:
  addr: ':4001'
  tls:
    cert: /etc/wire/http.crt
    key: /etc/wire/http.key
node_tls:
  cert: /etc/wire/coordinator.crt
  key: /etc/wire/coordinator.key
  ca_cert: /etc/wire/ca.crt
  verify_client: true
```

The coordinator serves TLS 1.3 on its HTTP and worker-RPC ports when configured.
For HTTPS client certificate verification, also set `http.tls.verify_client`
and `http.tls.ca_cert`. Client verification requires an explicit CA file.
Invalid certificate/key files fail startup; there is no plaintext fallback.
Absent TLS settings preserve plaintext operation.

Configure workers with the CA and, for mutual TLS, their own certificate/key:

```yaml
mode: worker
worker:
  coordinator_addr: coordinator.example:4002
node_tls:
  ca_cert: /etc/wire/ca.crt
  cert: /etc/wire/worker.crt
  key: /etc/wire/worker.key
  verify_server_name: coordinator.example
```

Workers verify the server certificate against the configured roots, or system
roots when no CA file is specified. The server name defaults to the connection
hostname; `verify_server_name` overrides it without disabling verification.
Certificate rotation requires a restart.

Coordinator HTTPS and worker-to-coordinator RPC TLS apply on this branch.
RPC registration also checks verified worker certificate identity. File-backed
HTTP authentication and admin/operator/viewer authorization are configured with
`auth.file` (`--auth`). Metrics use their separate HTTP listener. Data-plane and
checkpoint-replica TLS, full client credential propagation and the remaining
security acceptance matrix are still tracked by WIP-17; this is not a claim that
every cluster surface is secured.

HA and standalone startup both load HTTP TLS and authentication before listening.
HA installs authentication around the leadership dispatcher, so standby redirects
and every replacement leader handler use the same policy and admission limiter.
TLS 1.3 is enforced on the HA listener too. An HTTPS request redirected to another
coordinator keeps HTTPS; nodes in the same HA cluster must use compatible HTTP
security settings. Auth files are loaded once at startup, not re-read on election.
Changing credentials or certificates requires restarting each coordinator.

## SDK submission client

Configure the submitting application separately from coordinator/worker listeners:

```go
env := sdk.New().SetMode(sdk.Cluster).
    SetCoordinator("https://coordinator.example:4001").
    SetCoordinatorSecurity(sdk.CoordinatorSecurity{
        CACert: "/etc/wire/ca.crt",
        APIKeyFile: "/run/secrets/wire-api-key",
    })
```

`ClientCert`/`ClientKey` optionally configure HTTPS client certificates. For Basic
authentication use `Username` and `PasswordFile` instead of `APIKeyFile`.
Credentials and CA files are loaded when execution begins. The same authenticated
client handles submission and status polling. An HTTPS URL is required when any
security option is supplied, hostname validation stays enabled, and TLS 1.3 is
required. Requests have a 30-second client timeout in addition to caller context
cancellation. Redirects are reported as errors, including after leadership loss;
submissions are not automatically replayed against a different node.

`ExportSubmission` stays offline and does not read these credential files.
Neither credential contents nor their paths are serialized into the job graph.
These options secure the submitting application's HTTP requests only; they do
not configure worker discovery, RPC TLS or operator HTTP connectors.
