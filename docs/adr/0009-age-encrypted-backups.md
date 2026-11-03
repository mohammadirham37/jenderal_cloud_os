# 0009 — age-encrypted backups with an offline private key

- Status: Accepted
- Date: 2026-11-02

## Context and problem statement

Backups contain the platform database (password hashes, encrypted secrets, audit log), the
configuration file with all platform secrets, user files and project databases. Spec §18 and
§20 require encrypted backups stored off the server, with authorised restores. A server
compromise must not also expose readable backups.

## Decision drivers

- Backups must be encrypted before touching disk.
- The server should be able to create backups but not read them.
- Simple, audited, well-maintained cryptography; easy offline recovery.

## Considered options

1. Symmetric encryption with a key stored on the server (e.g. derived from `ENCRYPTION_KEY`).
2. GPG public-key encryption.
3. `age` (X25519) public-key encryption with the private key kept offline.

## Decision outcome

Chosen option 3, using `filippo.io/age` v1.3.2. The server holds only the recipient
(`BACKUP_AGE_RECIPIENT`, required in production); `itkctl backup keygen` creates the pair and
the private key is stored offline. The worker streams `pg_dump`, tar/gzip and JSON exports
directly into age writers; the agent does the same for workspace files. Unencrypted backups
are refused. Restores need the private key: web restores receive it per request (kept only
encrypted inside the single-attempt restore job and scrubbed afterwards); offline restores use
`itkctl backup decrypt` / `itkctl restore platform-db`.

### Consequences

- Good: a compromised server cannot read existing backups; backup files can be copied to
  untrusted storage (NAS, rsync remote).
- Good: small, modern tool; recovery possible with the `age` CLI as well.
- Bad: losing the private key loses all backups — key custody and periodic decrypt tests are
  mandatory operational procedures.
- Bad: web restores require pasting the private key into the browser of an administrator.

## More information

[backup-restore.md](../backup-restore.md), `TestPlatformBackupEncryptedAndVerifiable`,
`TestProjectDatabaseLifecycle`.
