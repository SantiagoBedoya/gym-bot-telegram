-- name: GetBotState :one
SELECT value FROM bot_state WHERE key = $1;

-- name: UpsertBotState :exec
INSERT INTO bot_state (key, value)
VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
