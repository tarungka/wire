# Security Model

> **Feature/Project:** `Security Model`
>
> **WIP ID:** `WIP-17`
>
> **Author:** `Tarun Ashok`
>
> **Status:** `Draft`
>
> **Created:** `2026-02-22`
>
> **Last Updated:** `2026-02-22`

### Revision History

| Version | Date | Author | Changes |
| -- | -- | -- | -- |
| 0.1 | 2026-02-22 | Tarun Ashok | Initial draft |

---

## 1. Overview

### 1.1 Problem Statement

Wire's technical documentation has empty sections for "Data Encryption" and "Secret Management" with questions but no answers. The codebase already has CLI flags for X.509 certificates (HTTP TLS, inter-node mTLS, client verification) in `cmd/init.go`, and an `--auth` flag pointing to an authentication file — but none of this is documented. Users cannot deploy Wire securely without understanding the security model.

### 1.2 Proposed Solution (Technical Summary)

Document Wire's layered security model: (1) TLS encryption for the HTTP API, (2) mutual TLS for inter-node (Raft + data) communication, (3) file-based authentication for API access, (4) role-based authorization for job management, (5) credential management for connector secrets via environment variable substitution, and (6) encryption at rest for checkpoints/state.

### 1.3 Goals & Non-Goals

| Goals (In Scope) | Non-Goals (Explicitly Out) |
| -- | -- |
| Document TLS configuration for HTTP API | OAuth2 / OIDC integration |
| Document mTLS for inter-node communication | Vault integration (deferred) |
| Define authentication file format | LDAP / Active Directory integration |
| Define RBAC model for API endpoints | Row-level security for state data |
| Document secret management for connectors | Key rotation automation |
| Document encryption at rest strategy | Hardware security modules (HSM) |

### 1.4 Success Metrics

| Metric | Current Baseline | Target | Measurement |
| -- | -- | -- | -- |
| Security features documented | 0 (empty sections) | 100% of existing security flags | Cross-ref with cmd/init.go |
| Cluster deployable with mTLS | Undocumented | Step-by-step guide | Manual walkthrough |
| Auth model defined | No | Complete RBAC spec | Doc review |

---

## 2. Architecture & System Design

### 2.1 Security Layers

```
┌─────────────────────────────────────────────────────────────┐
│                     Wire Security Model                      │
│                                                              │
│  Layer 1: Transport Security                                 │
│  ┌───────────────────┐  ┌────────────────────────────┐      │
│  │ HTTPS (Port 4001) │  │ mTLS (Port 4002)           │      │
│  │ - Server TLS      │  │ - Mutual authentication    │      │
│  │ - Optional mTLS   │  │ - Encrypted Raft + data    │      │
│  └───────────────────┘  └────────────────────────────┘      │
│                                                              │
│  Layer 2: Authentication                                     │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ File-based auth (--auth flag)                         │   │
│  │ - Username / hashed password / permissions            │   │
│  │ - API key authentication                              │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                              │
│  Layer 3: Authorization (RBAC)                               │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ Roles: admin, operator, viewer                        │   │
│  │ - admin: all operations                               │   │
│  │ - operator: job management, no cluster ops            │   │
│  │ - viewer: read-only access                            │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                              │
│  Layer 4: Secrets Management                                 │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ ${ENV_VAR} substitution in pipeline configs           │   │
│  │ - Credentials never stored in config files            │   │
│  │ - Compatible with K8s Secrets, Vault sidecar          │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                              │
│  Layer 5: Encryption at Rest                                 │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ - Pebble state: OS-level filesystem encryption        │   │
│  │ - S3 checkpoints: Server-side encryption (SSE-S3/KMS) │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Component Breakdown

**Component 1:** HTTP TLS (`--http-cert`, `--http-key`)
* **Responsibility:** Encrypt REST API traffic. Optionally verify client certificates (`--http-verify-client`).
* **Technology:** Go `crypto/tls`, X.509 certificates
* **Interactions:** All REST API endpoints served over HTTPS when configured.

**Component 2:** Node-to-Node mTLS (`--node-cert`, `--node-key`, `--node-verify-client`)
* **Responsibility:** Encrypt and authenticate all inter-node communication (Raft consensus + Yamux data streams) on port 4002.
* **Technology:** Go `crypto/tls` wrapping TCP listener in `internal/tcp/mux.go` (`NewTLSMux`)
* **Interactions:** Every Yamux session is wrapped in TLS. Both sides present certificates.

**Component 3:** Authentication File (`--auth`)
* **Responsibility:** Define users, credentials, and roles for API access.
* **Technology:** JSON file loaded at startup
* **Interactions:** HTTP middleware validates credentials on every request.

**Component 4:** Environment Variable Substitution
* **Responsibility:** Allow connector credentials to reference environment variables.
* **Technology:** `${VAR}` and `${VAR:-default}` syntax in YAML pipeline configs
* **Interactions:** Variables resolved at pipeline submission time. Resolved values never persisted.

### 2.3 Data Flow — Secure Cluster Setup

1. Generate CA certificate and per-node certificates (see Section 3.3).
2. Start Coordinator with `--http-cert`, `--http-key`, `--node-cert`, `--node-key`, `--node-verify-client`.
3. Start Workers with same node certificates. Workers connect to Coordinator via mTLS on port 4002.
4. Yamux sessions established over TLS — all Raft traffic and data shuffle encrypted.
5. External clients connect to Coordinator REST API via HTTPS on port 4001.
6. Clients authenticate via Basic Auth or API key (from auth file).
7. Coordinator validates role and authorizes the request.

```mermaid
sequenceDiagram
    participant C as Client
    participant H as Coordinator HTTP<br/>(port 4001)
    participant A as Auth Middleware
    participant R as RBAC Check
    participant Handler

    C->>H: HTTPS request + Authorization header
    Note over C,H: TLS handshake (optional mTLS)
    H->>A: Extract credentials
    A->>A: Validate (bcrypt compare<br/>or API key lookup)
    alt Invalid credentials
        A->>C: 401 Unauthorized
    else Valid credentials
        A->>R: Check role permissions
        alt Insufficient permissions
            R->>C: 403 Forbidden
        else Authorized
            R->>Handler: Forward request
            Handler->>C: Response
        end
    end
