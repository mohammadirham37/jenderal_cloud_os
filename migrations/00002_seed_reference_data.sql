-- +goose Up
-- Reference data: permissions, built-in roles, quota packages, workspace
-- image catalog, templates and default platform settings. No user accounts
-- or credentials are seeded; the first administrator is created with
-- `itkctl admin create`.

INSERT INTO permissions (key, category, description) VALUES
  ('users.view',               'users',         'View user accounts'),
  ('users.lookup',             'users',         'Search users by name to add them as members'),
  ('users.create',             'users',         'Create user accounts'),
  ('users.update',             'users',         'Update user profiles and quota assignment'),
  ('users.suspend',            'users',         'Suspend or reactivate user accounts'),
  ('users.reset_password',     'users',         'Issue password reset links for users'),
  ('users.assign_roles',       'users',         'Assign lower-ranked roles to users'),
  ('roles.view',               'roles',         'View roles and permissions'),
  ('roles.manage',             'roles',         'Create and modify roles and their permissions'),
  ('projects.create',          'projects',      'Create projects'),
  ('projects.view_all',        'projects',      'View every project'),
  ('projects.manage_all',      'projects',      'Manage every project and its members'),
  ('workspaces.view_own',      'workspaces',    'View own and shared workspaces'),
  ('workspaces.view_all',      'workspaces',    'View metadata of every workspace'),
  ('workspaces.create',        'workspaces',    'Create workspaces'),
  ('workspaces.delete',        'workspaces',    'Delete own workspaces'),
  ('workspaces.manage_all',    'workspaces',    'Start/stop/suspend/delete any workspace'),
  ('workspaces.access_all',    'workspaces',    'Emergency access to terminal and files of any workspace (requires reason)'),
  ('workspaces.native',        'workspaces',    'Create Linux-native (host) workspaces'),
  ('templates.view',           'templates',     'View the workspace template catalog'),
  ('templates.manage',         'templates',     'Manage workspace templates'),
  ('images.view',              'images',        'View the container image allowlist'),
  ('images.manage',            'images',        'Manage the container image allowlist'),
  ('containers.create',        'containers',    'Create containers in own workspaces'),
  ('containers.stop',          'containers',    'Start/stop/restart containers in own workspaces'),
  ('containers.manage_all',    'containers',    'Manage every container'),
  ('terminals.open',           'terminals',     'Open terminals in accessible workspaces'),
  ('files.manage',             'files',         'Manage files in accessible workspaces'),
  ('databases.create',         'databases',     'Create project databases'),
  ('databases.delete',         'databases',     'Delete project databases the user manages'),
  ('databases.manage_all',     'databases',     'Manage every project database'),
  ('gpu.view',                 'gpu',           'View GPU inventory and own allocations'),
  ('gpu.request',              'gpu',           'Request GPU access'),
  ('gpu.approve',              'gpu',           'Approve or reject GPU requests and release allocations'),
  ('gpu.manage',               'gpu',           'Manage GPU devices and allocation policy'),
  ('quotas.view',              'quotas',        'View quota packages'),
  ('quotas.manage',            'quotas',        'Manage quota packages'),
  ('monitoring.view_host',     'monitoring',    'View host and platform-wide monitoring'),
  ('announcements.manage',     'notifications', 'Publish announcements and broadcast notifications'),
  ('host.operations.view',     'host',          'View host and agent status'),
  ('host.operations.execute',  'host',          'Run allow-listed host operations (emergency stop)'),
  ('audit.view',               'audit',         'View audit logs'),
  ('backup.manage',            'backup',        'Create backups and restore'),
  ('settings.manage',          'settings',      'Change platform settings'),
  ('agent.view',               'host',          'View host agent status');

