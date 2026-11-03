-- +goose Up
-- ITK Cloud initial schema. All timestamps are timestamptz (stored UTC).

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------------------
-- Identity & access
-- ---------------------------------------------------------------------------

CREATE TABLE resource_quotas (
    id                          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key                         text NOT NULL UNIQUE CHECK (key ~ '^[a-z0-9][a-z0-9_-]{1,62}$'),
    name                        text NOT NULL,
    description                 text NOT NULL DEFAULT '',
    cpu_millis                  integer NOT NULL CHECK (cpu_millis > 0),
    memory_bytes                bigint  NOT NULL CHECK (memory_bytes > 0),
    storage_bytes               bigint  NOT NULL CHECK (storage_bytes > 0),
    max_workspaces              integer NOT NULL CHECK (max_workspaces >= 0),
    max_containers              integer NOT NULL CHECK (max_containers >= 0),
    max_databases               integer NOT NULL CHECK (max_databases >= 0),
    max_terminal_sessions       integer NOT NULL CHECK (max_terminal_sessions >= 0),
    gpu_allowed                 boolean NOT NULL DEFAULT false,
    max_workspace_duration_min  integer CHECK (max_workspace_duration_min IS NULL OR max_workspace_duration_min > 0),
    idle_timeout_min            integer CHECK (idle_timeout_min IS NULL OR idle_timeout_min > 0),
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username              text NOT NULL CHECK (username ~ '^[a-z][a-z0-9._-]{2,31}$'),
    email                 text NOT NULL CHECK (position('@' in email) > 1),
    full_name             text NOT NULL CHECK (length(full_name) BETWEEN 1 AND 200),
    identity_number       text CHECK (identity_number IS NULL OR length(identity_number) <= 50),
    password_hash         text NOT NULL,
    status                text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    suspended_reason      text,
    must_change_password  boolean NOT NULL DEFAULT false,
    quota_id              uuid REFERENCES resource_quotas(id) ON DELETE SET NULL,
    failed_login_count    integer NOT NULL DEFAULT 0,
    locked_until          timestamptz,
    last_login_at         timestamptz,
    password_changed_at   timestamptz NOT NULL DEFAULT now(),
    created_by            uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_username_key ON users (lower(username));
CREATE UNIQUE INDEX users_email_key ON users (lower(email));
CREATE INDEX users_status_idx ON users (status);

CREATE TABLE roles (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key           text NOT NULL UNIQUE CHECK (key ~ '^[a-z][a-z0-9_]{1,40}$'),
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    is_system     boolean NOT NULL DEFAULT false,
    requires_mfa  boolean NOT NULL DEFAULT false,
    -- rank orders roles by privilege; users may only grant roles of lower rank.
    rank          integer NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE permissions (
    key          text PRIMARY KEY CHECK (key ~ '^[a-z_]+(\.[a-z_]+)+$'),
    category     text NOT NULL,
    description  text NOT NULL
);

CREATE TABLE role_permissions (
    role_id         uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_key  text NOT NULL REFERENCES permissions(key) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_key)
);

CREATE TABLE user_roles (
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id     uuid NOT NULL REFERENCES roles(id) ON DELETE RESTRICT,
    granted_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    granted_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role_id)
);
CREATE INDEX user_roles_role_idx ON user_roles (role_id);

