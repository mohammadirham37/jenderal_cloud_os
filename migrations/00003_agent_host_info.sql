-- +goose Up
ALTER TABLE agent_nodes ADD COLUMN host_info jsonb NOT NULL DEFAULT '{}';
INSERT INTO platform_settings (key, value) VALUES
  ('scheduler.cpu_overcommit_pct', '200'),
  ('gpu.release_after_stopped_minutes', '60')
ON CONFLICT (key) DO NOTHING;

-- +goose Down
DELETE FROM platform_settings WHERE key IN ('scheduler.cpu_overcommit_pct', 'gpu.release_after_stopped_minutes');
ALTER TABLE agent_nodes DROP COLUMN host_info;
