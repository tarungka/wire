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

This change covers coordinator HTTPS and worker-to-coordinator RPC. It does
not implement application authentication, RBAC, certificate-to-worker identity
mapping, or a distributed shuffle data plane. Metrics use their separate HTTP
listener. TLS does not establish authorization for individual job operations.
