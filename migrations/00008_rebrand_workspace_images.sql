-- +goose Up
-- Locally built workspace images are now tagged jenderal/<name> instead of
-- itk-cloud/<name>; the upgrade script re-tags images that already exist.
UPDATE container_images
   SET reference = 'jenderal/' || substr(reference, 11), updated_at = now()
 WHERE reference LIKE 'itk-cloud/%';

-- Home directory is now /home/jenderal (user jenderal).
UPDATE workspace_templates
   SET env = replace(env::text, '/home/itk', '/home/jenderal')::jsonb, updated_at = now()
 WHERE env::text LIKE '%/home/itk%';

-- +goose Down
UPDATE workspace_templates
   SET env = replace(env::text, '/home/jenderal', '/home/itk')::jsonb, updated_at = now()
 WHERE env::text LIKE '%/home/jenderal%';
UPDATE container_images
   SET reference = 'itk-cloud/' || substr(reference, 10), updated_at = now()
 WHERE reference LIKE 'jenderal/%';
