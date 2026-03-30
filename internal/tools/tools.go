package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"

	"github.com/SantiagoBedoya/gym-bot/internal/db"
)

type Dispatcher struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

func NewDispatcher(pool *pgxpool.Pool) *Dispatcher {
	return &Dispatcher{
		pool:    pool,
		queries: db.New(pool),
	}
}

func (d *Dispatcher) Definitions() []responses.ToolUnionParam {
	return []responses.ToolUnionParam{
		{OfFunction: &responses.FunctionToolParam{
			Name:        "save_routine",
			Description: openai.String("Guarda o reemplaza una rutina de entrenamiento con sus ejercicios, series y rangos de repeticiones."),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"routine_name": map[string]any{
						"type":        "string",
						"description": "Nombre de la rutina, ej: 'upper', 'lower', 'push', 'pull'",
					},
					"exercises": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"name":     map[string]any{"type": "string"},
								"position": map[string]any{"type": "integer", "description": "Orden del ejercicio en la rutina"},
								"sets":     map[string]any{"type": "integer"},
								"reps_min": map[string]any{"type": "integer"},
								"reps_max": map[string]any{"type": "integer"},
								"notes":    map[string]any{"type": "string"},
							},
							"required": []string{"name", "position", "sets", "reps_min", "reps_max"},
						},
					},
				},
				"required": []string{"routine_name", "exercises"},
			},
		}},
		{OfFunction: &responses.FunctionToolParam{
			Name:        "list_routines",
			Description: openai.String("Lista los nombres de todas las rutinas guardadas por el usuario."),
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		}},
		{OfFunction: &responses.FunctionToolParam{
			Name:        "get_routine",
			Description: openai.String("Obtiene la lista de ejercicios de una rutina con sus series y rangos de repeticiones objetivo."),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"routine_name": map[string]any{"type": "string"},
				},
				"required": []string{"routine_name"},
			},
		}},
		{OfFunction: &responses.FunctionToolParam{
			Name:        "get_exercise_history",
			Description: openai.String("Obtiene el historial de las últimas sesiones de un ejercicio específico para calcular la sobrecarga progresiva."),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"exercise_name": map[string]any{"type": "string"},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Número de sesiones pasadas a retornar (default 5)",
					},
				},
				"required": []string{"exercise_name"},
			},
		}},
		{OfFunction: &responses.FunctionToolParam{
			Name:        "log_session_sets",
			Description: openai.String("Registra los sets realizados en una sesión de entrenamiento."),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"routine_name": map[string]any{"type": "string"},
					"sets": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"exercise_name": map[string]any{"type": "string"},
								"set_number":    map[string]any{"type": "integer"},
								"reps":          map[string]any{"type": "integer"},
								"weight_kg":     map[string]any{"type": "number"},
								"rpe": map[string]any{
									"type":        "integer",
									"description": "Esfuerzo percibido 1-10 (opcional)",
								},
							},
							"required": []string{"exercise_name", "set_number", "reps", "weight_kg"},
						},
					},
				},
				"required": []string{"routine_name", "sets"},
			},
		}},
		{OfFunction: &responses.FunctionToolParam{
			Name:        "get_last_session_summary",
			Description: openai.String("Obtiene el resumen de la última sesión de entrenamiento para una rutina."),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"routine_name": map[string]any{"type": "string"},
				},
				"required": []string{"routine_name"},
			},
		}},
	}
}

func (d *Dispatcher) Execute(ctx context.Context, name, argsJSON string) (string, error) {
	switch name {
	case "list_routines":
		return d.listRoutines(ctx)
	case "save_routine":
		return d.saveRoutine(ctx, argsJSON)
	case "get_routine":
		return d.getRoutine(ctx, argsJSON)
	case "get_exercise_history":
		return d.getExerciseHistory(ctx, argsJSON)
	case "log_session_sets":
		return d.logSessionSets(ctx, argsJSON)
	case "get_last_session_summary":
		return d.getLastSessionSummary(ctx, argsJSON)
	}
	return "", fmt.Errorf("unknown tool: %s", name)
}

func (d *Dispatcher) listRoutines(ctx context.Context) (string, error) {
	names, err := d.queries.ListRoutines(ctx)
	if err != nil {
		return "", err
	}
	result, _ := json.Marshal(map[string]any{"routines": names})
	return string(result), nil
}

