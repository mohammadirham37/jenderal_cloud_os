-- +goose Up
-- JupyterLab must run its server process (the agent defaults to
-- `sleep infinity` when a template has no command).
UPDATE workspace_templates SET command = '["/usr/local/bin/start-jupyter"]', updated_at = now()
WHERE key = 'jupyterlab' AND command = '[]';

-- +goose Down
UPDATE workspace_templates SET command = '[]' WHERE key = 'jupyterlab' AND command = '["/usr/local/bin/start-jupyter"]';
