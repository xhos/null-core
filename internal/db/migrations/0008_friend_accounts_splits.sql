-- +goose Up
ALTER TABLE accounts DROP CONSTRAINT accounts_account_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_account_type_check CHECK (account_type BETWEEN 0 AND 6);

ALTER TABLE transactions ADD COLUMN split_from_id BIGINT REFERENCES transactions(id) ON DELETE CASCADE;
CREATE INDEX idx_tx_split_from ON transactions(split_from_id) WHERE split_from_id IS NOT NULL;

ALTER TABLE transactions ADD COLUMN forgiven BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE transactions DROP COLUMN IF EXISTS forgiven;
DROP INDEX IF EXISTS idx_tx_split_from;
ALTER TABLE transactions DROP COLUMN IF EXISTS split_from_id;

ALTER TABLE accounts DROP CONSTRAINT accounts_account_type_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_account_type_check CHECK (account_type BETWEEN 0 AND 5);
