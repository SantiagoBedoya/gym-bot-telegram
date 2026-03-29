CREATE TABLE IF NOT EXISTS sessions (
    id           SERIAL PRIMARY KEY,
    routine_name TEXT NOT NULL,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS session_sets (
    id            SERIAL PRIMARY KEY,
    session_id    INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    exercise_name TEXT NOT NULL,
    set_number    INTEGER NOT NULL,
    reps          INTEGER NOT NULL,
    weight_kg     DOUBLE PRECISION NOT NULL,
    rpe           INTEGER,
    logged_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
