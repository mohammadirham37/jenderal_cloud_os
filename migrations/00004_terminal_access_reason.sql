-- +goose Up
ALTER TABLE terminal_sessions ADD COLUMN access_reason text;
ALTER TABLE terminal_sessions ADD COLUMN cols integer NOT NULL DEFAULT 120;
ALTER TABLE terminal_sessions ADD COLUMN rows integer NOT NULL DEFAULT 32;

-- +goose Down
ALTER TABLE terminal_sessions DROP COLUMN rows;
ALTER TABLE terminal_sessions DROP COLUMN cols;
ALTER TABLE terminal_sessions DROP COLUMN access_reason;