```

---

## 3. API Design

### 3.1 HTTPS Configuration

Already implemented in code. CLI flags:

```bash
wire \
  --http-cert /etc/wire/certs/http.crt \
  --http-key /etc/wire/certs/http.key \
  --http-ca-cert /etc/wire/certs/ca.crt \    # For client certificate verification
  --http-verify-client                         # Enable mutual TLS
```

When `--http-cert` and `--http-key` are set, the HTTP server binds with TLS. The advertised URL scheme changes from `http://` to `https://`.

### 3.2 Inter-Node mTLS Configuration

Already implemented in code (`internal/tcp/mux.go:NewTLSMux`). CLI flags:

```bash
wire \
  --node-cert /etc/wire/certs/node.crt \
  --node-key /etc/wire/certs/node.key \
  --node-ca-cert /etc/wire/certs/ca.crt \     # CA for verifying peer nodes
  --node-verify-client \                        # Require mutual authentication
  --node-verify-server-name wire-node           # Expected CN/SAN on peer certs
```

When configured, the TCP listener on port 4002 is wrapped in TLS via `NewTLSMux`. All Raft consensus messages and Yamux data streams are encrypted.

`--node-no-verify` disables certificate verification (for development only).

### 3.3 Certificate Generation Guide

```bash
# 1. Generate CA
openssl genrsa -out ca.key 4096
openssl req -new -x509 -days 3650 -key ca.key -out ca.crt \
  -subj "/CN=Wire CA"

# 2. Generate node certificate
openssl genrsa -out node.key 2048
openssl req -new -key node.key -out node.csr \
  -subj "/CN=wire-node"
openssl x509 -req -in node.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out node.crt -days 365 \
  -extfile <(echo "subjectAltName=DNS:wire-node,DNS:*.wire.local,IP:127.0.0.1")

# 3. Generate HTTP certificate (can reuse node cert or separate)
openssl genrsa -out http.key 2048
openssl req -new -key http.key -out http.csr \
  -subj "/CN=wire-api"
openssl x509 -req -in http.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out http.crt -days 365 \
  -extfile <(echo "subjectAltName=DNS:wire-api,DNS:*.wire.local,IP:127.0.0.1")
```

### 3.4 Authentication File Format

The `--auth` flag points to a JSON file:

```json
{
  "users": [
    {
      "username": "admin",
      "password_hash": "$2a$10$...",
      "role": "admin"
    },
    {
      "username": "operator",
      "password_hash": "$2a$10$...",
      "role": "operator"
    },
    {
      "username": "viewer",
      "api_key": "wk_live_abc123...",
      "role": "viewer"
    }
  ]
}
```