CREATE TABLE sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash    bytea NOT NULL UNIQUE,
    csrf_token    text NOT NULL,
    mfa_verified  boolean NOT NULL DEFAULT false,
    ip            text,
    user_agent    text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz
);
CREATE INDEX sessions_user_idx ON sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE mfa_methods (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type                text NOT NULL DEFAULT 'totp' CHECK (type IN ('totp')),
    secret_encrypted    bytea NOT NULL,
    confirmed_at        timestamptz,
    last_used_step      bigint NOT NULL DEFAULT 0,
    recovery_codes      text[] NOT NULL DEFAULT '{}',  -- SHA-256 hex of unused codes
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX mfa_methods_user_type_key ON mfa_methods (user_id, type);

CREATE TABLE password_reset_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  bytea NOT NULL UNIQUE,
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_tokens_user_idx ON password_reset_tokens (user_id);

-- ---------------------------------------------------------------------------
-- Projects
-- ---------------------------------------------------------------------------

CREATE TABLE projects (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug         text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,48}[a-z0-9]$'),
    name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    description  text NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
    owner_id     uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    quota_id     uuid REFERENCES resource_quotas(id) ON DELETE SET NULL,
    status       text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX projects_owner_idx ON projects (owner_id);

CREATE TABLE project_members (
    project_id  uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role        text NOT NULL CHECK (role IN ('owner', 'maintainer', 'member', 'viewer')),
    added_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX project_members_user_idx ON project_members (user_id);

-- ---------------------------------------------------------------------------
-- Images, templates, workspaces, containers
-- ---------------------------------------------------------------------------

CREATE TABLE container_images (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL UNIQUE,
    reference    text NOT NULL UNIQUE CHECK (reference ~ '^[a-z0-9][a-z0-9._/:@-]{1,250}$'),
    description  text NOT NULL DEFAULT '',
    gpu_capable  boolean NOT NULL DEFAULT false,
    is_allowed   boolean NOT NULL DEFAULT true,
    pulled_at    timestamptz,
    size_bytes   bigint,
    created_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workspace_templates (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key                    text NOT NULL UNIQUE CHECK (key ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
    name                   text NOT NULL,
    description            text NOT NULL DEFAULT '',
    category               text NOT NULL DEFAULT 'general',
    image_id               uuid NOT NULL REFERENCES container_images(id) ON DELETE RESTRICT,
    runtime_versions       jsonb NOT NULL DEFAULT '{}',
    min_cpu_millis         integer NOT NULL CHECK (min_cpu_millis > 0),
    min_memory_bytes       bigint  NOT NULL CHECK (min_memory_bytes > 0),
    rec_cpu_millis         integer NOT NULL CHECK (rec_cpu_millis >= min_cpu_millis),
    rec_memory_bytes       bigint  NOT NULL CHECK (rec_memory_bytes >= min_memory_bytes),
    default_storage_bytes  bigint  NOT NULL CHECK (default_storage_bytes > 0),
    ports                  jsonb NOT NULL DEFAULT '[]',
    gpu_mode               text NOT NULL DEFAULT 'none' CHECK (gpu_mode IN ('none', 'optional', 'required')),
    security               jsonb NOT NULL DEFAULT '{}',
    env                    jsonb NOT NULL DEFAULT '{}',
    command                jsonb NOT NULL DEFAULT '[]',
    is_active              boolean NOT NULL DEFAULT true,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workspaces (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    slug                  text NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
    owner_id              uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    project_id            uuid REFERENCES projects(id) ON DELETE SET NULL,
    template_id           uuid NOT NULL REFERENCES workspace_templates(id) ON DELETE RESTRICT,
    type                  text NOT NULL DEFAULT 'container' CHECK (type IN ('container', 'native')),
    status                text NOT NULL DEFAULT 'requested' CHECK (status IN
                            ('requested','provisioning','ready','running','stopped','suspended','failed','deleting','deleted')),
    status_reason         text,
    cpu_millis            integer NOT NULL CHECK (cpu_millis > 0),
    memory_bytes          bigint  NOT NULL CHECK (memory_bytes > 0),
    storage_bytes         bigint  NOT NULL CHECK (storage_bytes > 0),
    pids_limit            integer NOT NULL DEFAULT 512 CHECK (pids_limit BETWEEN 32 AND 32768),
    idle_timeout_min      integer CHECK (idle_timeout_min IS NULL OR idle_timeout_min > 0),
    max_duration_min      integer CHECK (max_duration_min IS NULL OR max_duration_min > 0),
    storage_used_bytes    bigint,
    last_activity_at      timestamptz,
    started_at            timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    deleted_at            timestamptz
);
CREATE UNIQUE INDEX workspaces_owner_slug_key ON workspaces (owner_id, slug) WHERE deleted_at IS NULL;
CREATE INDEX workspaces_owner_idx ON workspaces (owner_id);
CREATE INDEX workspaces_project_idx ON workspaces (project_id);
CREATE INDEX workspaces_status_idx ON workspaces (status);

CREATE TABLE workspace_members (
    workspace_id  uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role          text NOT NULL CHECK (role IN ('viewer', 'editor')),
    added_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);
CREATE INDEX workspace_members_user_idx ON workspace_members (user_id);

CREATE TABLE containers (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    owner_id        uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    template_id     uuid NOT NULL REFERENCES workspace_templates(id) ON DELETE RESTRICT,
    image_id        uuid NOT NULL REFERENCES container_images(id) ON DELETE RESTRICT,
    name            text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$'),
    is_primary      boolean NOT NULL DEFAULT false,
    runtime_id      text,          -- Docker container ID
    status          text NOT NULL DEFAULT 'created' CHECK (status IN
                      ('created','starting','running','stopping','stopped','failed','removing','removed')),
    status_reason   text,
    cpu_millis      integer NOT NULL CHECK (cpu_millis > 0),
    memory_bytes    bigint  NOT NULL CHECK (memory_bytes > 0),
    pids_limit      integer NOT NULL CHECK (pids_limit > 0),
    ports           jsonb NOT NULL DEFAULT '[]',
    gpu_device_ids  text[] NOT NULL DEFAULT '{}',
    started_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    removed_at      timestamptz
);
CREATE UNIQUE INDEX containers_workspace_name_key ON containers (workspace_id, name) WHERE removed_at IS NULL;
CREATE UNIQUE INDEX containers_primary_key ON containers (workspace_id) WHERE is_primary AND removed_at IS NULL;
CREATE INDEX containers_owner_idx ON containers (owner_id);
CREATE INDEX containers_status_idx ON containers (status);

CREATE TABLE terminal_sessions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id   uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    container_id   uuid REFERENCES containers(id) ON DELETE SET NULL,
    ticket_hash    bytea UNIQUE,
    status         text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','active','closed','expired')),
    client_ip      text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    connected_at   timestamptz,
    last_activity_at timestamptz,
    expires_at     timestamptz NOT NULL,
    ended_at       timestamptz,
    end_reason     text
);
CREATE INDEX terminal_sessions_user_idx ON terminal_sessions (user_id, status);
CREATE INDEX terminal_sessions_workspace_idx ON terminal_sessions (workspace_id);

-- ---------------------------------------------------------------------------
-- GPU
-- ---------------------------------------------------------------------------

CREATE TABLE gpu_devices (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    uuid                text NOT NULL UNIQUE,
    index               integer NOT NULL,
    name                text NOT NULL,
    driver_version      text,
    cuda_version        text,
    memory_total_bytes  bigint,
    memory_used_bytes   bigint,
    utilization_pct     integer,
    temperature_c       integer,
    mig_mode            text,
    allocation_policy   text NOT NULL DEFAULT 'exclusive' CHECK (allocation_policy IN ('exclusive', 'shared', 'disabled')),
    max_shared          integer NOT NULL DEFAULT 1 CHECK (max_shared >= 1),
    status              text NOT NULL DEFAULT 'available' CHECK (status IN ('available', 'missing', 'disabled')),
    last_seen_at        timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE gpu_requests (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    requester_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id     uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    gpu_device_id    uuid REFERENCES gpu_devices(id) ON DELETE SET NULL,
    mode             text NOT NULL DEFAULT 'exclusive' CHECK (mode IN ('exclusive', 'shared')),
    duration_hours   integer NOT NULL CHECK (duration_hours BETWEEN 1 AND 720),
    reason           text NOT NULL CHECK (length(reason) BETWEEN 10 AND 2000),
    status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','cancelled')),
    reviewed_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    review_note      text,
    reviewed_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX gpu_requests_status_idx ON gpu_requests (status, created_at);
CREATE INDEX gpu_requests_requester_idx ON gpu_requests (requester_id);

CREATE TABLE gpu_allocations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id      uuid REFERENCES gpu_requests(id) ON DELETE SET NULL,
    gpu_device_id   uuid NOT NULL REFERENCES gpu_devices(id) ON DELETE RESTRICT,
    workspace_id    uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode            text NOT NULL CHECK (mode IN ('exclusive', 'shared')),
    status          text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'released', 'expired')),
    starts_at       timestamptz NOT NULL DEFAULT now(),
    ends_at         timestamptz NOT NULL,
    released_at     timestamptz,
    released_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    release_reason  text,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX gpu_allocations_active_idx ON gpu_allocations (gpu_device_id) WHERE status = 'active';
CREATE INDEX gpu_allocations_workspace_idx ON gpu_allocations (workspace_id);
CREATE UNIQUE INDEX gpu_allocations_exclusive_key ON gpu_allocations (gpu_device_id) WHERE status = 'active' AND mode = 'exclusive';

-- ---------------------------------------------------------------------------
-- Resources & monitoring
-- ---------------------------------------------------------------------------

CREATE TABLE resource_usage_samples (
    id            bigserial PRIMARY KEY,
    scope         text NOT NULL CHECK (scope IN ('host', 'workspace', 'container', 'gpu')),
    ref_id        text NOT NULL DEFAULT '',
    cpu_pct       double precision,
    memory_bytes  bigint,
    memory_total_bytes bigint,
    disk_used_bytes  bigint,
    disk_total_bytes bigint,
    net_rx_bytes_per_sec double precision,
    net_tx_bytes_per_sec double precision,
    gpu_util_pct  double precision,
    gpu_memory_bytes bigint,
    gpu_temperature_c double precision,
    load1         double precision,
    collected_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX resource_usage_samples_lookup_idx ON resource_usage_samples (scope, ref_id, collected_at DESC);
CREATE INDEX resource_usage_samples_time_idx ON resource_usage_samples (collected_at);

CREATE TABLE alerts (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rule           text NOT NULL,
    subject        text NOT NULL DEFAULT '',
    severity       text NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    status         text NOT NULL CHECK (status IN ('firing', 'resolved')),
    message        text NOT NULL,
    value          double precision,
    started_at     timestamptz NOT NULL DEFAULT now(),
    resolved_at    timestamptz,
    acknowledged_by uuid REFERENCES users(id) ON DELETE SET NULL,
    acknowledged_at timestamptz
);
CREATE UNIQUE INDEX alerts_firing_key ON alerts (rule, subject) WHERE status = 'firing';
CREATE INDEX alerts_started_idx ON alerts (started_at DESC);

-- ---------------------------------------------------------------------------
-- Project databases
-- ---------------------------------------------------------------------------

CREATE TABLE databases (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id          uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    created_by          uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    display_name        text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 80),
    db_name             text NOT NULL UNIQUE CHECK (db_name ~ '^itk_[a-z0-9_]{3,55}$'),
    role_name           text NOT NULL UNIQUE CHECK (role_name ~ '^itk_[a-z0-9_]{3,55}$'),
    password_encrypted  bytea,
    engine              text NOT NULL DEFAULT 'postgresql' CHECK (engine IN ('postgresql')),
    status              text NOT NULL DEFAULT 'provisioning' CHECK (status IN ('provisioning','ready','failed','deleting','deleted')),
    status_reason       text,
    size_bytes          bigint,
    connection_limit    integer NOT NULL DEFAULT 20 CHECK (connection_limit BETWEEN 1 AND 200),
    last_backup_at      timestamptz,
    last_backup_status  text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    deleted_at          timestamptz
);
CREATE INDEX databases_project_idx ON databases (project_id);

-- ---------------------------------------------------------------------------
-- Notifications, announcements, audit
-- ---------------------------------------------------------------------------

CREATE TABLE notifications (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type        text NOT NULL CHECK (type IN ('system','workspace','gpu','quota','maintenance','security','backup','alert')),
    severity    text NOT NULL DEFAULT 'info' CHECK (severity IN ('info','success','warning','critical')),
    title       text NOT NULL,
    body        text NOT NULL DEFAULT '',
    link        text,
    read_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notifications_user_idx ON notifications (user_id, created_at DESC);
CREATE INDEX notifications_unread_idx ON notifications (user_id) WHERE read_at IS NULL;

CREATE TABLE announcements (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title       text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    body        text NOT NULL CHECK (length(body) <= 5000),
    severity    text NOT NULL DEFAULT 'info' CHECK (severity IN ('info','warning','critical')),
    starts_at   timestamptz NOT NULL DEFAULT now(),
    ends_at     timestamptz,
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
    id              bigserial PRIMARY KEY,
    occurred_at     timestamptz NOT NULL DEFAULT now(),
    actor_id        uuid,
    actor_username  text,
    action          text NOT NULL,
    resource_type   text NOT NULL DEFAULT '',
    resource_id     text NOT NULL DEFAULT '',
    outcome         text NOT NULL CHECK (outcome IN ('success','failure','denied')),
    reason          text,
    ip              text,
    user_agent      text,
    request_id      text,
    metadata        jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_logs_time_idx ON audit_logs (occurred_at DESC);
CREATE INDEX audit_logs_actor_idx ON audit_logs (actor_id, occurred_at DESC);
CREATE INDEX audit_logs_resource_idx ON audit_logs (resource_type, resource_id);
CREATE INDEX audit_logs_action_idx ON audit_logs (action);

-- Audit log is append-only for the application. Retention purges run with
-- the session variable itk.audit_retention = 'on', set only by the worker's
-- retention job inside its own transaction.
-- +goose StatementBegin
CREATE FUNCTION audit_logs_append_only() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' AND current_setting('itk.audit_retention', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'audit_logs is append-only';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER audit_logs_no_update BEFORE UPDATE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_append_only();
CREATE TRIGGER audit_logs_no_delete BEFORE DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_append_only();
CREATE TRIGGER audit_logs_no_truncate BEFORE TRUNCATE ON audit_logs
    FOR EACH STATEMENT EXECUTE FUNCTION audit_logs_append_only();

-- ---------------------------------------------------------------------------
-- Operations
-- ---------------------------------------------------------------------------

CREATE TABLE backup_jobs (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind             text NOT NULL CHECK (kind IN ('platform_db','config','workspace_metadata','workspace_files','project_databases','full')),
    status           text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed','expired')),
    trigger          text NOT NULL DEFAULT 'manual' CHECK (trigger IN ('manual','scheduled','pre_upgrade')),
    file_name        text,
    size_bytes       bigint,
    checksum_sha256  text,
    encrypted        boolean NOT NULL DEFAULT false,
    error            text,
    triggered_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    started_at       timestamptz,
    finished_at      timestamptz,
    expires_at       timestamptz,
    restore_status   text CHECK (restore_status IS NULL OR restore_status IN ('queued','running','succeeded','failed')),
    restored_at      timestamptz,
    restored_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    restore_error    text,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX backup_jobs_created_idx ON backup_jobs (created_at DESC);

CREATE TABLE agent_nodes (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name              text NOT NULL UNIQUE,
    hostname          text,
    agent_version     text,
    protocol_version  integer,
    os                text,
    kernel            text,
    arch              text,
    capabilities      jsonb NOT NULL DEFAULT '{}',
    status            text NOT NULL DEFAULT 'unknown' CHECK (status IN ('online','offline','degraded','unknown')),
    last_error        text,
    last_seen_at      timestamptz,
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE platform_settings (
    key         text PRIMARY KEY,
    value       jsonb NOT NULL,
    updated_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE background_jobs (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind             text NOT NULL,
    payload          jsonb NOT NULL DEFAULT '{}',
    status           text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed','dead','cancelled')),
    attempts         integer NOT NULL DEFAULT 0,
    max_attempts     integer NOT NULL DEFAULT 5 CHECK (max_attempts >= 1),
    run_at           timestamptz NOT NULL DEFAULT now(),
    locked_at        timestamptz,
    locked_by        text,
    last_error       text,
    idempotency_key  text UNIQUE,
    resource_type    text,
    resource_id      text,
    created_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    finished_at      timestamptz
);
CREATE INDEX background_jobs_ready_idx ON background_jobs (run_at) WHERE status = 'queued';
CREATE INDEX background_jobs_resource_idx ON background_jobs (resource_type, resource_id);
CREATE INDEX background_jobs_status_idx ON background_jobs (status, updated_at DESC);

CREATE TABLE api_idempotency_keys (
    key           text NOT NULL,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method        text NOT NULL,
    path          text NOT NULL,
    status_code   integer NOT NULL,
    response      jsonb NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);

-- +goose Down
DROP TABLE IF EXISTS api_idempotency_keys;
DROP TABLE IF EXISTS background_jobs;
DROP TABLE IF EXISTS platform_settings;
DROP TABLE IF EXISTS agent_nodes;
DROP TABLE IF EXISTS backup_jobs;
DROP TABLE IF EXISTS audit_logs;
DROP FUNCTION IF EXISTS audit_logs_append_only();
DROP TABLE IF EXISTS announcements;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS databases;
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS resource_usage_samples;
DROP TABLE IF EXISTS gpu_allocations;
DROP TABLE IF EXISTS gpu_requests;
DROP TABLE IF EXISTS gpu_devices;
DROP TABLE IF EXISTS terminal_sessions;
DROP TABLE IF EXISTS containers;
DROP TABLE IF EXISTS workspace_members;
DROP TABLE IF EXISTS workspaces;
DROP TABLE IF EXISTS workspace_templates;
DROP TABLE IF EXISTS container_images;
DROP TABLE IF EXISTS project_members;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS password_reset_tokens;
DROP TABLE IF EXISTS mfa_methods;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS resource_quotas;
