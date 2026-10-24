-- name: CreateSession :exec
INSERT INTO sessions (token_hash, created_at, expires_at, last_seen_at, user_agent, remote_addr)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetSession :one
SELECT * FROM sessions WHERE token_hash = ? AND expires_at > ?;

-- name: RenewSession :exec
UPDATE sessions SET expires_at = ?, last_seen_at = ? WHERE token_hash = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = ?;

-- name: DeleteAllSessions :exec
DELETE FROM sessions;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < ?;
