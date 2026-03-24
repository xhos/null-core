-- +goose Up
ALTER TABLE receipts ADD COLUMN image_taken_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE receipts DROP COLUMN image_taken_at;
