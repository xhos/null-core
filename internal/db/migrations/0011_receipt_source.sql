-- +goose Up
ALTER TABLE receipts
  ALTER COLUMN image_path DROP NOT NULL,
  ADD COLUMN source TEXT NOT NULL DEFAULT 'ocr' CHECK (source IN ('ocr', 'manual'));

-- +goose Down
ALTER TABLE receipts
  DROP COLUMN source,
  ALTER COLUMN image_path SET NOT NULL;
