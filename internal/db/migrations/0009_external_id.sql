-- +goose Up
ALTER TABLE transactions RENAME COLUMN email_id TO external_id;

DROP INDEX IF EXISTS ux_transactions_email_id_notnull;

CREATE UNIQUE INDEX ux_transactions_account_external_id_notnull
  ON transactions(account_id, external_id) WHERE external_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS ux_transactions_account_external_id_notnull;

CREATE UNIQUE INDEX ux_transactions_email_id_notnull
  ON transactions(external_id) WHERE external_id IS NOT NULL;

ALTER TABLE transactions RENAME COLUMN external_id TO email_id;