INSERT INTO roles (key, name, description, is_system, requires_mfa, rank) VALUES
  ('super_admin', 'Super Administrator', 'Full platform control, including settings, backups and emergency operations.', true, true, 100),
  ('admin',       'Administrator',       'Manages users, workspaces, templates, quotas and monitoring.',                 true, true, 80),
  ('lecturer',    'Dosen / Researcher',  'Creates projects and workspaces, manages project databases, requests GPU.',   true, false, 40),
  ('assistant',   'Asisten Laboratorium','Monitors workloads and helps users without access to private data.',         true, false, 30),
  ('student',     'Mahasiswa',           'Uses own workspaces and projects within quota.',                               true, false, 10);

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM roles r CROSS JOIN permissions p WHERE r.key = 'super_admin';

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM roles r JOIN permissions p ON p.key IN (
  'users.view','users.lookup','users.create','users.update','users.suspend','users.reset_password','users.assign_roles',
  'roles.view','projects.create','projects.view_all','projects.manage_all',
  'workspaces.view_own','workspaces.view_all','workspaces.create','workspaces.delete','workspaces.manage_all',
  'templates.view','templates.manage','images.view','images.manage',
  'containers.create','containers.stop','containers.manage_all','terminals.open','files.manage',
  'databases.create','databases.delete','databases.manage_all',
  'gpu.view','gpu.request','gpu.approve','quotas.view','quotas.manage',
  'monitoring.view_host','announcements.manage','host.operations.view','audit.view','agent.view'
) WHERE r.key = 'admin';

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM roles r JOIN permissions p ON p.key IN (
  'users.lookup','projects.create','workspaces.view_own','workspaces.create','workspaces.delete',
  'templates.view','images.view','containers.create','containers.stop','terminals.open','files.manage',
  'databases.create','databases.delete','gpu.view','gpu.request','quotas.view'
) WHERE r.key = 'lecturer';

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM roles r JOIN permissions p ON p.key IN (
  'users.view','users.lookup','projects.create','workspaces.view_own','workspaces.view_all','workspaces.create','workspaces.delete',
  'templates.view','images.view','containers.create','containers.stop','terminals.open','files.manage',
  'gpu.view','gpu.request','quotas.view','monitoring.view_host','agent.view','host.operations.view'
) WHERE r.key = 'assistant';

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.key FROM roles r JOIN permissions p ON p.key IN (
  'users.lookup','projects.create','workspaces.view_own','workspaces.create','workspaces.delete',
  'templates.view','images.view','containers.create','containers.stop','terminals.open','files.manage',
  'gpu.view','gpu.request','quotas.view'
) WHERE r.key = 'student';

-- Example packages from the specification. Administrators must tune these to
-- the actual server capacity before production (see docs/operations.md).
INSERT INTO resource_quotas (key, name, description, cpu_millis, memory_bytes, storage_bytes,
  max_workspaces, max_containers, max_databases, max_terminal_sessions, gpu_allowed,
  max_workspace_duration_min, idle_timeout_min) VALUES
  ('basic',    'Basic',    '1 core, 1 GB RAM, 5 GB storage',   1000, 1073741824, 5368709120,  2,  3, 1, 3,  false, NULL, 120),
  ('standard', 'Standard', '2 core, 4 GB RAM, 20 GB storage',  2000, 4294967296, 21474836480, 3,  6, 2, 5,  false, NULL, 240),
  ('research', 'Research', '4 core, 8 GB RAM, 50 GB storage',  4000, 8589934592, 53687091200, 5, 10, 5, 10, true,  NULL, 480);

