-- +goose Up
ALTER TABLE receipts ADD COLUMN image_hash TEXT;
CREATE UNIQUE INDEX receipts_user_image_hash_idx ON receipts (user_id, image_hash) WHERE image_hash IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS receipts_user_image_hash_idx;
ALTER TABLE receipts DROP COLUMN image_hash;
