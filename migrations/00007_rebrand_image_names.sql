-- +goose Up
-- Rebrand the seeded workspace image names: "ITK Ubuntu Dev" -> "Jenderal Cloud OS Ubuntu Dev".
UPDATE container_images
   SET name = 'Jenderal Cloud OS ' || substr(name, 5), updated_at = now()
 WHERE name LIKE 'ITK %';

-- +goose Down
UPDATE container_images
   SET name = 'ITK ' || substr(name, 19), updated_at = now()
 WHERE name LIKE 'Jenderal Cloud OS %';