func (d *Dispatcher) saveRoutine(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		RoutineName string `json:"routine_name"`
		Exercises   []struct {
			Name     string `json:"name"`
			Position int32  `json:"position"`
			Sets     int32  `json:"sets"`
			RepsMin  int32  `json:"reps_min"`
			RepsMax  int32  `json:"reps_max"`
			Notes    string `json:"notes"`
		} `json:"exercises"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	qtx := db.New(tx)

	routine, err := qtx.UpsertRoutine(ctx, args.RoutineName)
	if err != nil {
		return "", err
	}
	if err := qtx.DeleteExercisesByRoutine(ctx, routine.ID); err != nil {
		return "", err
	}
	for _, ex := range args.Exercises {
		_, err := qtx.InsertExercise(ctx, db.InsertExerciseParams{
			RoutineID: routine.ID,
			Name:      ex.Name,
			Position:  ex.Position,
			Sets:      ex.Sets,
			RepsMin:   ex.RepsMin,
			RepsMax:   ex.RepsMax,
			Notes:     ex.Notes,
		})
		if err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}

	result, _ := json.Marshal(map[string]any{
		"status":          "ok",
		"routine":         args.RoutineName,
		"exercises_saved": len(args.Exercises),
	})
	return string(result), nil
}

func (d *Dispatcher) getRoutine(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		RoutineName string `json:"routine_name"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}

	exercises, err := d.queries.GetExercisesByRoutine(ctx, args.RoutineName)
	if err != nil {
		return "", err
	}
	if len(exercises) == 0 {
		return fmt.Sprintf(`{"error": "routine '%s' not found or has no exercises"}`, args.RoutineName), nil
	}

	result, _ := json.Marshal(exercises)
	return string(result), nil
}

func (d *Dispatcher) getExerciseHistory(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ExerciseName string `json:"exercise_name"`
		Limit        int32  `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	if args.Limit <= 0 {
		args.Limit = 5
	}

	rows, err := d.queries.GetExerciseHistory(ctx, db.GetExerciseHistoryParams{
		ExerciseName: args.ExerciseName,
		Limit:        args.Limit * 10, // fetch more rows to cover multiple sets per session
	})
	if err != nil {
		return "", err
	}

	type setEntry struct {
		SetNumber   int32   `json:"set_number"`
		Reps        int32   `json:"reps"`
		WeightKg    float64 `json:"weight_kg"`
		RPE         *int32  `json:"rpe,omitempty"`
		SessionDate string  `json:"session_date"`
	}

	entries := make([]setEntry, 0, len(rows))
	for _, r := range rows {
		e := setEntry{
			SetNumber:   r.SetNumber,
			Reps:        r.Reps,
			WeightKg:    r.WeightKg,
			SessionDate: r.SessionDate.Format("2006-01-02"),
		}
		if r.Rpe.Valid {
			e.RPE = &r.Rpe.Int32
		}
		entries = append(entries, e)
	}

	result, _ := json.Marshal(map[string]any{
		"exercise": args.ExerciseName,
		"history":  entries,
	})
	return string(result), nil
}

func (d *Dispatcher) logSessionSets(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		RoutineName string `json:"routine_name"`
		Sets        []struct {
			ExerciseName string  `json:"exercise_name"`
			SetNumber    int32   `json:"set_number"`
			Reps         int32   `json:"reps"`
			WeightKg     float64 `json:"weight_kg"`
			RPE          *int32  `json:"rpe"`
		} `json:"sets"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	qtx := db.New(tx)

	session, err := qtx.GetTodaySession(ctx, args.RoutineName)
	if err != nil {
		session, err = qtx.CreateSession(ctx, args.RoutineName)
		if err != nil {
			return "", err
		}
	}

	for _, s := range args.Sets {
		rpe := pgtype.Int4{}
		if s.RPE != nil {
			rpe = pgtype.Int4{Int32: *s.RPE, Valid: true}
		}
		_, err := qtx.InsertSessionSet(ctx, db.InsertSessionSetParams{
			SessionID:    session.ID,
			ExerciseName: s.ExerciseName,
			SetNumber:    s.SetNumber,
			Reps:         s.Reps,
			WeightKg:     s.WeightKg,
			Rpe:          rpe,
		})
		if err != nil {
			return "", err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}

	result, _ := json.Marshal(map[string]any{
		"status":     "ok",
		"session_id": session.ID,
		"sets_saved": len(args.Sets),
		"date":       session.StartedAt.Format("2006-01-02 15:04"),
	})
	return string(result), nil
}

func (d *Dispatcher) getLastSessionSummary(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		RoutineName string `json:"routine_name"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}

	session, err := d.queries.GetLastSessionByRoutine(ctx, args.RoutineName)
	if err != nil {
		return fmt.Sprintf(`{"error": "no sessions found for routine '%s'"}`, args.RoutineName), nil
	}

	sets, err := d.queries.GetSessionSetsBySession(ctx, session.ID)
	if err != nil {
		return "", err
	}

	type setEntry struct {
		ExerciseName string  `json:"exercise_name"`
		SetNumber    int32   `json:"set_number"`
		Reps         int32   `json:"reps"`
		WeightKg     float64 `json:"weight_kg"`
		RPE          *int32  `json:"rpe,omitempty"`
	}

	entries := make([]setEntry, 0, len(sets))
	for _, s := range sets {
		e := setEntry{
			ExerciseName: s.ExerciseName,
			SetNumber:    s.SetNumber,
			Reps:         s.Reps,
			WeightKg:     s.WeightKg,
		}
		if s.Rpe.Valid {
			e.RPE = &s.Rpe.Int32
		}
		entries = append(entries, e)
	}

	result, _ := json.Marshal(map[string]any{
		"routine":  args.RoutineName,
		"date":     session.StartedAt.Format("2006-01-02"),
		"sessions": entries,
	})
	return string(result), nil
}
