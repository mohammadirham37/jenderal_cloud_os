# 0005 — Per-workspace loopback ext4 volumes for storage quotas

- Status: Accepted
- Date: 2026-11-02

## Context and problem statement

Each workspace needs persistent storage with a quota that is actually enforced (spec §12:
"do not rely only on numbers in the database; enforce limits … using appropriate storage
mechanisms"), usable both by containers (bind mount) and by Linux-native workspaces (home
directory), without depending on a particular host filesystem.

## Decision drivers

- Hard enforcement independent of the host filesystem type (ext4 is Ubuntu's default).
- Works for containers and native accounts alike.
- Survives reboots; simple backup (plain files).
- No dependency on Docker storage plugins.

## Considered options

1. Plain directories with periodic measurement only.
2. Filesystem project quotas (XFS `pquota` or ext4 project quotas) on `WORKSPACE_ROOT`.
3. Docker named volumes with size options (driver-dependent).
4. One sparse image file per workspace, formatted ext4 and loop-mounted (`loopback`), with a
   `directory` fallback for development.

## Decision outcome

Chosen option 4 (`apps/agent/internal/storage`). The agent creates
`WORKSPACE_ROOT/<id>/disk.img` of `storage_bytes` (sparse), `mkfs.ext4 -m 0 -E
root_owner=<uid>:<uid>`, and mounts it `loop,nosuid,nodev` on `<id>/data`, which is bind-mounted
at `/home/jenderal` or used as the native home. Volumes are remounted at agent start and lazily
before access. The `directory` driver keeps the same layout without enforcement for
development; the agent reports `storage_enforced` so the console can show it.

### Consequences

- Good: the quota is a hard filesystem limit (ENOSPC → `507 quota_exceeded`), regardless of
  the host filesystem; per-workspace isolation of inode/space exhaustion.
- Good: deletion is `umount` + remove directory; backups read the mounted tree.
- Bad: requires root and loop devices; each running workspace uses a loop device.
- Bad: volumes cannot be resized in the current version; sparse images allow allocations to
  exceed physical disk (overcommit must be monitored).
- Bad: the container writable layer is outside the volume and is limited only with
  `AGENT_CONTAINER_ROOTFS_SIZE` on overlay2/XFS `pquota` (documented residual risk).

## More information

[architecture.md §10](../architecture.md#10-workspace-storage-drivers), [workspace.md §11](../workspace.md#11-storage).
