# 0010 — Minimal in-house Docker Engine API client

- Status: Accepted
- Date: 2026-11-02

## Context and problem statement

The agent needs a small set of Docker Engine operations: ping/version/info, image inspect and
pull, network create/remove, container create/start/stop/restart/remove/inspect/list, logs,
one-shot stats, and exec with a TTY for terminals. The official Go SDK pulls in a large
dependency tree that changes frequently, and the agent is the most privileged component.

## Decision drivers

- Minimal dependencies and attack surface in the root process.
- Explicit control over every request (no accidental use of dangerous options).
- Stable behaviour across Docker releases via API version negotiation.

## Considered options

1. Official Docker Go SDK.
2. Shelling out to the `docker` CLI.
3. A small client for the versioned REST API over the Unix socket.

## Decision outcome

Chosen option 3 (`apps/agent/internal/docker`, about 500 lines). The client pings
`/_ping`, pins the daemon's `API-Version` (minimum **1.44**, i.e. Docker Engine 25+), and
implements only the calls listed above, including the multiplexed log decoder and the hijacked
connection for `exec start` with a TTY. Errors map to `ErrNotFound`, `ErrUnavailable` and
`APIError`. Container configuration is assembled in one place (`containerCreate`), which makes
the hardening profile reviewable.

### Consequences

- Good: no SDK dependency; the full Docker surface used by Jenderal Cloud OS fits on a few pages.
- Good: version negotiation keeps it working with current Docker releases (tested with Docker
  Engine 29.9, API 1.56).
- Bad: new Docker features require code changes here; response structs only contain the
  fields Jenderal Cloud OS reads.
- Bad: the hijacked exec stream is implemented by hand and needs care when Docker changes
  upgrade semantics.

## More information

[security.md §4.10–4.11](../security.md#410-container-hardening), integration test
`TestContainerWorkspaceLifecycle` (inspects the created containers).
