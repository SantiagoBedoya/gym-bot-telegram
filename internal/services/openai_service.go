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
   - Si no sabes qué rutinas tiene el usuario, llama list_routines primero.
   - Llama get_routine para obtener la lista de ejercicios con sus rangos objetivo.
   - Llama get_last_session_summary para obtener la sesión anterior de esa rutina (pesos y reps reales).
     * Si no hay sesión anterior, usa get_exercise_history por ejercicio para buscar historial.
   - Con los datos de la sesión anterior, aplica sobrecarga progresiva inteligente:
     * Doble progresión: si en la sesión anterior se completaron todas las reps del rango máximo en TODOS los sets → sube el peso mínimo recomendado (2.5 kg en upper, 5 kg en lower)
     * Progresión de reps: si no se llegó al rango máximo → mantén el mismo peso, apunta a más reps
     * Deload: si el RPE fue ≥ 9 o las reps bajaron respecto a sesiones anteriores → mantén o reduce ligeramente el peso
   - Presenta el plan completo indicando para cada ejercicio: peso mínimo recomendado basado en la sesión anterior, series × reps objetivo, y series de calentamiento (40%, 60%, 80% del peso efectivo).

3. REGISTRAR SESIÓN: Cuando el usuario reporta lo que hizo (ej: "hice 4x8 con 80kg en press"),
   extrae ejercicio, sets, reps y peso, luego llama log_session_sets.
   Si no está claro a qué rutina pertenece, pregunta.

4. RESPONDER PREGUNTAS: Responde preguntas sobre progreso, historial o principios de entrenamiento.
   Usa las tools para obtener datos reales antes de responder.

Reglas importantes:
- Nunca inventes pesos o datos de historial. Siempre usa las tools para obtener información real.
- Respuestas concisas y prácticas, como lo haría un entrenador real.
- Usa kg como unidad de peso.
- Si no sabes qué rutinas tiene el usuario, llama list_routines antes de responder.
- Si una rutina no existe, pide al usuario que la defina primero.
- Formato de respuesta: usa exclusivamente HTML de Telegram. Etiquetas permitidas: <b>negrita</b>, <i>cursiva</i>, <code>código</code>, <pre>bloque de código</pre>. NO uses markdown estándar (**, *, #, -, etc.). Para listas usa guiones simples sin formato o saltos de línea.

RESPUESTAS RÁPIDAS (botones):
Cuando termines de presentar un ejercicio o acuses recibo de una serie y estés esperando que el usuario reporte la siguiente, añade AL FINAL de tu mensaje (después de todo el texto) una línea con este formato exacto:
[QR:opción1|opción2|opción3]
- Cada opción es el mensaje corto que se enviará al bot si el usuario presiona ese botón.
- Incluye siempre como primera opción repetir exactamente lo mismo que la serie anterior (mismas reps y peso). Ej: si acaba de hacer "Serie 1: 12 reps 80kg", la primera opción es "S2: 12 reps 80kg".
- Añade 1-2 variantes realistas (una rep menos, o un kg menos si el RPE fue alto).
- Las opciones deben ser frases cortas y naturales, máximo ~25 caracteres cada una.
- NO incluyas [QR:...] cuando estés registrando la sesión completa, respondiendo preguntas generales, o mostrando el plan inicial.`

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
