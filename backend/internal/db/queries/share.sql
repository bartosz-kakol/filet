-- name: InsertShareLink :exec
INSERT INTO share_links (id, place_id, path, is_dir, password_hash, password_salt, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetShareLink :one
SELECT * FROM share_links WHERE id = ?;

-- name: ListShareLinks :many
SELECT * FROM share_links WHERE expires_at IS NULL OR expires_at > ? ORDER BY created_at DESC;

-- name: DeleteShareLink :execrows
DELETE FROM share_links WHERE id = ?;

-- name: TouchShareLink :exec
UPDATE share_links SET access_count = access_count + 1, last_accessed_at = ? WHERE id = ?;

-- name: DeleteExpiredShareLinks :execrows
DELETE FROM share_links WHERE expires_at IS NOT NULL AND expires_at < ?;