-- Workspace images are built locally from deployments/docker/workspace-images
-- (installer/build-workspace-images.sh). They run as the non-root user itk (1000).
INSERT INTO container_images (name, reference, description, gpu_capable) VALUES
  ('ITK Ubuntu Dev',       'itk-cloud/ubuntu-dev:1.0',       'Ubuntu 24.04 with build-essential, git, curl, vim, nano', false),
  ('ITK Go Dev',           'itk-cloud/go-dev:1.0',           'Go 1.27 on Debian bookworm', false),
  ('ITK PHP Laravel',      'itk-cloud/php-laravel:1.0',      'PHP 8.4 CLI, Composer, common extensions', false),
  ('ITK Node.js Dev',      'itk-cloud/node-dev:1.0',         'Node.js 24 LTS with npm, pnpm via corepack', false),
  ('ITK Python Dev',       'itk-cloud/python-dev:1.0',       'Python 3.13 with pip and venv', false),
  ('ITK Python Data Sci',  'itk-cloud/python-datascience:1.0','Python 3.13 with numpy, pandas, scikit-learn, matplotlib', false),
  ('ITK JupyterLab',       'itk-cloud/jupyterlab:1.0',       'JupyterLab on Python 3.13', false),
  ('ITK ML CUDA',          'itk-cloud/ml-cuda:1.0',          'CUDA 12.9 cuDNN runtime, Python 3, PyTorch (installed by user)', true),
  ('ITK C/C++ Dev',        'itk-cloud/cpp-dev:1.0',          'GCC, Clang, CMake, GDB, Valgrind', false),
  ('ITK Java Dev',         'itk-cloud/java-dev:1.0',         'Eclipse Temurin JDK 21, Maven, Gradle', false),
  ('ITK Web Dev',          'itk-cloud/web-dev:1.0',          'Node.js 24, PHP 8.4, Python 3 for full-stack web development', false);

INSERT INTO workspace_templates (key, name, description, category, image_id, runtime_versions,
  min_cpu_millis, min_memory_bytes, rec_cpu_millis, rec_memory_bytes, default_storage_bytes, ports, gpu_mode, security, env, command)
SELECT t.key, t.name, t.description, t.category, i.id, t.runtime::jsonb,
       t.min_cpu, t.min_mem, t.rec_cpu, t.rec_mem, t.storage, t.ports::jsonb, t.gpu_mode,
       '{"run_as_uid":1000,"run_as_gid":1000,"no_new_privileges":true,"read_only_rootfs":false,"cap_add":[]}'::jsonb,
       t.env::jsonb, t.command::jsonb
