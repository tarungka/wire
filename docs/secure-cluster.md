# Secure local cluster walkthrough

This walkthrough uses the current binary, two workers and separate HTTP/node
certificate authorities. It verifies startup and transport boundaries, not
checkpoint recovery or transactional job correctness. Use an isolated machine
with Go (the version in go.mod), Python 3 with TLS 1.3, and OpenSSL 3 on PATH.
No third-party Python packages are required.

From the repository root:

```sh
go build -o /tmp/wire-security ./cmd
python3 scripts/security-smoke.py --binary /tmp/wire-security \
  --output /tmp/wire-security-example
```

The output directory must not already exist. Omitting `--output` uses a temporary
directory that is deleted on exit. With `--output`, keep the directory private:
it contains CA private keys, node private keys and an API credential. The script
uses mode 0700 for the directory and a 0077 umask for its files. These are local
test credentials, valid for two days, not credentials for a deployed cluster.

The script starts processes only on loopback, disables the separate public
metrics listener, strips inherited `WIRE_*` overrides, and stops its processes
on completion or failure. It reserves distinct ephemeral ports before startup;
another local process taking a released port causes a visible failure. On a
failed run with retained output, inspect the three node logs before retrying
with a new directory.

## What the commands provision

1. OpenSSL creates two RSA-2048 CA keys with CA constraints and signing usage.
   The HTTP CA signs an HTTPS server and an API client. The node CA signs the
   coordinator RPC server and separate `worker-a`/`worker-b` certificates.
2. Leaf certificates include `127.0.0.1` and `localhost` SANs. Worker certificates
   have both clientAuth and serverAuth EKUs and a Common Name equal to the worker
   ID. Each CSR is signed with a random serial. The exact OpenSSL commands are in
   [security-smoke.py](../scripts/security-smoke.py), in `certificate()` and the CA loop.
3. The coordinator JSON config enables HTTPS client verification, API auth,
   RPC client verification, file-lock leader election and explicit advertised
   addresses. Discovery uses the election epoch; the standalone noop mode is
   intended for direct RPC connections rather than this discovery walkthrough. Its generated
   auth file contains a viewer API key. Its metadata lives in a separate directory.
4. Each worker JSON config enables authenticated HTTPS seed discovery, RPC TLS,
   worker peer mTLS, a durable epoch path and separate checkpoint storage,
   artifact and staging directories. Discovery uses the API client certificate
   and viewer key; worker RPC/data/checkpoint connections use the worker's own
   node certificate.
5. All three processes run the same binary with their generated `--config` and
   `--metrics-enabled=false`. These exact configurations remain in the retained
   output and can be inspected without reconstructing fields from prose.

No shared worker private key is used. For a real deployment, replace loopback
SANs and addresses with the actual advertised DNS/IP identities, issue distinct
keys through your PKI, keep CA signing keys off runtime nodes, and place runtime
storage on protected volumes as described in [storage security](storage-security.md).
Do not use these test keys outside this local walkthrough.

## Acceptance evidence

The script waits for the authenticated cluster API to report both workers
ALIVE and the coordinator ready. This exercises real worker HTTPS discovery,
client credentials and mTLS RPC registration. It then verifies that a valid
HTTPS client certificate without an API credential gets 401.

For the HTTPS API, coordinator RPC, both data listeners and both checkpoint
replica listeners, OpenSSL `s_client` verifies the certificate chain and IP SAN,
negotiates TLS 1.3, confirms TLS 1.2 is rejected with a protocol-version alert,
and checks a missing client certificate is rejected with a certificate-required
alert on every listener.
The script fails on an unexpected result and writes `acceptance.json` only after
all checks pass. That file records the tested addresses; it is evidence from
this run, not a substitute for testing your deployment.

The handshake probes do not send Wire session negotiation, data frames or
checkpoint archives. Worker identity binding and authorized transfers have
separate Go integration tests. This walkthrough also does not test HA takeover,
container isolation, volume encryption, CRL/OCSP revocation or every cipher with
`testssl.sh`; see the [WIP-17 completion audit](trds/WIP-17/completion.md) for
remaining gates. The [runtime TLS guide](runtime-tls.md) explains flag mappings,
secret handling, trust rotation and the public metrics boundary.
