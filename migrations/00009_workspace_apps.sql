-- +goose Up
-- Built-in workspace apps (served through itk-app-gate inside the container):
--   * VS Code (code-server) on port 13337 for development templates;
--   * a remote desktop web browser (Firefox over noVNC) on port 6080.

UPDATE workspace_templates
   SET ports = ports || '[{"port":13337,"name":"vscode"}]'::jsonb, updated_at = now()
 WHERE key IN ('ubuntu-dev','go-dev','php-laravel','nodejs-dev','python-dev','python-datascience',
               'jupyterlab','ml-cuda','cpp-dev','java-dev','web-dev')
   AND NOT ports @> '[{"port":13337}]'::jsonb;

INSERT INTO container_images (name, reference, description, gpu_capable)
VALUES ('Jenderal Cloud OS Desktop Browser', 'jenderal/desktop-browser:1.0',
        'Firefox ESR in a lightweight X desktop, streamed to the browser with noVNC', false)
ON CONFLICT (reference) DO NOTHING;

INSERT INTO workspace_templates (key, name, description, category, image_id, runtime_versions,
  min_cpu_millis, min_memory_bytes, rec_cpu_millis, rec_memory_bytes, default_storage_bytes, ports, gpu_mode, security, env, command)
SELECT 'browser', 'Web Browser',
       'A full Firefox browser running on the server and shown in a window — browse any website from Jenderal Cloud OS.',
       'desktop', i.id, '{"firefox":"esr"}'::jsonb,
       500, 536870912, 1000, 1073741824, 2147483648,
       '[{"port":6080,"name":"desktop"}]'::jsonb, 'none',
       '{"run_as_uid":1000,"run_as_gid":1000,"no_new_privileges":true,"read_only_rootfs":false,"cap_add":[]}'::jsonb,
       '{}'::jsonb, '[]'::jsonb
  FROM container_images i
 WHERE i.reference = 'jenderal/desktop-browser:1.0'
ON CONFLICT (key) DO NOTHING;

-- +goose Down
DELETE FROM workspace_templates WHERE key = 'browser'
   AND NOT EXISTS (SELECT 1 FROM workspaces w JOIN workspace_templates t ON t.id = w.template_id WHERE t.key = 'browser');
DELETE FROM container_images WHERE reference = 'jenderal/desktop-browser:1.0'
   AND NOT EXISTS (SELECT 1 FROM workspace_templates t WHERE t.image_id = container_images.id);
UPDATE workspace_templates
   SET ports = (SELECT coalesce(jsonb_agg(p), '[]'::jsonb) FROM jsonb_array_elements(ports) p WHERE (p->>'port')::int <> 13337),
       updated_at = now()
 WHERE ports @> '[{"port":13337}]'::jsonb;
