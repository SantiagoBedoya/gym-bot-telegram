package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"

	"github.com/SantiagoBedoya/gym-bot/internal/db"
	"github.com/SantiagoBedoya/gym-bot/internal/tools"
)

const botStateKey = "previous_response_id"
const botStateDateKey = "previous_response_date"

const systemPrompt = `Eres un entrenador personal de fuerza integrado en un bot de Telegram.
Respondes en el mismo idioma que use el usuario (español o inglés).

Responsabilidades:
1. GUARDAR RUTINAS: Cuando el usuario describe una rutina (ej: "mi upper es press banca 4x6-12, remo 4x6-10"),
   extrae todos los ejercicios con sets y rango de reps, luego llama save_routine. Confirma lo que guardaste.

2. GUIAR ENTRENAMIENTO: Cuando dice "hoy entreno X" o similar:
   - Llama get_routine para obtener la lista de ejercicios.
   - Para cada ejercicio, llama get_exercise_history para ver el historial reciente.
   - Aplica sobrecarga progresiva inteligente según el historial:
     * Doble progresión: si en la última sesión se completaron todas las reps del rango máximo → sube el peso la próxima sesión
     * Progresión de reps: si no se llegó al rango máximo → mantén el peso, aumenta reps
     * Deload: si el RPE fue muy alto o las reps bajaron → mantén o reduce ligeramente el peso
   - Presenta el plan completo: orden de ejercicios, series de calentamiento (40%, 60%, 80% del peso efectivo),
     peso efectivo objetivo, series × reps objetivo.

3. REGISTRAR SESIÓN: Cuando el usuario reporta lo que hizo (ej: "hice 4x8 con 80kg en press"),
   extrae ejercicio, sets, reps y peso, luego llama log_session_sets.
   Si no está claro a qué rutina pertenece, pregunta.

4. RESPONDER PREGUNTAS: Responde preguntas sobre progreso, historial o principios de entrenamiento.
   Usa las tools para obtener datos reales antes de responder.

Reglas importantes:
- Nunca inventes pesos o datos de historial. Siempre usa las tools para obtener información real.
- Respuestas concisas y prácticas, como lo haría un entrenador real.
- Usa kg como unidad de peso.
- Si una rutina no existe, pide al usuario que la defina primero.
- Formato de respuesta: usa exclusivamente HTML de Telegram. Etiquetas permitidas: <b>negrita</b>, <i>cursiva</i>, <code>código</code>, <pre>bloque de código</pre>. NO uses markdown estándar (**, *, #, -, etc.). Para listas usa guiones simples sin formato o saltos de línea.`

type OpenAIService struct {
	client             openai.Client
	queries            *db.Queries
	dispatcher         *tools.Dispatcher
	model              string
	previousResponseID string
}

func NewOpenAIService(apiKey, model string, queries *db.Queries, dispatcher *tools.Dispatcher) *OpenAIService {
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAIService{
		client:     client,
		queries:    queries,
		dispatcher: dispatcher,
		model:      model,
	}
}

func (s *OpenAIService) LoadState(ctx context.Context) {
	storedDate, err := s.queries.GetBotState(ctx, botStateDateKey)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("warn: could not load bot state date: %v", err)
		}
		return
	}
	today := time.Now().Format("2006-01-02")
	if storedDate != today {
		log.Printf("new day (%s), resetting conversation context", today)
		return
	}
	val, err := s.queries.GetBotState(ctx, botStateKey)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("warn: could not load bot state: %v", err)
		}
		return
	}
	s.previousResponseID = val
	log.Printf("loaded previous_response_id: %s", val)
}

func (s *OpenAIService) Chat(ctx context.Context, userMessage string) (string, error) {
	params := responses.ResponseNewParams{
		Model:        openai.ChatModel(s.model),
		Instructions: openai.String(systemPrompt),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(userMessage),
		},
		Tools: s.dispatcher.Definitions(),
	}

	if s.previousResponseID != "" {
		params.PreviousResponseID = openai.String(s.previousResponseID)
	}

	response, err := s.client.Responses.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("openai request: %w", err)
	}

	// Agentic loop: process tool calls until the model returns final text
	for {
		var toolOutputs []responses.ResponseInputItemUnionParam

		for _, item := range response.Output {
			if item.Type == "function_call" {
				log.Printf("tool call: %s(%s)", item.Name, item.Arguments)
				result, execErr := s.dispatcher.Execute(ctx, item.Name, item.Arguments)
				if execErr != nil {
					log.Printf("tool error [%s]: %v", item.Name, execErr)
					result = fmt.Sprintf(`{"error": "%v"}`, execErr)
				}
				toolOutputs = append(toolOutputs,
					responses.ResponseInputItemParamOfFunctionCallOutput(item.CallID, result))
			}
		}

		if len(toolOutputs) == 0 {
			break
		}

		// Follow-up with tool outputs: no need to resend Instructions or Tools,
		// the model already has them in context via PreviousResponseID.
		response, err = s.client.Responses.New(ctx, responses.ResponseNewParams{
			Model:              openai.ChatModel(s.model),
			PreviousResponseID: openai.String(response.ID),
			Input: responses.ResponseNewParamsInputUnion{
				OfInputItemList: responses.ResponseInputParam(toolOutputs),
			},
		})
		if err != nil {
			return "", fmt.Errorf("openai tool result: %w", err)
		}
	}

	s.previousResponseID = response.ID
	s.saveState(ctx)

	return response.OutputText(), nil
}

func (s *OpenAIService) saveState(ctx context.Context) {
	if err := s.queries.UpsertBotState(ctx, db.UpsertBotStateParams{
		Key:   botStateKey,
		Value: s.previousResponseID,
	}); err != nil {
		log.Printf("warn: could not save bot state: %v", err)
	}
	today := time.Now().Format("2006-01-02")
	if err := s.queries.UpsertBotState(ctx, db.UpsertBotStateParams{
		Key:   botStateDateKey,
		Value: today,
	}); err != nil {
		log.Printf("warn: could not save bot state date: %v", err)
	}
}
