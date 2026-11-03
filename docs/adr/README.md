# Architecture Decision Records

Significant architecture decisions of Jenderal Cloud OS, written in the
[MADR](https://adr.github.io/madr/) format. Each record states the context, the options that
were considered, the decision and its consequences. Records are immutable once accepted; a
changed decision gets a new record that supersedes the old one.

| # | Title | Status | Date |
|---|---|---|---|
| [0001](0001-modular-monolith-go.md) | Go modular monolith for API and worker | Accepted | 2026-11-02 |
| [0002](0002-postgres-job-queue.md) | PostgreSQL-backed job queue with `SKIP LOCKED` | Accepted | 2026-11-02 |
| [0003](0003-host-agent-unix-socket-hmac.md) | Privileged host agent over a Unix socket with HMAC-signed, allow-listed operations | Accepted | 2026-11-02 |
| [0004](0004-native-install-no-docker-for-platform.md) | Native installation; Docker only for workspace containers | Accepted | 2026-11-02 |
| [0005](0005-workspace-storage-loopback-volumes.md) | Per-workspace loopback ext4 volumes for storage quotas | Accepted | 2026-11-02 |
| [0006](0006-project-database-isolation.md) | Project databases as separate database + role on a shared PostgreSQL | Accepted | 2026-11-02 |
| [0007](0007-app-proxy-separate-origin.md) | Workspace app proxy on a separate origin | Accepted | 2026-11-02 |
| [0008](0008-sveltekit-spa-console.md) | Client-rendered SvelteKit console | Accepted | 2026-11-02 |
| [0009](0009-age-encrypted-backups.md) | age-encrypted backups with an offline private key | Accepted | 2026-11-02 |
| [0010](0010-minimal-docker-engine-client.md) | Minimal in-house Docker Engine API client | Accepted | 2026-11-02 |

## Template

```markdown
# NNNN — Title

- Status: Proposed | Accepted | Superseded by NNNN
- Date: YYYY-MM-DD

## Context and problem statement
## Decision drivers
## Considered options
## Decision outcome
### Consequences
## More information
```
