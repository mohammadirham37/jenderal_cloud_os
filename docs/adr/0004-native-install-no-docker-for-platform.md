# 0004 — Native installation; Docker only for workspace containers

- Status: Accepted
- Date: 2026-11-02

## Context and problem statement

ITK Cloud targets one Ubuntu Server 24.04 machine managed with systemd (spec §4.1, §21). The
platform could be shipped as containers (Docker Compose) or installed natively. The lab
decided (user decision) that **ITK Cloud itself is installed natively** and that **Docker is
used only as the runtime for user container workspaces**.

## Decision drivers

- The host agent needs root, loop devices, `useradd`, PTYs and the Docker socket — running it
  in a container would require privileged mode and host mounts, negating isolation.
- Keep the Docker daemon dedicated to untrusted user workloads; avoid mixing platform and
  user containers on the same daemon and networks.
- Operators know systemd, apt and journald; upgrades and rollbacks should be transparent.
- systemd sandboxing (`ProtectSystem`, `NoNewPrivileges`, empty capability sets, syscall
  filters) is available for the unprivileged services.

## Considered options

1. Docker Compose for all platform services.
2. Native systemd services for the platform; Docker only for workspaces.
3. Kubernetes (single node).

## Decision outcome

Chosen option 2. `installer/install.sh` installs PostgreSQL, Redis and Caddy from apt, Docker
Engine (for workspaces only), a pinned Node.js runtime for the web console, the Go binaries in
`/opt/itk-cloud/releases/<version>` with a `current` symlink, and the units
`itk-cloud-{agent,api,worker,web}.service`. API, worker and web run as the unprivileged user
`itk-cloud` with hardened units; the agent runs as root with a restricted capability bounding
set. Upgrades switch the symlink and can roll back binaries.

### Consequences

- Good: no privileged platform containers; Docker failures do not take down the console
  (the console reports "container runtime unavailable" honestly).
- Good: standard Ubuntu tooling for logs, updates, and backups of PostgreSQL.
- Bad: the installer must handle OS packages and versions (e.g. PostgreSQL from Ubuntu's
  archive, Docker from docker.com); only Ubuntu 24.04 is supported.
- Bad: development on macOS runs the processes directly and uses the `directory` storage
  driver (no loop mounts).

## More information

[installation.md](../installation.md), `deployments/systemd/`, `installer/`.
