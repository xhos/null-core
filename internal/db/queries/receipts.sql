-- name: CreateReceipt :one
INSERT INTO receipts (
  user_id,
  image_path,
  image_hash,
  status
)
VALUES (
  sqlc.arg(user_id)::uuid,
  sqlc.arg(image_path)::text,
  sqlc.arg(image_hash)::text,
  sqlc.arg(status)::smallint
)
RETURNING *;

-- name: GetReceiptByImageHash :one
SELECT *
FROM receipts
WHERE user_id = sqlc.arg(user_id)::uuid
  AND image_hash = sqlc.arg(image_hash)::text;

-- name: GetReceipt :one
SELECT *
FROM receipts
WHERE id = sqlc.arg(id)::bigint
  AND user_id = sqlc.arg(user_id)::uuid;

-- name: ListReceipts :many
SELECT
  r.*,
  count(*) OVER() AS total_count
FROM receipts r
WHERE r.user_id = sqlc.arg(user_id)::uuid
  AND (
    sqlc.narg('status')::smallint IS NULL
    OR r.status = sqlc.narg('status')::smallint
  )
  AND (
    sqlc.narg('unlinked_only')::boolean IS NULL
    OR (sqlc.narg('unlinked_only')::boolean = true AND r.transaction_id IS NULL)
  )
  AND (
    sqlc.narg('start_date')::date IS NULL
    OR r.receipt_date >= sqlc.narg('start_date')::date
  )
  AND (
    sqlc.narg('end_date')::date IS NULL
    OR r.receipt_date <= sqlc.narg('end_date')::date
  )
ORDER BY r.created_at DESC, r.id DESC
LIMIT COALESCE(sqlc.narg('lim')::int, 50)
OFFSET COALESCE(sqlc.narg('off')::int, 0);

-- name: UpdateReceipt :one
UPDATE receipts
SET
  transaction_id = coalesce(sqlc.narg('transaction_id')::bigint, transaction_id),
  merchant       = coalesce(sqlc.narg('merchant')::text, merchant),
  receipt_date   = coalesce(sqlc.narg('receipt_date')::date, receipt_date),
  currency       = coalesce(sqlc.narg('currency')::char(3), currency),
  subtotal_cents = coalesce(sqlc.narg('subtotal_cents')::bigint, subtotal_cents),
  tax_cents      = coalesce(sqlc.narg('tax_cents')::bigint, tax_cents),
  total_cents    = coalesce(sqlc.narg('total_cents')::bigint, total_cents),
  confidence     = coalesce(sqlc.narg('confidence')::real, confidence),
  status         = coalesce(sqlc.narg('status')::smallint, status)
WHERE id = sqlc.arg(id)::bigint
  AND user_id = sqlc.arg(user_id)::uuid
RETURNING *;

-- name: DeleteReceipt :exec
DELETE FROM receipts
WHERE id = sqlc.arg(id)::bigint
  AND user_id = sqlc.arg(user_id)::uuid;

-- name: GetPendingReceipts :many
SELECT *
FROM receipts
WHERE status = 1
ORDER BY created_at ASC
LIMIT 20;

-- name: CreateReceiptItem :one
INSERT INTO receipt_items (
  receipt_id,
  raw_name,
  name,
  quantity,
  unit_price_cents,
  unit_currency,
  sort_order
)
VALUES (
  sqlc.arg(receipt_id)::bigint,
  sqlc.arg(raw_name)::text,
  sqlc.narg('name')::text,
  sqlc.arg(quantity)::double precision,
  sqlc.arg(unit_price_cents)::bigint,
  sqlc.arg(unit_currency)::char(3),
  sqlc.arg(sort_order)::int
)
RETURNING *;

-- name: ListReceiptItems :many
SELECT *
FROM receipt_items
WHERE receipt_id = sqlc.arg(receipt_id)::bigint
ORDER BY sort_order ASC, id ASC;

-- name: DeleteReceiptItemsByReceipt :exec
DELETE FROM receipt_items
WHERE receipt_id = sqlc.arg(receipt_id)::bigint;

-- name: FindReceiptLinkCandidates :many
SELECT
  t.id,
  t.account_id,
  t.tx_date,
  t.tx_amount_cents,
  t.tx_currency,
  t.merchant,
  COALESCE(a.friendly_name, a.name) AS account_display_name,
  CASE
    WHEN sqlc.narg('receipt_date')::date IS NOT NULL
    THEN ABS(t.tx_date::date - sqlc.narg('receipt_date')::date)
    ELSE NULL
  END AS date_diff_days
FROM transactions t
JOIN accounts a ON t.account_id = a.id
LEFT JOIN account_users au ON a.id = au.account_id AND au.user_id = sqlc.arg(user_id)::uuid
WHERE (a.owner_id = sqlc.arg(user_id)::uuid OR au.user_id IS NOT NULL)
  AND t.tx_amount_cents BETWEEN sqlc.arg(amount_cents)::bigint - 50 AND sqlc.arg(amount_cents)::bigint + 50
  AND t.tx_currency = sqlc.arg(currency)::char(3)
  AND t.tx_direction = 2::smallint
  AND NOT EXISTS (
    SELECT 1 FROM receipts r
    WHERE r.transaction_id = t.id
  )
ORDER BY
  CASE
    WHEN sqlc.narg('receipt_date')::date IS NOT NULL
    THEN ABS(t.tx_date::date - sqlc.narg('receipt_date')::date)
    ELSE 0
  END ASC,
  t.tx_date DESC
LIMIT 10;
