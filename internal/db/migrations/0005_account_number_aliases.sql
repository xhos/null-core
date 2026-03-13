-- +goose Up
ALTER TABLE accounts RENAME COLUMN alias TO friendly_name;
ALTER TABLE accounts ADD COLUMN aliases TEXT[] NOT NULL DEFAULT '{}';
CREATE INDEX idx_accounts_aliases ON accounts USING GIN (aliases);

-- +goose Down
DROP INDEX IF EXISTS idx_accounts_aliases;
ALTER TABLE accounts DROP COLUMN IF EXISTS aliases;
ALTER TABLE accounts RENAME COLUMN friendly_name TO alias;
