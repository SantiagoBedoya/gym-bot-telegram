-- name: UpsertRoutine :one
INSERT INTO routines (name)
VALUES ($1)
ON CONFLICT (name) DO UPDATE SET updated_at = NOW()
RETURNING *;

-- name: GetRoutineByName :one
SELECT * FROM routines WHERE name = $1;

-- name: DeleteExercisesByRoutine :exec
DELETE FROM exercises WHERE routine_id = $1;

-- name: InsertExercise :one
INSERT INTO exercises (routine_id, name, position, sets, reps_min, reps_max, notes)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetExercisesByRoutine :many
SELECT e.*
FROM exercises e
JOIN routines r ON r.id = e.routine_id
WHERE r.name = $1
ORDER BY e.position;