**Password hashing:** bcrypt (`$2a$` prefix). Generate with:
```bash
htpasswd -nbBC 10 "" "password" | cut -d: -f2
```

**API key format:** `wk_live_` prefix + 32 random alphanumeric characters.

### 3.5 Authentication Methods

| Method | Header | Example |
|--------|--------|---------|
| Basic Auth | `Authorization: Basic base64(user:pass)` | `Authorization: Basic YWRtaW46cGFzc3dvcmQ=` |
| API Key | `Authorization: Bearer wk_live_...` | `Authorization: Bearer wk_live_abc123` |

If `--auth` is not set, authentication is **disabled** (all requests allowed). This is suitable for development only.

### 3.6 RBAC Model

| Role | Job Submit | Job Cancel/Pause | Job View | Savepoints | Cluster Manage | Metrics |
|------|-----------|-------------------|----------|------------|---------------|---------|
| **admin** | Yes | Yes | Yes | Yes | Yes | Yes |
| **operator** | Yes | Yes | Yes | Yes | No | Yes |
| **viewer** | No | No | Yes | No | No | Yes |

| Endpoint | Required Role |
|----------|---------------|
| `POST /api/v1/jobs` | operator |
| `POST /api/v1/jobs/submit` | operator |
| `GET /api/v1/jobs` | viewer |
| `GET /api/v1/jobs/{id}` | viewer |
| `POST /api/v1/jobs/{id}/cancel` | operator |
| `POST /api/v1/jobs/{id}/pause` | operator |
| `POST /api/v1/jobs/{id}/resume` | operator |
| `POST /api/v1/jobs/{id}/savepoints` | operator |
| `GET /api/v1/cluster` | viewer |
| `DELETE /api/v1/cluster/nodes/{id}` | admin |
| `GET /healthz` | (public) |
| `GET /readyz` | (public) |
| `GET /metrics` | (public) |

### 3.7 Connector Secret Management

Pipeline configurations use `${ENV_VAR}` for credentials:

```yaml
sinks:
  - type: http-api
    config:
      url: "https://${API_HOST}/events"
      headers:
        Authorization: "Bearer ${API_TOKEN}"
```

**Resolution rules:**
- `${VAR}` — required. Fails with error if not set.
- `${VAR:-default}` — optional with default value.
- Variables resolved at submission time by the Coordinator.
- Resolved values stored only in memory during job execution.
- Resolved values **never** written to job metadata, logs, or Raft log.
- On job detail API response, credential fields are redacted.

**Kubernetes integration:**
```yaml
# K8s Pod spec
env:
  - name: DB_PASSWORD
    valueFrom:
      secretKeyRef:
        name: wire-secrets
        key: db-password
```

---

## 4. Data Model & Storage

