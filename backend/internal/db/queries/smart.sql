-- name: UpsertSmartSnapshot :exec
INSERT INTO smart_snapshots (device, polled_at, status, data) VALUES (?, ?, ?, ?)
ON CONFLICT (device) DO UPDATE SET polled_at = excluded.polled_at, status = excluded.status, data = excluded.data;

-- name: GetSmartSnapshot :one
SELECT * FROM smart_snapshots WHERE device = ?;

-- name: ListSmartSnapshots :many
SELECT * FROM smart_snapshots;

-- name: DeleteSmartSnapshot :exec
DELETE FROM smart_snapshots WHERE device = ?;
