# API ↔ host agent protocol (agentproto v1)

`itk-cloud-agent` is the only privileged component of ITK Cloud. The API and the worker talk
to it through the versioned protocol defined in [`packages/agentproto`](../packages/agentproto)
(shared by both sides). This document specifies that protocol. Rationale:
[ADR 0003](adr/0003-host-agent-unix-socket-hmac.md). Security context: [security.md §4.9](security.md#49-host-agent).

## 1. Transport

| Aspect | Value |
|---|---|
| Transport | HTTP/1.1 over a Unix domain socket, `AGENT_SOCKET_PATH` (default `/run/itk-cloud/agent.sock`). No TCP listener. |
| Socket permissions | `0660`, group `AGENT_SOCKET_GROUP` (default `itk-cloud`); the parent directory is chgrp'd to the same group (`RuntimeDirectoryMode=0750` in the unit). |
| Host in URLs | `http://agent` / `ws://agent` (ignored; the dialer always connects to the socket) |
| Endpoints | `POST /v1/ops` (JSON operations), `GET /v1/files/download` (stream), `POST /v1/files/upload` (stream), `GET /v1/terminal` (WebSocket) |
| Server limits | header read timeout 10 s; max header size 128 KiB; `/v1/ops` body ≤ 1 MiB; op header ≤ 64 KiB decoded; at most `AGENT_MAX_CONCURRENT_OPS` (32) concurrent `/v1/ops` operations — a request waiting more than 30 s for a slot fails with `unavailable` ("agent is busy") |
| Client timeouts | `/v1/ops`: 10 min overall and 10 min response-header timeout (callers pass shorter contexts, e.g. 10 s for heartbeats, 20 min for image pulls); streams: bounded by the caller's context |
| Connection failure | the client returns `agentproto.ErrUnavailable`; the API maps it to `503 unavailable` |

## 2. Authentication

Every request carries three headers:

| Header | Content |
|---|---|
| `X-ITK-Agent-Timestamp` | Unix time in seconds (decimal) |
| `X-ITK-Agent-Nonce` | Random string, 16–64 characters (the client sends 16 random bytes as 32 hex chars) |
| `X-ITK-Agent-Signature` | Lowercase hex HMAC-SHA256 |

Canonical string and signature (`packages/agentproto/sign.go`):

```text
signature = hex( HMAC-SHA256( key = AGENT_SHARED_SECRET (raw bytes),
                              message = METHOD + "\n" + PATH + "\n" + TIMESTAMP + "\n" + NONCE + "\n" + DIGEST ) )
DIGEST    = hex( SHA-256( request body ) )                 for POST /v1/ops
          = hex( SHA-256( value of X-ITK-Agent-Op header ) ) for the streaming/WebSocket endpoints
```

`METHOD` is the HTTP method (`POST`/`GET`) and `PATH` the URL path without query string
(`/v1/ops`, `/v1/files/download`, `/v1/files/upload`, `/v1/terminal`).

Verification (`Verifier.Verify`), performed **before** any decoding:

1. The timestamp must be within **±60 s** (`MaxClockSkew`) of the agent's clock.
2. The nonce length must be 16–64.
3. The signature is recomputed and compared with `hmac.Equal` (constant time).
4. The nonce must not be in the **replay cache**; it is then stored for 2 × 60 s = 120 s
   (expired entries are purged on each verification).

Failures return HTTP 403 with `{"ok":false,"error":{"code":"forbidden","message":"authentication failed"}}`
and a warning in the agent log. `AGENT_SHARED_SECRET` must be at least 32 characters (startup
check on both sides); it can also be read from `AGENT_SHARED_SECRET_FILE`.

Notes: the nonce cache is in memory, so it is empty after an agent restart (a captured
request could be replayed within its 60 s window right after a restart — capturing requires
access to the socket). For uploads only the operation header is signed; the streamed file
body is protected by the socket's file permissions.

## 3. Envelope

### Request (`agentproto.Request`)

```json
{
  "protocol_version": 1,
  "operation": "workspace.provision",
  "request_id": "9f2c1d0e8b7a4c3d2e1f0a9b8c7d6e5f",
  "actor_id": "6c1f0c8e-6d0a-4f0e-9a51-0e3b9a7f4d21",
  "resource_id": "3b1d2c4e-0f6a-4b7c-8d9e-1a2b3c4d5e6f",
  "reason": "provision workspace storage",
  "payload": { "workspace_id": "3b1d2c4e-0f6a-4b7c-8d9e-1a2b3c4d5e6f", "storage_bytes": 5368709120, "owner_uid": 1000, "native": false }
}
```

| Field | Rule (`Request.Validate`) |
|---|---|
| `protocol_version` | must equal the agent's `ProtocolVersion` (1) |
| `operation` | must be in the allowlist (§5) |
| `request_id` | required, ≤ 64 chars — the API request ID, or `job-<job id>` for worker jobs |
| `actor_id` | required, ≤ 64 chars — user UUID or `system` |
| `resource_id` | ≤ 128 chars — the affected resource |
| `reason` | required (non-blank), ≤ 500 chars — why the operation is performed |
| `payload` | operation-specific object (`{}` when empty) |

The envelope and the payload are decoded with `DisallowUnknownFields`; unknown fields,
trailing data and type mismatches are rejected (`invalid_request`). The payload is then
validated by its `Validate()` method, which also normalises paths.

The API fills `request_id`, `actor_id` and `reason` automatically for every call
(`internal/modules/agent`), so agent logs can be correlated with API logs and audit entries.

### Response (`agentproto.Response`)

```json
{ "ok": true,  "data": { ... } }
{ "ok": false, "error": { "code": "not_found", "message": "workspace storage is not available" } }
```

## 4. Versioning

- `ProtocolVersion = 1`. The agent rejects requests with any other version.
- The worker's heartbeat (`agent.info` every 30 s) records the agent's protocol version; a
  mismatch marks the node `degraded` with "protocol version mismatch", visible in
  `GET /api/v1/admin/agent/status` (`expected_protocol_version`).
- Because decoding is strict, adding a field to an existing payload is a **breaking change**
  for older agents. API, worker and agent are therefore always released and upgraded
  together (one release bundle; `installer/upgrade.sh` restarts all services). Increment
  `ProtocolVersion` for incompatible changes to semantics or envelopes.

## 5. Operations (allowlist)

All operations below are the complete allowlist (`agentproto.Operations`). There is no
generic command execution. Common validation formats: UUIDs are lowercase canonical; runtime
IDs `^[0-9a-f]{12,64}$`; image references `^[a-z0-9][a-z0-9._/:@-]{1,250}$`; container names
`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`; native Linux users `^itk-[a-z0-9][a-z0-9-]{1,26}$`;
GPU UUIDs `^(GPU|MIG)-[0-9a-fA-F-]{8,64}$`; env keys `^[A-Z_][A-Z0-9_]{0,63}$`; backup file names
`^[a-zA-Z0-9._-]{1,200}$` without `..`; workspace paths are cleaned by `CleanRelPath`
(relative, no `..`, no NUL, ≤ 4096 bytes, segments ≤ 255).

### 5.1 Host

| Operation | Payload | Result | Notes |
|---|---|---|---|
| `agent.info` | `{}` | `AgentInfo`: `agent_version, protocol_version, hostname, os, platform, kernel, arch, cpu_model, cpu_cores, cpu_threads, memory_total, boot_time, capabilities{docker, docker_version, docker_error, nvidia, nvidia_runtime, native_workspaces, storage_driver, storage_enforced, apparmor, seccomp}` | Docker checked with a 5 s timeout; `nvidia` = `nvidia-smi` found; `nvidia_runtime` = Docker has an `nvidia` runtime or `nvidia-container-cli` exists. |
| `host.metrics` | `{}` | `HostMetrics`: CPU (overall, per core), load 1/5/15, memory/swap, disks (physical filesystems; workspace mounts, `/snap`, `/var/lib/docker` excluded), net rx/tx bytes/s, uptime, `docker{available, version, error, containers_running/stopped/paused, images}` | |
| `gpu.inventory` | `{}` | `GPUInventory{available, error, gpus[]}` | Never fails; without `nvidia-smi` → `available:false` with an error message ([gpu.md](gpu.md)). |
| `emergency.stop_all` | `{}` | `{"stopped": n, "failed": n}` | Stops every running container labelled `itk.managed=true` (10 s grace). |

### 5.2 Workspace storage and native users

| Operation | Payload (validation) | Result | Notes |
|---|---|---|---|
| `workspace.provision` | `workspace_id` UUID; `storage_bytes` 64 MiB–4 TiB; `owner_uid` 1000–2³¹−2; `native` bool; `linux_user` (required when native) | `WorkspaceUsage{used_bytes, limit_bytes, enforced}` | Idempotent. Loopback: creates `disk.img` (sparse, `mkfs.ext4 -m 0 -E root_owner=uid:uid`), mounts it `loop,nosuid,nodev`; chowns `data/` when root. Native requests are `forbidden` when native workspaces are disabled. |
| `workspace.usage` | `WorkspaceRef{workspace_id, native?, linux_user?}` | `WorkspaceUsage` | Loopback: `statfs` (enforced). Directory: tree walk (cached 1 min). |
| `workspace.destroy` | `WorkspaceRef` | `null` | Idempotent. Force-removes managed containers labelled with the workspace, removes network `itk-ws-<id>`, unmounts (lazy fallback) and deletes the workspace directory, deletes the native Linux user. |
| `native.user_ensure` | `workspace_id` UUID, `linux_user` | `NativeUser{uid, gid}` | `useradd --no-create-home --home-dir <data dir> --shell /bin/bash --gid <AGENT_NATIVE_GROUP>`; refuses uids < 1000; `forbidden` when disabled. |

### 5.3 Images and containers

| Operation | Payload (validation) | Result | Notes |
|---|---|---|---|
| `image.ensure` | `reference` (image regex); `pull` bool | `ImageInfo{present, id, size_bytes}` | Inspects; pulls when missing and `pull` is true. A failed pull returns `not_found` ("image is not available locally and could not be pulled"). |
| `container.create` | `ContainerCreate`: `workspace_id`, `container_id`, `owner_id` UUIDs; `name`; `image`; `cpu_millis` 100–256000; `memory_bytes` 64 MiB–2 TiB; `pids_limit` 32–32768; `run_as_uid/gid` ≥ 1000; `env` ≤ 64 (key regex, value ≤ 4096, no NUL); `command` ≤ 64 args (≤ 4096, no NUL); `ports` ≤ 16, each 1024–65535; `gpu_devices` ≤ 16 GPU UUIDs; `read_only_root` | `ContainerState` | Idempotent by label `itk.container_id`. Requires provisioned storage and a local image. Creates the workspace network and a hardened container `itk-<container id>` ([security.md §4.10](security.md#410-container-hardening)); GPU devices via `DeviceRequests` (driver `nvidia`, capabilities `gpu, compute, utility`). |
| `container.start` | `ContainerRef{container_id UUID, runtime_id?, timeout_sec 0–300}` | `ContainerState` incl. `host_ports` (container port → loopback port) | Ensures the workspace volume is mounted first. |
| `container.stop` / `container.restart` | `ContainerRef` | `ContainerState` | Grace period `timeout_sec` (default 15 s). |
| `container.remove` | `ContainerRef` | `ContainerState{status: removed}` (or `missing`) | Force remove incl. anonymous volumes; missing containers are not an error. |
| `container.inspect` | `ContainerRef` | `ContainerState` (or `{status: missing}`) | |
| `container.logs` | `ContainerRef` + `tail` 1–5000, `timestamps` | `LogsResult{lines, truncated}` | stdout+stderr demultiplexed, capped at 4 MiB, invalid UTF-8 replaced. |
| `container.stats` | `runtime_ids` (≤ 500) | `[]ContainerStats{runtime_id, cpu_percent, memory_bytes, memory_limit, pids, net_rx_bytes, net_tx_bytes}` | Only managed, running containers; others silently skipped. |
| `container.list` | `{}` | `[]ManagedContainer{runtime_id, container_id, workspace_id, state}` | All containers labelled `itk.managed=true`. |

Every operation that takes a `ContainerRef` resolves the Docker container and verifies the
labels `itk.managed=true` and `itk.container_id=<container_id>`; otherwise it fails with
`forbidden` ("container is not managed by ITK Cloud").

### 5.4 Files (confined with `os.Root` to `WORKSPACE_ROOT/<id>/data`)

| Operation | Payload | Result | Errors |
|---|---|---|---|
| `files.list` | `FilePath{workspace_id, path}` | `{entries: FileEntry[], truncated}` (≤ 5000, directories first) | `not_found`, `invalid_request` (not a directory) |
| `files.stat` | `FilePath` | `FileEntry{name, path, type: file\|dir\|symlink\|other, size, mode, mod_time}` (lstat) | `not_found` |
| `files.mkdir` | `FilePath` (non-root) | `FileEntry` | `conflict` (exists), `not_found` (parent) |
| `files.rename` | `FileRename{workspace_id, from, to}` (both non-root) | `FileEntry` at `to` | `conflict` (destination exists) |
| `files.delete` | `FileDelete{workspace_id, path (non-root), recursive}` | `null` | `conflict` (directory not empty) |
| `files.read` | `FileRead{workspace_id, path, max_bytes 1–16 MiB}` | `FileContent{content, size, mod_time, binary}` | `too_large`; binary content → `binary: true`, empty content |
| `files.write` | `FileWrite{workspace_id, path, content ≤ 16 MiB, expected_mod_time?, create}` | `FileEntry` | `conflict` (modified since `expected_mod_time`), `not_found` (missing and `create` false), `quota_exceeded` |
| `files.download` | stream only (§6) | file bytes | `not_found`, `invalid_request` (not a regular file) |
| `files.upload` | stream only (§6) | `FileEntry` | `conflict` (exists and not `overwrite`), `too_large`, `quota_exceeded` |

All storage-backed operations first ensure the loopback volume is mounted (after a reboot).
Files are opened with `O_NONBLOCK` and must be regular files; writes go to a temporary file
`.itk-tmp-<random>` and are renamed into place; new files and directories are chowned to the
owner of the data directory.

### 5.5 Terminal

`terminal.attach` — WebSocket only (§6). Payload `TerminalAttach`:

| Field | Rule |
|---|---|
| `workspace_id` | UUID |
| `runtime_id` + `container_id` **or** `linux_user` | exactly one target |
| `run_as_uid` | ≥ 1000 (terminals never run as root) |
| `cols` / `rows` | 10–1000 / 5–500 |
| `cpu_millis`, `memory_bytes`, `pids_limit` | native only: limits of the transient systemd scope |
| `idle_timeout_sec`, `max_duration_sec` | both ≥ 60 |

Container: `docker exec` with a TTY as `uid:uid`, working directory `/home/itk`, command
`/bin/sh -c 'if [ -x /bin/bash ]; then exec /bin/bash -l; else exec /bin/sh -l; fi'`.
Native: `systemd-run --scope --quiet --collect --uid --gid --slice=itk-native.slice -p TasksMax=max(pids,64) -p MemoryMax -p MemorySwapMax=0 -p CPUQuota=<cpu_millis/10>% -- /bin/bash -l`
in a PTY with a minimal environment.

Frames: binary = terminal bytes (both directions); text from the client = JSON
`{"type":"resize","cols":N,"rows":N}` (applied when within limits) or `{"type":"ping"}`.
The agent closes the session with reason `shell_exited`, `client_disconnected`,
`idle_timeout`, `max_duration` or `cancelled`.

### 5.6 Backups

| Operation | Payload | Result | Notes |
|---|---|---|---|
| `backup.workspace_files` | `workspace_ids` (1–10000 UUIDs), `file_name`, `age_recipient` (`age1…`, ≤ 200) | `BackupResult{file_name, size, sha256}` | tar+gzip stream of each workspace's data dir (prefix `<workspace id>/`, `lost+found`, sockets, FIFOs and devices skipped) encrypted with age directly into `BACKUP_ROOT/<file>.partial` (0600), fsynced and renamed. Plaintext never touches disk. |
| `restore.workspace_files` | `file_name`, `age_identity` (`AGE-SECRET-KEY-…`), `workspace_id?` | `{"files_restored": n}` | Decrypts and extracts through `os.Root` into existing data dirs (workspaces must be provisioned); entries must start with a UUID and pass `CleanRelPath`; existing files are overwritten, others kept; ownership set to the data dir owner. The identity is never written to disk. |

## 6. Streaming endpoints

For `files.download`, `files.upload` and `terminal.attach`, the request envelope (§3) is
JSON-encoded, base64url-encoded without padding and sent in the header
**`X-ITK-Agent-Op`**; the signature digest is the SHA-256 of that header value. The agent
checks that `operation` matches the endpoint; these operations are refused on `/v1/ops`
("operation must use its streaming endpoint").

| Endpoint | Method | Body | Success |
|---|---|---|---|
| `/v1/files/download` | GET | — | `200`, `Content-Type: application/octet-stream`, `Content-Length`, `X-ITK-File-Name`; body = file bytes |
| `/v1/files/upload` | POST | raw file bytes (`application/octet-stream`); at most `max_bytes` | `200` JSON envelope with `FileEntry` |
| `/v1/terminal` | GET (WebSocket upgrade) | — | `101`, then frames (§5.5) |

On failure before streaming starts, the endpoints answer with the JSON error envelope and
the mapped HTTP status.

## 7. Error codes

| Agent code | HTTP (agent) | API mapping (`agent.MapError`) |
|---|---|---|
| `invalid_request` | 400 | `400 bad_request` (message passed through) |
| `forbidden` | 403 | `403 forbidden` ("The host agent refused the operation.") |
| `not_found` | 404 | `404 not_found` |
| `conflict` | 409 | `409 conflict` (message passed through) |
| `too_large` | 413 | `413 payload_too_large` |
| `quota_exceeded` | 507 | `507 quota_exceeded` (ENOSPC/EDQUOT) |
| `unavailable` | 503 | `503 unavailable` (Docker unreachable, agent busy) |
| `internal` | 500 | `500 internal_error` (message ≤ 300 chars, logged server-side only) |
| (socket unreachable) | — | `503 unavailable` ("The host agent is unavailable…") |

Docker API errors with status 409 map to `conflict`; other Docker errors to `internal`
("container runtime error: …"); an unreachable Docker daemon to `unavailable`.

## 8. Logging of sensitive operations

Each `/v1/ops` and stream operation is logged as JSON with `operation`, `request_id`,
`actor_id`, `resource_id`, `reason` and `duration_ms` — never payload contents (no file
contents, no environment values, no age identities). Failures are logged at `warn` with the
error message. These operations are always logged at `info`; the rest at `debug`:

`workspace.provision`, `workspace.destroy`, `container.create|start|stop|restart|remove`,
`image.ensure`, `files.delete|rename|upload|write|download`, `terminal.attach`,
`native.user_ensure`, `backup.workspace_files`, `restore.workspace_files`,
`emergency.stop_all`.

Terminal sessions additionally log their end with reason and duration. Rejected
(unauthenticated) requests are logged at `warn`. Logs go to stdout (journald:
`journalctl -u itk-cloud-agent`).

### Manual call (debugging, as root on the server)

```bash
SECRET=$(sed -n 's/^AGENT_SHARED_SECRET=//p' /etc/itk-cloud/agent.env)
BODY='{"protocol_version":1,"operation":"agent.info","request_id":"manual-check-1","actor_id":"operator","resource_id":"local","reason":"manual health check","payload":{}}'
TS=$(date +%s); NONCE=$(openssl rand -hex 16)
DIGEST=$(printf '%s' "$BODY" | sha256sum | cut -d' ' -f1)
SIG=$(printf 'POST\n/v1/ops\n%s\n%s\n%s' "$TS" "$NONCE" "$DIGEST" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $NF}')
curl -s --unix-socket /run/itk-cloud/agent.sock -H 'Content-Type: application/json' \
  -H "X-ITK-Agent-Timestamp: $TS" -H "X-ITK-Agent-Nonce: $NONCE" -H "X-ITK-Agent-Signature: $SIG" \
  --data "$BODY" http://agent/v1/ops | jq .
```

## 9. Adding a new operation safely

1. **Justify it.** Prefer extending the API/worker; add an agent operation only when host
   privileges are unavoidable. Never add generic command execution, arbitrary paths, or
   arbitrary Docker options.
2. **Define it in `packages/agentproto/protocol.go`**: an `Op…` constant, an entry in
   `Operations` with a payload constructor, a payload struct with `json` tags and a strict
   `Validate()` (UUIDs, regexes, ranges, `CleanRelPath` for paths, no raw host paths, no
   free-form command strings). Add result types.
3. **Implement it in the agent** (`apps/agent/internal/server/ops.go` `dispatch`, or a
   dedicated endpoint for streams registered in `streamOps`). Derive host paths only from
   validated IDs; use `os.Root` for workspace files; run helper binaries only with fixed
   arguments via the existing `run` helpers; verify container labels; map errors to agent
   codes. Add it to `sensitive` if it changes state or touches user data.
4. **Call it from the API/worker** through `internal/modules/agent` (`Agent.Do`) with a
   meaningful `reason`, after the API has performed authorization and auditing.
5. **Tests**: payload validation (accept/reject) in `packages/agentproto/protocol_test.go`,
   agent behaviour in `apps/agent/internal/server/server_test.go`, and an integration test
   for the user-facing flow.
6. **Compatibility**: new operations and new payload fields require the agent and API to be
   upgraded together; bump `ProtocolVersion` for incompatible changes.
7. **Document it** in this file (§5) and, if user-visible, in the API docs.
