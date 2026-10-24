-- name: InsertUploadSession :exec
INSERT INTO upload_sessions (id, place_id, dir, name, target_path, partial_name, size, chunk_size,
  chunk_count, received_count, hash_list_digest, client_mtime, conflict_policy, created_at, last_activity_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?);

-- name: GetUploadSession :one
SELECT * FROM upload_sessions WHERE id = ?;

-- name: GetUploadSessionByTarget :one
SELECT * FROM upload_sessions WHERE place_id = ? AND target_path = ?;

-- name: ListUploadSessions :many
SELECT * FROM upload_sessions ORDER BY created_at;

-- name: ListUploadSessionsInPlace :many
SELECT * FROM upload_sessions WHERE place_id = ?;

-- name: ListUploadSessionsInDir :many
SELECT * FROM upload_sessions WHERE place_id = ? AND dir = ? ORDER BY name;

-- name: ListLeftovers :many
SELECT * FROM upload_sessions WHERE last_activity_at < ? ORDER BY last_activity_at DESC;

-- name: CountLeftovers :one
SELECT COUNT(*) FROM upload_sessions WHERE last_activity_at < ?;

-- name: DeleteUploadSession :execrows
DELETE FROM upload_sessions WHERE id = ?;

-- name: TouchUploadSession :exec
UPDATE upload_sessions SET last_activity_at = ? WHERE id = ?;

-- name: IncrementReceivedCount :one
UPDATE upload_sessions SET received_count = received_count + 1, last_activity_at = ?
WHERE id = ? RETURNING received_count;

-- name: ResetUploadReceived :exec
UPDATE upload_sessions SET received_count = 0, last_activity_at = ? WHERE id = ?;

-- name: ListChunks :many
SELECT idx, hash, received, failures FROM upload_chunks WHERE upload_id = ? ORDER BY idx;

-- name: ListReceivedFlags :many
SELECT idx, received FROM upload_chunks WHERE upload_id = ? ORDER BY idx;

-- name: MarkChunkReceived :execrows
UPDATE upload_chunks SET received = 1 WHERE upload_id = ? AND idx = ? AND received = 0;

-- name: IncrementChunkFailures :one
UPDATE upload_chunks SET failures = failures + 1 WHERE upload_id = ? AND idx = ? RETURNING failures;

-- name: ResetChunks :exec
UPDATE upload_chunks SET received = 0 WHERE upload_id = ?;
