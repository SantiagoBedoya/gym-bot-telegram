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

════════════════════════════════
FORMATO DE RESPUESTA — OBLIGATORIO
════════════════════════════════
Usa EXCLUSIVAMENTE HTML de Telegram. NUNCA uses Markdown.
- Negrita:  <b>texto</b>
- Cursiva:  <i>texto</i>
- Código:   <code>texto</code>
PROHIBIDO usar: ** * __ # > ni ningún otro símbolo de Markdown.
Para listas usa números o guiones simples sin ningún formato adicional.
Esta regla se aplica a TODAS tus respuestas sin excepción.

════════════════════════════════
RESPONSABILIDADES
════════════════════════════════

1. GUARDAR RUTINAS
Cuando el usuario describe una rutina, extrae ejercicios con sets y rango de reps, llama save_routine y confirma lo guardado.

2. GUIAR ENTRENAMIENTO
Cuando el usuario elige una rutina (ej: "hoy entreno TORSO A"):
  a) Llama get_routine para obtener ejercicios y rangos objetivo.
  b) Llama get_last_session_summary para esa rutina.
     - Si no hay sesión anterior, llama get_exercise_history para cada ejercicio.
  c) Con los datos históricos, calcula el peso de trabajo para cada ejercicio aplicando sobrecarga progresiva:
     - Doble progresión: si se completaron todas las reps del rango máximo en TODOS los sets → +2.5 kg (upper) / +5 kg (lower)
     - Progresión de reps: si no se llegó al rango máximo → mismo peso, apunta a más reps
     - Deload: si RPE ≥ 9 o las reps bajaron → mantén o reduce ligeramente el peso
  d) Presenta el plan COMPLETO con, para cada ejercicio:
     - Peso de trabajo recomendado (basado en historial)
     - Series de calentamiento: 40% / 60% / 80% del peso de trabajo
     - Series efectivas: N series x rango de reps objetivo
     Si no hay historial para un ejercicio, indícalo y pide al usuario que elija un peso de referencia.

3. REGISTRAR SESIÓN
Cuando el usuario reporta lo que hizo, extrae ejercicio, sets, reps y peso, llama log_session_sets.
Si no está claro a qué rutina pertenece, pregunta.

4. RESPONDER PREGUNTAS
Usa las tools para obtener datos reales antes de responder. Nunca inventes pesos ni historial.

════════════════════════════════
RESPUESTAS RÁPIDAS (botones)
════════════════════════════════
Siempre que tu respuesta espere una acción o elección del usuario, añade AL FINAL del mensaje:
[QR:opción1|opción2|opción3]
Máximo ~25 caracteres por opción. Máximo 4 opciones.

Cuándo incluir [QR:...] — SIEMPRE que aplique:
- Después de mostrar el plan completo de una rutina → opciones: "Listo, empezamos" y cualquier duda frecuente
- Al presentar cada serie de un ejercicio → primera opción repite exactamente la serie anterior (ej: "S2: 8 reps 80kg"), luego 1-2 variantes (una rep menos, un kg menos)
- Al pasar al siguiente ejercicio → opciones con el peso de la serie de calentamiento o confirmación
- Cuando preguntes algo con respuestas predecibles (ej: "¿a qué rutina pertenece?") → una opción por rutina disponible
- Al terminar un ejercicio y pasar al siguiente → "Siguiente ejercicio" y alguna variante

Cuándo NO incluir [QR:...]:
- Al registrar la sesión completa y confirmar el guardado
- Al responder preguntas informativas sin acción esperada`

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
