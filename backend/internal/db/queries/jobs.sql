-- name: InsertJob :exec
INSERT INTO jobs (id, type, status, spec, conflict_default, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: UpdateJob :exec
UPDATE jobs SET status = ?, conflict_default = ?, bytes_total = ?, bytes_done = ?, items_total = ?,
  items_done = ?, current_item = ?, pending_conflict = ?, temp_place_id = ?, temp_path = ?, result = ?,
  error_code = ?, error_message = ?, started_at = ?, finished_at = ?
WHERE id = ?;

-- name: SetJobTemp :exec
UPDATE jobs SET temp_place_id = ?, temp_path = ? WHERE id = ?;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;

-- name: ListJobs :many
SELECT * FROM jobs ORDER BY created_at DESC;

-- name: ListUnfinishedJobs :many
SELECT * FROM jobs WHERE status IN ('queued', 'running', 'awaiting_input');

-- name: DeleteJob :exec
DELETE FROM jobs WHERE id = ?;

-- name: ListOldFinishedJobIDs :many
SELECT id FROM jobs
WHERE status IN ('completed', 'failed', 'cancelled', 'interrupted') AND finished_at < ?;
