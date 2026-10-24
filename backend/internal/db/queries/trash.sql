-- name: InsertTrashItem :exec
INSERT INTO trash_items (id, place_id, original_path, name, is_dir, size, deleted_at, origin_unknown)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTrashItem :one
SELECT * FROM trash_items WHERE id = ?;

-- name: ListTrashItems :many
SELECT * FROM trash_items WHERE place_id = ? ORDER BY deleted_at DESC;

-- name: DeleteTrashItem :exec
DELETE FROM trash_items WHERE id = ?;

-- name: UpdateTrashSize :exec
UPDATE trash_items SET size = ? WHERE id = ?;
