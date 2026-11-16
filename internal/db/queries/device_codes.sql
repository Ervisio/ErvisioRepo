-- name: InsertDeviceCode :exec
INSERT INTO oauth_device_codes (id, device_code_hash, user_code, client_id, scopes, device_name, request_ip,
                                interval_seconds, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetDeviceCodeByHashForUpdate :one
SELECT * FROM oauth_device_codes WHERE device_code_hash = $1 FOR UPDATE;

-- name: GetPendingDeviceCode :one
SELECT * FROM oauth_device_codes
WHERE user_code = $1 AND status = 'pending' AND expires_at > now();

-- name: GetPendingDeviceCodeForUpdate :one
SELECT * FROM oauth_device_codes
WHERE user_code = $1 AND status = 'pending' AND expires_at > now()
FOR UPDATE;

-- name: SetDeviceCodePoll :exec
UPDATE oauth_device_codes SET last_poll_at = sqlc.arg(polled_at), interval_seconds = sqlc.arg(interval_seconds)
WHERE id = $1;

-- name: DecideDeviceCode :exec
UPDATE oauth_device_codes SET status = sqlc.arg(status), user_id = sqlc.arg(user_id) WHERE id = $1;

-- name: ConsumeDeviceCode :exec
UPDATE oauth_device_codes SET status = 'consumed', token_id = sqlc.arg(token_id) WHERE id = $1;

-- name: DeleteExpiredDeviceCodes :execrows
-- Keeps expired rows for an hour, so that a late poll still gets
-- expired_token instead of an unknown code.
DELETE FROM oauth_device_codes WHERE expires_at < now() - interval '1 hour';
