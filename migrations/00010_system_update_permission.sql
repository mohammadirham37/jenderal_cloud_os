-- +goose Up
-- Platform self-update (Administration → System update): a dedicated
-- permission for installing new releases, granted to super administrators.
INSERT INTO permissions (key, category, description) VALUES
  ('system.update', 'host', 'Install platform updates (new Jenderal Cloud OS releases)')
ON CONFLICT (key) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, 'system.update' FROM roles r WHERE r.key = 'super_admin'
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM role_permissions WHERE permission_key = 'system.update';
DELETE FROM permissions WHERE key = 'system.update';