FROM (VALUES
  ('ubuntu-dev',  'Ubuntu Development',      'General purpose Ubuntu 24.04 shell environment.', 'general',      'itk-cloud/ubuntu-dev:1.0',        '{"ubuntu":"24.04"}',                         500, 536870912,  1000, 1073741824, 5368709120,  '[]', 'none', '{}', '[]'),
  ('go-dev',      'Go Development',          'Go toolchain with module cache in the workspace.', 'programming', 'itk-cloud/go-dev:1.0',            '{"go":"1.27"}',                              500, 536870912,  1000, 2147483648, 5368709120,  '[{"port":8080,"name":"http"}]', 'none', '{"GOPATH":"/home/itk/go"}', '[]'),
  ('php-laravel', 'PHP / Laravel Development','PHP 8.4 and Composer for Laravel projects.',   'web',          'itk-cloud/php-laravel:1.0',       '{"php":"8.4","composer":"2"}',               500, 536870912,  1000, 1073741824, 5368709120,  '[{"port":8000,"name":"artisan serve"}]', 'none', '{}', '[]'),
  ('nodejs-dev',  'Node.js Development',     'Node.js 24 LTS with npm and pnpm.',             'web',          'itk-cloud/node-dev:1.0',          '{"node":"24"}',                              500, 536870912,  1000, 2147483648, 5368709120,  '[{"port":3000,"name":"http"},{"port":5173,"name":"vite"}]', 'none', '{}', '[]'),
  ('python-dev',  'Python Development',      'Python 3.13 with pip and venv.',                'programming', 'itk-cloud/python-dev:1.0',        '{"python":"3.13"}',                          500, 536870912,  1000, 1073741824, 5368709120,  '[{"port":8000,"name":"http"}]', 'none', '{}', '[]'),
  ('python-datascience','Python Data Science','Scientific Python stack.',                     'data',        'itk-cloud/python-datascience:1.0','{"python":"3.13"}',                          1000, 1073741824, 2000, 4294967296, 10737418240, '[]', 'none', '{}', '[]'),
  ('jupyterlab',  'JupyterLab',              'JupyterLab served through the authenticated ITK Cloud proxy.', 'data', 'itk-cloud/jupyterlab:1.0',   '{"python":"3.13","jupyterlab":"4"}',         1000, 1073741824, 2000, 4294967296, 10737418240, '[{"port":8888,"name":"jupyterlab","proxy":true}]', 'optional', '{}', '[]'),
  ('ml-cuda',     'Machine Learning CUDA',   'CUDA runtime for GPU training (requires an approved GPU allocation).', 'ml', 'itk-cloud/ml-cuda:1.0',  '{"cuda":"12.9","python":"3"}',               2000, 4294967296, 4000, 8589934592, 21474836480, '[{"port":8888,"name":"jupyter"}]', 'required', '{}', '[]'),
  ('cpp-dev',     'C / C++ Development',     'GCC, Clang, CMake, GDB and Valgrind.',          'programming', 'itk-cloud/cpp-dev:1.0',           '{"gcc":"12","cmake":"3"}',                   500, 536870912,  1000, 1073741824, 5368709120,  '[]', 'none', '{}', '[]'),
  ('java-dev',    'Java Development',        'Temurin JDK 21 with Maven and Gradle.',         'programming', 'itk-cloud/java-dev:1.0',          '{"java":"21"}',                              1000, 1073741824, 2000, 2147483648, 5368709120,  '[{"port":8080,"name":"http"}]', 'none', '{}', '[]'),
  ('web-dev',     'Web Development',         'Node.js, PHP and Python for full-stack web work.', 'web',      'itk-cloud/web-dev:1.0',           '{"node":"24","php":"8.4","python":"3"}',     1000, 1073741824, 2000, 2147483648, 10737418240, '[{"port":3000,"name":"http"},{"port":5173,"name":"vite"},{"port":8000,"name":"php"}]', 'none', '{}', '[]')
) AS t(key, name, description, category, image_ref, runtime, min_cpu, min_mem, rec_cpu, rec_mem, storage, ports, gpu_mode, env, command)
JOIN container_images i ON i.reference = t.image_ref;

INSERT INTO platform_settings (key, value) VALUES
  ('platform.name',                         '"ITK Cloud"'),
  ('platform.organization',                 '"Informatika dan Teknik Komputer"'),
  ('security.require_mfa_for_admins',       'true'),
  ('security.login_max_attempts',           '5'),
  ('security.lockout_minutes',              '15'),
  ('quota.default_package',                 '"basic"'),
  ('workspaces.idle_stop_enabled',          'true'),
  ('workspaces.max_per_user_hard_limit',    '20'),
  ('gpu.max_duration_hours',                '72'),
  ('gpu.allow_shared',                      'false'),
  ('backup.schedule_enabled',               'true'),
  ('backup.schedule_hour_utc',              '18'),
  ('backup.include_workspace_files',        'false'),
  ('monitoring.sample_interval_seconds',    '30'),
  ('monitoring.retention_days',             '14'),
  ('alerts.disk_pct',                       '90'),
  ('alerts.memory_pct',                     '90'),
  ('alerts.cpu_pct',                        '90'),
  ('alerts.cpu_sustain_minutes',            '10'),
  ('alerts.gpu_temp_c',                     '85'),
  ('alerts.gpu_util_pct',                   '98'),
  ('alerts.queue_backlog',                  '50'),
  ('audit.retention_days',                  '365');

-- +goose Down
DELETE FROM platform_settings;
DELETE FROM workspace_templates;
DELETE FROM container_images;
DELETE FROM resource_quotas;
DELETE FROM role_permissions;
DELETE FROM roles;
DELETE FROM permissions;
