CREATE TABLE IF NOT EXISTS exercises (
    id         SERIAL PRIMARY KEY,
    routine_id INTEGER NOT NULL REFERENCES routines(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    position   INTEGER NOT NULL,
    sets       INTEGER NOT NULL,
    reps_min   INTEGER NOT NULL,
    reps_max   INTEGER NOT NULL,
    notes      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
