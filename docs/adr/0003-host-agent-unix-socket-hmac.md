# 0003 — Privileged host agent over a Unix socket with HMAC-signed, allow-listed operations

- Status: Accepted
- Date: 2026-11-02

## Context and problem statement

The platform must create storage volumes, manage Docker containers, open PTYs, read and
write user files, query GPUs and create Linux accounts — all privileged. The spec (§4.3, §8,
§18) forbids exposing the Docker socket or arbitrary command execution, requires a separate
agent with minimal privilege, structured allow-listed operations, path validation, mutual
authentication and a versioned protocol. How do the API/worker and the privileged component
communicate?

## Decision drivers

- The internet-facing API must not run as root nor talk to Docker.
- A compromised API process must only be able to perform the same allow-listed operations it
  could perform anyway — never a shell.
- No network exposure of the privileged component.
- Streaming for uploads, downloads and terminals.

## Considered options

1. API process with Docker socket access and sudo rules.
2. Agent over TCP with mTLS.
3. Agent over a Unix domain socket with filesystem permissions plus HMAC request signatures,
   an operation allowlist and strict schemas.
4. gRPC over a Unix socket.

## Decision outcome

Chosen option 3 (`apps/agent`, `packages/agentproto`). HTTP/1.1 over
`/run/itk-cloud/agent.sock` (0660, group `itk-cloud`); every request is signed with
HMAC-SHA256 over method, path, timestamp, nonce and a digest of the body (or of the
`X-ITK-Agent-Op` header for streams), with a ±60 s window and a nonce replay cache. The agent
accepts only the 30 operations in `agentproto.Operations`, decodes envelopes and payloads with
`DisallowUnknownFields`, validates every payload, confines file access with `os.Root`,
verifies ITK labels before touching a container, and logs every sensitive operation with
actor, request ID and reason. `ProtocolVersion` is 1.

### Consequences

- Good: the attack surface of the root process is small and fully enumerated; no TCP port.
- Good: plain HTTP keeps the client and tests simple (WebSocket for terminals via
  `coder/websocket`); payload schemas are shared Go types.
- Bad: the agent still runs as root (loop mounts, chown, Docker, useradd) — a bug in it is a
  host compromise (documented residual risk).
- Bad: strict decoding makes every payload change a coordinated release of agent and API.
- Bad: the shared secret is readable by root and the `itk-cloud` service account (both
  trusted).

## More information

[agent-protocol.md](../agent-protocol.md), [security.md §4.9](../security.md#49-host-agent),
`packages/agentproto/protocol_test.go`, `apps/agent/internal/server/server_test.go`.
