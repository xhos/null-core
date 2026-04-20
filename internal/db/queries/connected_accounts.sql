-- name: ListActiveConnectedAccounts :many
select *
from connected_accounts
where status = 'active'
order by id;

-- name: GetConnectedAccount :one
select *
from connected_accounts
where id = @id::bigint;

-- name: UpdateConnectedAccountCursor :exec
update connected_accounts
set sync_cursor = sqlc.narg('sync_cursor')::timestamptz,
    status      = coalesce(sqlc.narg('status')::text, status)
where id = @id::bigint;

-- name: CreateConnectedAccount :one
insert into connected_accounts (user_id, provider, credentials)
values (@user_id::uuid, @provider::text, @credentials::bytea)
returning *;

-- name: DeleteConnectedAccount :execrows
delete from connected_accounts
where id = @id::bigint and user_id = @user_id::uuid;