### 4.1 Auth File Schema

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "users": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["username", "role"],
        "properties": {
          "username": { "type": "string" },
          "password_hash": { "type": "string", "description": "bcrypt hash" },
          "api_key": { "type": "string", "pattern": "^wk_live_" },
          "role": { "type": "string", "enum": ["admin", "operator", "viewer"] }
        },
        "oneOf": [
          { "required": ["password_hash"] },
          { "required": ["api_key"] }
        ]
      }
    }
  }
}
```

### 4.2 Storage Considerations

* **Auth file:** Read-only at startup. Changes require restart (no hot-reload in v1).
* **Certificates:** Read at startup. Certificate rotation requires restart.
* **Credentials in pipeline config:** Never persisted to disk in resolved form.

---

## 5. Design Decisions & Trade-offs

### Decision 1: File-based auth (not database-backed)

|  |  |
| -- | -- |
| **Context** | Wire targets "zero external dependencies." |
| **Options Considered** | (A) JSON auth file, (B) SQLite-backed user table, (C) External identity provider (OIDC) |
| **Decision** | Option A: JSON file |
| **Rationale** | Simplest. No database dependency. Aligns with Wire's "single binary" philosophy. Can be managed by configuration management tools (Ansible, K8s ConfigMaps). |
| **Trade-offs Accepted** | No dynamic user management (requires restart). No audit trail. Limited to ~100 users. |
| **Revisit Trigger** | If users need > 100 users, dynamic user management, or OIDC integration. |

### Decision 2: Environment variables for secrets (not Vault integration)

|  |  |
| -- | -- |
| **Context** | Connector credentials need secure storage. |
| **Options Considered** | (A) `${ENV_VAR}` substitution, (B) HashiCorp Vault integration, (C) Encrypted config files |
| **Decision** | Option A (Vault integration deferred to future TRD) |
| **Rationale** | Environment variables work with every deployment model (bare metal, Docker, K8s). K8s Secrets and Docker secrets both inject as env vars. Vault can inject via sidecar. |
| **Trade-offs Accepted** | Env vars visible in /proc/PID/environ on Linux. Less secure than Vault. |
| **Revisit Trigger** | If users in regulated industries require Vault or HSM integration. |

### Decision 3: Separate HTTP and Node certificates

|  |  |
| -- | -- |
| **Context** | HTTP (external API) and Node (internal cluster) have different trust domains. |
| **Options Considered** | (A) Shared certificate for both, (B) Separate certificates with potentially different CAs |
| **Decision** | Option B (already implemented in code) |
| **Rationale** | External API may use a public CA (Let's Encrypt). Internal cluster should use a private CA. Different rotation schedules. Different SAN requirements. |
| **Trade-offs Accepted** | More certificates to manage. |
| **Revisit Trigger** | If users consistently want a single cert for simplicity. |

---

## 6. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | Auth file missing or malformed | Startup fails with descriptive error | Cluster won't start | Low |
| 2 | Certificate expired | TLS handshake fails. Connection refused. | Node cannot join cluster / API inaccessible | High |
| 3 | Brute-force password attack | bcrypt is intentionally slow (~100ms per attempt). Rate limiting on HTTP server (429 response). | Attacker slowed | Medium |
| 4 | API key leaked | Revoke by removing from auth file and restarting. No dynamic revocation in v1. | Unauthorized access until restart | High |
| 5 | `${ENV_VAR}` not set for required credential | Job submission fails with error: "environment variable X not set" | Job not started | Low |
| 6 | mTLS misconfigured (wrong CA) | Node-to-node connection fails. Worker cannot join cluster. | Node isolated | Medium |
| 7 | Client sends HTTP to HTTPS port | Go TLS server returns connection error. Not a security risk. | Client sees error | Low |

---

## 7. Security & Compliance

### 7.1 TLS Configuration

* **Minimum TLS version:** TLS 1.2 (Go default). TLS 1.3 preferred.
* **Cipher suites:** Go's default secure cipher suite selection.
* **Certificate key size:** Minimum 2048-bit RSA or 256-bit ECDSA.

### 7.2 Password Security

* **Hashing:** bcrypt with cost factor >= 10.
* **Storage:** Hashed passwords only — never plaintext.
* **Comparison:** Constant-time comparison to prevent timing attacks.

### 7.3 Logging

* Credentials and API keys are **never** logged, even at DEBUG level.
* Failed authentication attempts are logged with source IP and username (not password).
* Successful authentication logged at INFO level with username.

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | Auth file parsing, RBAC enforcement, password verification | Go `testing` | 100% of auth logic |
| Integration Tests | HTTPS endpoints, mTLS connection, auth enforcement | Go `net/http` + test certs | All endpoints + roles |
| Security Tests | TLS configuration, cipher suites, certificate validation | `openssl s_client`, `testssl.sh` | No weak ciphers |
| Negative Tests | Invalid certs, wrong passwords, unauthorized roles | Go `testing` | All rejection paths |

### 8.1 Key Test Scenarios

1. HTTPS: Connect with valid client cert → 200. Connect without → 401.
2. mTLS: Worker connects with valid node cert → Yamux session established. Invalid cert → connection refused.
3. Basic Auth: Valid credentials → 200. Invalid → 401. Wrong role → 403.
4. API Key: Valid key → 200. Revoked key (removed from file) → 401 after restart.
5. Secret substitution: `${SET_VAR}` resolves correctly. `${UNSET_VAR}` fails with descriptive error.
6. Admin role can DELETE /cluster/nodes/. Operator role gets 403. Viewer role gets 403.

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should auth file support hot-reload (file watcher) without restart? | Tarun | Open |
| 2 | Should we support JWT tokens for API authentication (in addition to Basic Auth / API key)? | Tarun | Open |
| 3 | Should metrics endpoint require authentication? (Current: public) | Tarun | Open |
| 4 | Risk: No dynamic API key revocation — leaked keys active until restart | — | Acknowledged |
| 5 | Should we add rate limiting as a built-in feature or defer to reverse proxy (nginx, envoy)? | Tarun | Open |
| 6 | Should inter-node traffic encrypt the Raft log entries specifically, or is TLS transport sufficient? | Tarun | Open |
