# 0006 — Project databases as separate database + role on a shared PostgreSQL

- Status: Accepted
- Date: 2026-11-02

## Context and problem statement

Projects can request PostgreSQL databases (spec §15). Credentials must be random and stored
securely, shown only at creation/reset, access limited per project, no superuser for users,
and backups separate from the platform database. The spec asks to document the trade-off and
not to claim that schema separation equals instance isolation.

## Decision drivers

- Data and privilege isolation between projects.
- Low operational cost on one server.
- Reachability from workspace containers.

## Considered options

1. Schema per project in one database.
2. Database + login role per project database on the platform's PostgreSQL server.
3. A PostgreSQL container/instance per project.

## Decision outcome

Chosen option 2 (`internal/modules/databases`). For each project database the worker (via
`PROJECT_DB_ADMIN_URL`, a role with `CREATEDB`/`CREATEROLE`, not superuser) creates a role
`itk_…` with `LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS` and a
connection limit, a database of the same name owned by it, revokes `ALL` on the database from
`PUBLIC` and grants `CONNECT, TEMPORARY` to the role. Passwords are 24 random characters,
encrypted at rest with AES-256-GCM, returned only on creation/reset. Workspaces reach the
server through `host.docker.internal`, restricted by `pg_hba.conf` (`/^itk_` roles from the
Docker pool `10.213.0.0/16`, scram-sha-256) and the firewall. Backups use separate
`project_databases` archives.

### Consequences

- Good: real database-level separation of data and privileges; per-role connection limits;
  standard tooling (`pg_dump`, `pg_restore`).
- Good: no extra processes; the platform's PostgreSQL is reused.
- Bad: **no resource isolation** — CPU, I/O, memory and disk are shared with the platform
  database; a heavy project query can slow the platform.
- Bad: catalog information (database and role names) is visible to all roles.
- Bad: PostgreSQL must listen beyond localhost when the feature is enabled (protected by
  `pg_hba.conf` and UFW).
- Instance-per-project (option 3) remains the path for projects needing stronger isolation.

## More information

[security.md §4.12](../security.md#412-project-database-isolation), `TestProjectDatabaseLifecycle`.
