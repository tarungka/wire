# Runtime TLS

TLS protects data in transit. For local state, checkpoint archives and backups,
see [storage security](storage-security.md). Wire does not encrypt files itself.

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
Certificate rotation requires a restart. Wire does not perform CRL or OCSP
checks and does not offer individual certificate serial-number revocation.
Removing a compromised issuer from a CA file takes effect only after every
listener/client using that trust store restarts. Replace affected certificates
with ones signed by the replacement issuer and restart the relevant nodes;
leaving the old issuer trusted continues to admit its unexpired certificates.
Restart also terminates existing sessions, whose prior handshakes are not
revalidated by editing certificate files. Plan capacity and availability for
this operation; normal rolling renewal with overlapping trust is different
from immediately removing a compromised issuer.

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

## Authenticated worker leader discovery

Workers that discover an HA leader through HTTP need HTTP credentials separately
from their RPC certificate. Configure every eligible coordinator as an explicit
HTTPS seed:

```yaml
worker:
  coordinator_seeds:
    - https://coordinator-a.example:4001
    - https://coordinator-b.example:4001
  epoch_path: /var/lib/wire/worker-epoch
  discovery_http:
    ca_cert: /etc/wire/http-ca.crt
    api_key_file: /run/secrets/wire-discovery-key
    # client_cert/client_key: optional HTTPS client credentials
    # username/password_file: alternative to api_key_file
```

Use an API identity authorized to read cluster discovery (the viewer role is
sufficient). Discovery requests require TLS 1.3 and hostname verification. The
worker confirms readiness and epoch with the advertised leader itself. For HTTPS
or credential-configured discovery, that leader must match a configured HTTPS
seed; an unlisted host or plaintext hint never receives credentials. Bare leader
hints inherit the seed's HTTPS scheme. HTTP redirects are not followed. Configure
the full seed list on every worker so a legitimate takeover can be confirmed.
Legacy unauthenticated HTTP discovery remains available for development.

Public SDK application workers use the corresponding fields:

```go
sdk.WorkerConfig{
    WorkerID: "worker-a",
    CoordinatorSeeds: []string{
        "https://coordinator-a.example:4001",
        "https://coordinator-b.example:4001",
    },
    EpochPath: "/var/lib/wire/worker-epoch",
    DiscoverySecurity: sdk.CoordinatorSecurity{
        CACert: "/etc/wire/http-ca.crt",
        APIKeyFile: "/run/secrets/wire-discovery-key",
    },
    // RPCTLSConfig must be configured separately for the discovered RPC endpoint.
}
```

A direct `CoordinatorAddr` without seeds does not use HTTP discovery. Discovery
credentials are process-local and never become task/operator configuration.

## Worker data and checkpoint peers

Enable peer mTLS on every worker participating in secured data exchange or
checkpoint replication:

```yaml
worker:
  peer_tls:
    cert: /etc/wire/worker-a.crt
    key: /etc/wire/worker-a.key
    ca_cert: /etc/wire/worker-ca.crt
```

All three files are required together. Each worker certificate must support both
TLS server and client authentication. Its SANs must match the advertised data
and replica addresses (including IP SANs when dialing IP addresses). The configured
CA verifies both incoming client certificates and outgoing server certificates;
TLS 1.3 and hostname verification are mandatory. There is no plaintext fallback
for a configured TLS connection. Workers with no peer TLS configured retain the
development plaintext behavior; enabling only `node_tls` does not enable this
separate peer policy.

The policy applies to the data mux listener/dials, replica listener, every new
checkpoint upload connection, and checkpoint fetches during restore/rescale.
Failed upload connections do not cause later reconnects to lose TLS settings.
Assignment authorization still runs before checkpoint publication or fetch.

SDK workers can supply `WorkerConfig.PeerTLSConfig` with certificate/key,
`RootCAs`, `ClientCAs`, and `ClientAuth: tls.RequireAndVerifyClientCert`.
`InsecureSkipVerify` is rejected. RPC TLS and HTTP discovery security remain
separate settings. Certificates are loaded at startup; restart workers to rotate
credentials. Data-session negotiation also requires the verified certificate's
Common Name to equal the peer's claimed worker NodeID, in both connection
directions. Set the certificate Common Name to the configured worker ID. This
check runs before the negotiated identity or advertised peer address is trusted.
Checkpoint replica services also require a nonempty verified certificate Common
Name. Fetch requests must name that same worker before archive lookup or
assignment authorization. For uploads, the receiving replica supplies the
certificate-derived source worker ID to the coordinator, which checks it against
the checkpoint's task owner. The uploader cannot set this identity in the archive
request. Existing assignment and deployment checks still apply.

Upgrade coordinators before enabling this peer policy on workers: old coordinators
do not enforce the added uploader identity field. Plaintext development replicas
retain the prior authorization format. Data-stream task-assignment authorization
beyond the negotiated worker identity and the complete security acceptance matrix
remain tracked work.

## Connector environment references

Structured connector configurations can contain coordinator-side references in
JSON string values, including nested arrays and objects:

```json
{"url":"https://example.invalid/events","headers":{"Authorization":"Bearer ${API_TOKEN}"}}
```

Provision `API_TOKEN` in each coordinator's environment before starting it.
`${NAME}` is required; `${NAME:-default}` uses the default only when the variable
is absent (an explicitly empty value stays empty). Defaults are part of the
submitted configuration, so use them for nonconfidential settings. References
in object keys and nested reference expressions are rejected. Other opaque
connector configuration formats remain supported without secret substitution.

The coordinator validates references before accepting a job and keeps its
resolved snapshot in memory for retries. Job graphs and assignment metadata
retain the references. After coordinator recovery, the new leader resolves its
own environment; provision the same values on every eligible coordinator.
Missing variables fail the recovered job before it is deployed. Changing an
environment value does not rotate credentials within an already running leader's
job snapshot.

Secret-bearing tasks require a live mutually authenticated coordinator-worker
RPC session and a worker advertising secret-config support. Upgrade workers
before submitting such jobs. The scheduler uses eligible secure slots and keeps
the job pending if none are available. It sends runtime copies directly over the
captured RPC connection; it never places credentials in the heartbeat command
queue. Workers receive only the substituted values needed by that task for
filtering factory/runtime logs, failure reports, panic stacks and DLQ diagnostic
text. Ordinary record payloads are not rewritten.

Connector factories must not persist credentials in their checkpoint state or
emit them through independent loggers. Custom logger hooks, diagnostic observers
and output destinations are trusted application code; do not unwrap sanitized
errors and serialize the original fields. Arbitrary application encodings cannot
be made safe merely by filtering known credential representations. TLS and these
runtime protections do not encrypt local state or external sink contents.
