-- name: CreateSession :one
INSERT INTO sessions (routine_name)
VALUES ($1)
RETURNING *;

-- name: InsertSessionSet :one
INSERT INTO session_sets (session_id, exercise_name, set_number, reps, weight_kg, rpe)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetLastSessionByRoutine :one
SELECT * FROM sessions
WHERE routine_name = $1
ORDER BY started_at DESC
LIMIT 1;

-- name: GetSessionSetsBySession :many
SELECT * FROM session_sets
WHERE session_id = $1
ORDER BY exercise_name, set_number;

-- name: GetTodaySession :one
SELECT id, routine_name, started_at FROM sessions
WHERE routine_name = $1
AND DATE(started_at) = CURRENT_DATE
ORDER BY started_at DESC
LIMIT 1;

-- name: GetExerciseHistory :many
SELECT
    ss.id,
    ss.exercise_name,
    ss.set_number,
    ss.reps,
    ss.weight_kg,
    ss.rpe,
    ss.logged_at,
    s.started_at AS session_date
FROM session_sets ss
JOIN sessions s ON s.id = ss.session_id
WHERE ss.exercise_name ILIKE $1
ORDER BY ss.logged_at DESC
LIMIT $2;
