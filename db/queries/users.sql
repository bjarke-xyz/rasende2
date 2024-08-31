-- name: GetUser :one
SELECT * FROM users
WHERE id = ? LIMIT 1;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = ? LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (email, password_hash) VALUES (?, ?) RETURNING *;

-- name: SetUserEmailConfirmed :exec
UPDATE users SET email_confirmed = 1, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND email_confirmed = 0;

-- name: GetLinkByCode :one
SELECT * FROM magic_links
WHERE link_code = ? LIMIT 1;

-- name: GetLinksByUserId :many
SELECT * FROM magic_links
WHERE user_id = ? AND expires_at >= CURRENT_TIMESTAMP;

-- name: CreateMagicLink :exec
INSERT INTO magic_links (user_id, otp_hash, link_code, expires_at) VALUES (?, ?, ?, ?);

-- name: DeleteMagicLink :exec
DELETE FROM magic_links 
WHERE id = ?;

-- name: DeleteExpiredMagicLinks :exec
DELETE FROM magic_links WHERE expires_at < CURRENT_TIMESTAMP;