-- +goose Up
-- Product renamed to "Jenderal Cloud OS" (only if the administrator kept the default name).
UPDATE platform_settings SET value = '"Jenderal Cloud OS"', updated_at = now()
WHERE key = 'platform.name' AND value = '"ITK Cloud"';
UPDATE workspace_templates SET description = 'JupyterLab served through the authenticated Jenderal Cloud OS proxy.', updated_at = now()
WHERE key = 'jupyterlab' AND description = 'JupyterLab served through the authenticated ITK Cloud proxy.';

-- +goose Down
UPDATE platform_settings SET value = '"ITK Cloud"' WHERE key = 'platform.name' AND value = '"Jenderal Cloud OS"';
