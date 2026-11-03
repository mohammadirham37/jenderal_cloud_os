# Backup and restore

Jenderal Cloud OS backs up the platform database, configuration, workspace metadata, workspace files
and project databases. **Every backup is encrypted with [age](https://age-encryption.org)**
to a public key whose private half is kept **offline**; the platform refuses to create
unencrypted backups ([ADR 0009](adr/0009-age-encrypted-backups.md)). Implementation:
`internal/modules/backups`, agent `backup.workspace_files` / `restore.workspace_files`,
`itkctl backup|restore`, `installer/backup.sh`.

Related: [operations](operations.md) · [installation](installation.md) · [security](security.md#413-backups).

## 1. Backup kinds

| Kind | Produced by | File name | Content |
|---|---|---|---|
| `platform_db` | worker (`pg_dump --format=custom --no-owner --no-privileges --compress=6` of `DATABASE_URL`) | `itk-platform-db-<UTC timestamp>-<id8>.dump.age` | The entire platform database: users, roles, sessions, settings, workspaces, audit log, encrypted secrets, jobs. |
| `config` | worker | `itk-config-….tar.gz.age` | Readable regular files under `CONFIG_DIR` (`/etc/itk-cloud`), stored under `config/`: `itk-cloud.env` (all platform secrets), `web.env`. Files the worker cannot read (e.g. `agent.env`, 0600 root) are skipped. |
| `workspace_metadata` | worker | `itk-workspace-metadata-….json.age` | JSON export (with `exported_at`) of `resource_quotas`, `container_images`, `workspace_templates`, `projects`, `project_members`, `workspaces`, `workspace_members`, `containers`, `gpu_devices`, `gpu_allocations`, and `databases` (id, project, names, status, created_at — no passwords). |
| `workspace_files` | agent (as root) | `itk-workspace-files-….tar.gz.age` | tar+gzip of the data directory of every workspace in `ready`, `running`, `stopped` or `suspended`, entries prefixed with the workspace ID; `lost+found`, sockets, FIFOs and devices skipped. Streamed straight into age — no plaintext on disk. |
| `project_databases` | worker | `itk-project-databases-….tar.age` | tar of `<db_name>.dump` (`pg_dump` custom format) for every `ready` project database; updates each database's `last_backup_at/status`. Only when `PROJECT_DB_ADMIN_URL` is set. |
| `full` | request only | — | Expands to one job per kind above (project databases only when configured). Scheduled `full` backups omit `workspace_files` unless `backup.include_workspace_files` is true. |

Properties of every backup file: created as `<name>.partial` with mode 0600, fsynced, renamed
atomically; SHA-256 of the encrypted file and its size are recorded in `backup_jobs`; status
`queued → running → succeeded|failed`, later `expired`. Backup jobs run in the worker with up
to 3 attempts; a final failure notifies every holder of `backup.manage` and fires the
`backup_failed` alert (for scheduled backups).

Consistency: `pg_dump` produces a transactionally consistent snapshot. Workspace files are
copied while workspaces may be running — there is no filesystem snapshot, so files being
written during the backup can be inconsistent. Stop critical workspaces (or ask users to)
before a manual `workspace_files` backup when consistency matters.

## 2. Encryption keys

Generate the key pair once (the installer does this automatically when
`BACKUP_AGE_RECIPIENT` is empty):

```bash
itkctl backup keygen
# Store the PRIVATE key OFFLINE (password manager / sealed envelope). It is required to restore backups.
# private key: AGE-SECRET-KEY-1...
BACKUP_AGE_RECIPIENT=age1...
```

- Put `BACKUP_AGE_RECIPIENT=age1…` into `/etc/itk-cloud/itk-cloud.env` and restart
  `itk-cloud-api` and `itk-cloud-worker`. In production the services refuse to start without
  it.
- Keep the **private key** (`AGE-SECRET-KEY-1…`) **off the server**: in the lab's password
  manager **and** on paper in a sealed envelope in a safe, accessible to at least two
  administrators. The installer writes it to `/root/itk-cloud-backup-private-key.txt` (0400)
  only so it can be copied away — then `shred -u` that file.
- Losing the private key makes every backup unreadable. Test it (§8).
- To rotate: generate a new pair, change `BACKUP_AGE_RECIPIENT`, restart; keep the old
  private key until all backups encrypted to it have expired.
- The platform never stores the private key. For a web restore it is pasted into the form,
  encrypted with `ENCRYPTION_KEY` inside the single-attempt restore job and removed from the
  job payload when the job finishes.

## 3. Schedule and retention

| Setting / variable | Default | Effect |
|---|---|---|
| `backup.schedule_enabled` (setting) | `true` | Daily scheduled `full` backup. |
| `backup.schedule_hour_utc` (setting) | `18` | Hour (UTC) of the daily backup — 18:00 UTC is 01:00 WIB. The worker checks every 10 minutes and enqueues once per day (job key `backup.scheduled:<date>:<kind>`). |
| `backup.include_workspace_files` (setting) | `false` | Include `workspace_files` in the scheduled backup. **Off by default**: without it, user files are only backed up manually. |
| `BACKUP_RETENTION_DAYS` | `14` | Each successful backup expires `n` days after completion; the retention task (every 6 h) deletes expired files from `BACKUP_ROOT` **and** `BACKUP_COPY_DIR` and marks them `expired`. |
| `BACKUP_ROOT` | `/srv/itk-cloud/backups` | Primary location (0770 root:itk-cloud). |
| `BACKUP_COPY_DIR` | empty | Second location receiving a copy of each backup (see §4). |

Manual backups: *Administration → Backups* (`POST /api/v1/admin/backups {"kind": ...}`,
permission `backup.manage`), or on the server:

```bash
sudo installer/backup.sh                                   # platform_db, config, workspace_metadata now
sudo itkctl backup run --kind platform_db                      # synchronous; kinds: platform_db|config|workspace_metadata
```

`installer/upgrade.sh` runs `itkctl backup run --kind platform_db --trigger pre_upgrade`
(and `config`) before every upgrade and aborts if the database backup fails.

Monitoring: alerts `backup_failed` (last scheduled backup failed) and `backup_missing` (no
successful backup in 26 hours), the *Last backup* card in *Monitoring → Services*, and
notifications to `backup.manage` holders.

## 4. Off-server copies

Spec §20 requires that the only copy of a backup must not live on the server's disk.

1. **`BACKUP_COPY_DIR`** — a directory on another disk or a network mount (NFS/SMB from the
   NAS). After each successful backup the worker copies the file there (atomically). Workspace
   file archives are **not** copied (they are written by the agent; copy them with §4.2).
   Requirements: writable by the `itk-cloud` user, and allowed in the worker's systemd
   sandbox:
   ```bash
   sudo mkdir -p /mnt/backup-nas/itk-cloud && sudo chown itk-cloud:itk-cloud /mnt/backup-nas/itk-cloud && sudo chmod 0700 /mnt/backup-nas/itk-cloud
   sudo systemctl edit itk-cloud-worker      # add:  [Service]  ReadWritePaths=/mnt/backup-nas/itk-cloud
   echo 'BACKUP_COPY_DIR=/mnt/backup-nas/itk-cloud' | sudo tee -a /etc/itk-cloud/itk-cloud.env
   sudo systemctl restart itk-cloud-worker itk-cloud-api
   ```
   Copy failures are recorded in the backup's `error` field while the backup itself stays
   `succeeded`. Retention also deletes expired files in `BACKUP_COPY_DIR`.
2. **rsync to another server/NAS** (recommended in addition, keeps history beyond retention):
   ```bash
   sudo ITK_BACKUP_REMOTE=backup@nas.lab:/srv/itk-cloud ./installer/backup.sh --sync-only
   # e.g. daily from root's crontab after the scheduled backup:
   # 30 19 * * * ITK_BACKUP_REMOTE=backup@nas.lab:/srv/itk-cloud /opt/itk-cloud/current/installer/backup.sh --sync-only
   ```
   `--sync` uses `rsync -a --partial` **without** `--delete` and excludes `*.partial`, so the
   remote keeps files deleted locally by retention; prune the remote with its own policy. The
   files are already encrypted, so the remote does not need to be trusted with plaintext.

Recommended: 3 copies (server + NAS + another building/server), 2 media, 1 off-site.

## 5. Restore overview

| Kind | Web (`POST /api/v1/admin/backups/{id}/restore`) | CLI / manual |
|---|---|---|
| `workspace_files` | **Restore** (all workspaces in the archive, or one `workspace_id`) | decrypt + extract (§6.4) |
| `project_databases` | **Restore** into an existing `ready` database (`database_id`) | decrypt + `pg_restore` (§6.5) |
| `platform_db` | **Verify only** (decrypt, checksum, `pg_restore --list`) | `itkctl restore platform-db` (§6.3) |
| `config` | **Verify only** (decrypt + checksum) | `itkctl backup decrypt` + extract (§6.6) |
| `workspace_metadata` | **Verify only** | `itkctl backup decrypt` (§6.6) |

Web restores require `backup.manage`, typing the backup **file name** as confirmation, the
**private key**, and a **reason**; they are audited (`backup.restore`,
`backup.restore_completed`). Progress is shown in `restore_status`
(`queued → running → succeeded|failed`, `restore_error`). Before decrypting, the file's SHA-256
is compared with the recorded checksum ("backup checksum mismatch" otherwise). Restores read
from `BACKUP_ROOT`; if a file only exists in the copy location, copy it back first.

## 6. Restore procedures

### 6.1 Workspace files (web)

1. *Administration → Backups*, pick a `workspace_files` backup → *Restore*.
2. Optionally choose one workspace; type the file name, paste the private key, give a reason.
3. The agent decrypts and extracts the archive into the existing workspaces' data
   directories: files in the archive overwrite current files; files not in the archive are
   kept. The target workspaces must still exist (provisioned storage); otherwise the restore
   fails with "workspace … storage is not provisioned".
4. Users should stop workloads that write to the same files before the restore.

### 6.2 Project database (web)

1. Pick a `project_databases` backup → *Restore*, select the target database (must be
   `ready`, and the archive must contain a dump with the same `db_name`).
2. The worker runs `pg_restore --clean --if-exists --no-owner --no-privileges --role=<db role>`
   into that database (objects in the dump are dropped and recreated). Connections from
   workspaces should be stopped first.

### 6.3 Full platform database restore (CLI)

Use after data corruption, a failed migration or in disaster recovery. It **overwrites** the
platform database.

```bash
sudo systemctl stop itk-cloud-web itk-cloud-worker itk-cloud-api
# put the private key in a root-only temporary file (from the password manager)
sudo install -m 0600 /dev/null /root/restore-key.txt && sudo nano /root/restore-key.txt   # paste AGE-SECRET-KEY-1...
sudo itkctl restore platform-db --identity-file /root/restore-key.txt \
     --file /srv/itk-cloud/backups/itk-platform-db-20261101T180000Z-1a2b3c4d.dump.age
#   -> prompts: type RESTORE (use --yes to skip)
sudo itkctl migrate     # bring the schema to the current version
sudo systemctl start itk-cloud-api itk-cloud-worker itk-cloud-web
sudo shred -u /root/restore-key.txt
```

`itkctl restore platform-db` decrypts to a temporary file and runs
`pg_restore --clean --if-exists --no-owner --no-privileges --single-transaction --exit-on-error`
with the credentials of `DATABASE_URL` (passed via environment variables). `pg_restore` must be
installed (`postgresql-client` of the server's major version). The identity file may contain
the bare key or the `# private key: …` line from `itkctl backup keygen`.

After restoring an **older** database, reconcile with the host state:

- workspaces/containers created after the backup exist on disk/Docker but not in the
  database — list managed containers with
  `docker ps -a --filter label=itk.managed=true --format '{{.Names}} {{.Label "itk.workspace_id"}}'`
  and workspace directories under `/srv/itk-cloud/workspaces`, compare with the restored
  database, and remove orphans deliberately;
- workspaces deleted after the backup appear again but their storage is gone (see the
  re-provisioning step in §7);
- sessions in the backup are old; users simply log in again.

### 6.4 Workspace files (manual)

For files of a workspace that no longer exists, or to inspect an archive:

```bash
sudo itkctl backup decrypt --identity-file /root/restore-key.txt \
     --in /srv/itk-cloud/backups/itk-workspace-files-….tar.gz.age --out /root/ws-files.tar.gz
sudo tar -tzf /root/ws-files.tar.gz | head                     # entries: <workspace id>/<path>
sudo mkdir /root/ws-restore && sudo tar -xzf /root/ws-files.tar.gz -C /root/ws-restore <old workspace id>/
```

Then give the files to the user, e.g. upload them through the file manager into a new
workspace, or copy them as root into the new workspace's data directory
(`/srv/itk-cloud/workspaces/<new id>/data/`) and `chown -R 1000:1000` them. Delete the
plaintext copies afterwards (`shred`/`rm -rf`).

### 6.5 Project database (manual)

When the target database no longer exists (e.g. a deleted project database or a rebuilt
server):

```bash
sudo itkctl backup decrypt --identity-file /root/restore-key.txt \
     --in /srv/itk-cloud/backups/itk-project-databases-….tar.age --out /root/projdb.tar
mkdir /tmp/projdb && tar -xf /root/projdb.tar -C /tmp/projdb      # <db_name>.dump files
# recreate role and database with the original names (as postgres), then restore:
sudo -u postgres psql -c "CREATE ROLE itk_x LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE CONNECTION LIMIT 20 PASSWORD 'temporary'"
sudo -u postgres createdb -O itk_x -E UTF8 itk_x
sudo -u postgres psql -c 'REVOKE ALL ON DATABASE itk_x FROM PUBLIC' -c 'GRANT CONNECT, TEMPORARY ON DATABASE itk_x TO itk_x'
sudo -u postgres pg_restore --no-owner --no-privileges --role=itk_x -d itk_x /tmp/projdb/itk_x.dump
```

If the `databases` row still exists and is `ready`, a maintainer then uses *Reset
credentials* in the console to set a fresh password. Remove the plaintext dumps afterwards.

### 6.6 Configuration and metadata

```bash
sudo itkctl backup decrypt --identity-file /root/restore-key.txt \
     --in /srv/itk-cloud/backups/itk-config-….tar.gz.age --out /root/config.tar.gz
sudo mkdir /root/config-restore && sudo tar -xzf /root/config.tar.gz -C /root/config-restore
sudo diff /root/config-restore/config/itk-cloud.env /etc/itk-cloud/itk-cloud.env
# copy the values you need; keep ownership/permissions:
sudo install -m 0640 -o root -g itk-cloud /root/config-restore/config/itk-cloud.env /etc/itk-cloud/itk-cloud.env
```

`agent.env` is not part of the archive; it is derived from `itk-cloud.env`
(`AGENT_SHARED_SECRET` must match). Metadata: decrypt the `.json.age` the same way and inspect
it with `jq` (e.g. `jq '.workspaces[] | {id,name,owner_id,status}'`) to reconcile ownership
after an incident.

### 6.7 Verification (web)

For `platform_db`, `config` and `workspace_metadata`, the web *Restore* action runs a
**verification**: checksum, full decryption and, for `platform_db`, `pg_restore --list`
(must contain table data). A wrong key fails verification (tested). Use it monthly (§8).

## 7. Disaster recovery runbook (server lost)

Prerequisites: off-server copies of the latest backups (§4), the backup private key, the
hardware inventory and these docs.

1. **Prepare the replacement server**: Ubuntu Server 24.04, same hostname/IP if possible
   (otherwise update DNS), NVIDIA driver if GPUs are used.
2. **Install the same Jenderal Cloud OS version** with the installer and the **same domain**
   (`sudo ./installer/install.sh --domain <same> …`). Skip creating real users — the restored
   database brings them back (a temporary admin is fine).
3. **Stop the platform**: `sudo systemctl stop itk-cloud-web itk-cloud-worker itk-cloud-api`.
4. **Copy backups** into `/srv/itk-cloud/backups/` (owner root:itk-cloud, files 0600 or
   0640 as copied).
5. **Restore secrets from the config backup** (§6.6). Keep the new install's
   `DATABASE_URL`, `REDIS_URL`, `PROJECT_DB_ADMIN_URL` and `ITK_*_PASSWORD` values; copy from
   the backup `ENCRYPTION_KEY` (**required** — otherwise TOTP secrets and stored database
   passwords cannot be decrypted), `SESSION_SECRET`, `AGENT_SHARED_SECRET` (also into
   `/etc/itk-cloud/agent.env`), `BACKUP_AGE_RECIPIENT`, SMTP and any tuned values. Restart the
   agent: `sudo systemctl restart itk-cloud-agent`.
6. **Restore the platform database** (§6.3) and run `itkctl migrate`.
7. **Re-provision workspace storage.** The restored database lists workspaces whose volumes
   and containers do not exist on the new host. Mark them for re-provisioning and clear the
   stale container references, then start the services:
   ```sql
   -- psql as postgres on itk_cloud, after reviewing the affected rows
   UPDATE containers SET runtime_id = NULL, status = 'stopped', updated_at = now()
     WHERE removed_at IS NULL;
   UPDATE workspaces SET status = 'failed', status_reason = 'Restored from backup: re-provisioning required', updated_at = now()
     WHERE status IN ('requested','provisioning','ready','running','stopped','suspended');
   ```
   ```bash
   sudo systemctl start itk-cloud-api itk-cloud-worker itk-cloud-web
   sudo installer/build-workspace-images.sh        # all images used by the templates
   ```
   Then use *Retry* on each workspace (`POST /api/v1/workspaces/{id}/retry`), which recreates
   the volume and containers (`ready`).
8. **Restore workspace files** from the latest `workspace_files` backup via the web (§6.1).
9. **Restore project databases** manually (§6.5); maintainers reset credentials.
10. **TLS**: the new Caddy instance has a **new internal CA** unless
    `/var/lib/caddy/.local/share/caddy/pki` was preserved; redistribute the root certificate
    ([installation.md §7](installation.md#7-tls-and-caddy)).
11. **Verify** (installation §10), announce availability, and record the drill/incident
    (times, problems) to refine RPO/RTO.

## 8. RPO, RTO and restore testing schedule

Realistic objectives with the default configuration (to be confirmed by drills on the
staging server):

| Data | Default protection | RPO |
|---|---|---|
| Platform DB, config, workspace metadata, project databases | Daily scheduled backup + pre-upgrade backups | ≤ 24 h |
| Workspace files | **Manual only** unless `backup.include_workspace_files=true` | Time since the last manual backup (≤ 24 h when enabled) |
| Off-server protection | `BACKUP_COPY_DIR` copy at backup time; rsync on its schedule | ≤ 24 h + sync interval |

| Scenario | RTO target |
|---|---|
| Service crash | Minutes (systemd restarts it automatically) |
| Corrupted platform DB on an intact server | ~1 hour (restore + migrate + checks), more for large audit logs |
| Single project database / workspace files | < 1 hour per item |
| Complete server loss | ~1 working day (hardware/OS, install, image builds, data restore; dominated by workspace data volume and image build time) |

Restore testing schedule:

| When | Test | Who |
|---|---|---|
| Monthly | Web **verify** of the latest `platform_db` backup with the offline key | Super administrator |
| Monthly | Restore one `workspace_files` backup into a test workspace and one project database into a test database | Administrator with `backup.manage` |
| Quarterly | Full DR drill (§7) on the staging server from off-server copies; measure RTO | Two administrators |
| After every upgrade | Confirm the pre-upgrade backup exists and verifies | Operator performing the upgrade |
| Yearly / on staff change | Key custody check: both stored copies of the private key decrypt a current backup | Lab head + administrator |

Record each test (date, backup, result, duration) in the lab operations log.
