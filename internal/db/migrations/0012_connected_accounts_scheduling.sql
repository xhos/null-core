-- +goose Up
ALTER TABLE connected_accounts
  ADD COLUMN sync_interval_minutes INT,
  ADD COLUMN next_run_at TIMESTAMPTZ DEFAULT NOW();

CREATE INDEX idx_connected_accounts_due
  ON connected_accounts(next_run_at) WHERE status = 'active' AND next_run_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_connected_accounts_due;
ALTER TABLE connected_accounts
  DROP COLUMN next_run_at,
  DROP COLUMN sync_interval_minutes;
