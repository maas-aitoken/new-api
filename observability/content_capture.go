package observability

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// buildLangfuseInput keeps only model-context fields from the final upstream
// request. The shape is intentionally protocol-specific; this is a projection
// for observability, not another cross-protocol request schema.
func buildLangfuseInput(body []byte, format types.RelayFormat) ([]byte, bool) {
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, false
	}

	keys := []string{"messages", "tools"}
	switch format {
	case types.RelayFormatOpenAI:
		keys = []string{"messages", "tools"}
	case types.RelayFormatClaude:
		keys = []string{"system", "messages", "tools"}
	case types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
		keys = []string{"instructions", "input", "tools"}
	case types.RelayFormatGemini:
		keys = geminiContextFieldKeys
	default:
		// Do not project non-target protocols into an OpenAI-shaped record.
		// Their observability format is intentionally deferred to a later change.
		return nil, false
	}

	result := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil, false
	}
	encoded, err := common.Marshal(result)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

// geminiContextFieldKeys are the conversation-context fields of a Gemini
// generateContent request. Gemini accepts the system instruction in either
// casing, and a passthrough body keeps whichever one the client sent.
var geminiContextFieldKeys = []string{"systemInstruction", "system_instruction", "contents", "tools"}

// contextFieldKeys lists the conversation-context fields each relay format
// carries. They are the complement of buildLangfuseInput's whitelist: the
// observability value for request parameters is everything else.
func contextFieldKeys(format types.RelayFormat) ([]string, bool) {
	switch format {
	case types.RelayFormatOpenAI:
		return []string{"messages", "tools"}, true
	case types.RelayFormatClaude:
		return []string{"system", "messages", "tools"}, true
	case types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
		return []string{"instructions", "input", "tools"}, true
	case types.RelayFormatGemini:
		return geminiContextFieldKeys, true
	default:
		// Do not project non-target protocols into an OpenAI-shaped record.
		// Their observability format is intentionally deferred to a later change.
		return nil, false
	}
}

// stripContextFields removes the conversation-context fields from a request
// body and returns the remaining request parameters. It is the projection
// behind gen_ai.input.value / new_api.request.upstream: sampling, reasoning
// effort, and other knobs the gateway and the client negotiated.
func stripContextFields(body []byte, format types.RelayFormat) ([]byte, bool) {
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	keys, ok := contextFieldKeys(format)
	if !ok {
		return nil, false
	}
	for _, key := range keys {
		delete(payload, key)
	}
	if len(payload) == 0 {
		return nil, false
	}
	encoded, err := common.Marshal(payload)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

// normalizeLangfuseOutput converts supported upstream response envelopes into
// a compact array of semantic assistant output items. Response metadata such as
// ids, model, status, usage, and event type is deliberately discarded.
func normalizeLangfuseOutput(body []byte) ([]byte, bool) {
	var payload any
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, false
	}

	var output []any
	switch value := payload.(type) {
	case map[string]any:
		if response, ok := value["response"].(map[string]any); ok {
			if items, ok := response["output"].([]any); ok {
				output = filterResponsesOutput(items)
			}
		}
		if output == nil {
			if items, ok := value["output"].([]any); ok {
				output = filterResponsesOutput(items)
			}
		}
		if output == nil {
			if choices, ok := value["choices"].([]any); ok {
				output = filterChatChoices(choices)
			}
		}
		if output == nil {
			if _, ok := value["candidates"].([]any); ok {
				output = geminiCandidatesToChatOutput(body)
			}
		}
		if output == nil {
			if content, ok := value["content"].([]any); ok {
				output = []any{claudeContentToAssistantMessage(value, content)}
			} else if _, hasRole := value["role"]; hasRole {
				output = []any{filterAssistantMessage(value)}
			}
		}
	case []any:
		output = filterResponsesOutput(value)
	}
	if output == nil {
		return nil, false
	}
	encoded, err := common.Marshal(output)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

func filterChatChoices(choices []any) []any {
	result := make([]any, 0, len(choices))
	for _, raw := range choices {
		choice, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		message, ok := choice["message"].(map[string]any)
		if ok {
			result = append(result, filterAssistantMessage(message))
			continue
		}
		if text, ok := choice["text"].(string); ok {
			result = append(result, map[string]any{"role": "assistant", "content": text})
		}
	}
	if len(choices) > 0 && len(result) == 0 {
		return nil
	}
	return result
}

// geminiCandidatesToChatOutput reads a Gemini generateContent response through
// the converter the relay path uses to serve a Gemini upstream in OpenAI
// format, so text, thoughts and function calls reach the trace in the same
// assistant message shape as every other chat upstream.
func geminiCandidatesToChatOutput(body []byte) []any {
	var response dto.GeminiChatResponse
	if err := common.Unmarshal(body, &response); err != nil {
		return nil
	}
	encoded, err := common.Marshal(relayconvert.ResponseGeminiChat2OpenAI("", 0, &response))
	if err != nil {
		return nil
	}
	var converted struct {
		Choices []any `json:"choices"`
	}
	if err := common.Unmarshal(encoded, &converted); err != nil {
		return nil
	}
	return filterChatChoices(converted.Choices)
}

func filterAssistantMessage(message map[string]any) map[string]any {
	result := make(map[string]any)
	for _, key := range []string{"role", "content", "reasoning_content", "reasoning", "tool_calls", "refusal"} {
		if value, ok := message[key]; ok {
			result[key] = value
		}
	}
	if _, ok := result["role"]; !ok {
		result["role"] = "assistant"
	}
	return result
}

func filterResponsesOutput(items []any) []any {
	result := make([]any, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if role, hasRole := item["role"].(string); hasRole && role != "" {
			if _, hasType := item["type"].(string); !hasType {
				result = append(result, filterAssistantMessage(item))
				continue
			}
		}
		filtered := make(map[string]any)
		for _, key := range []string{"type", "role", "content", "call_id", "name", "arguments", "input", "result"} {
			if value, exists := item[key]; exists {
				if key == "role" {
					if role, ok := value.(string); ok && role == "" {
						continue
					}
				}
				if key == "content" {
					value = filterResponseContent(value)
				}
				filtered[key] = value
			}
		}
		if len(filtered) > 0 {
			result = append(result, filtered)
		}
	}
	if len(items) > 0 && len(result) == 0 {
		return nil
	}
	return result
}

func filterResponseContent(value any) any {
	items, ok := value.([]any)
	if !ok {
		return value
	}
	result := make([]any, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		filtered := make(map[string]any)
		for _, key := range []string{"type", "text"} {
			if value, exists := item[key]; exists {
				filtered[key] = value
			}
		}
		if len(filtered) > 0 {
			result = append(result, filtered)
		}
	}
	return result
}

func claudeContentToAssistantMessage(payload map[string]any, content []any) map[string]any {
	message := map[string]any{"role": "assistant"}
	var textBuilder strings.Builder
	var reasoningBuilder strings.Builder
	var toolCalls []any
	for _, raw := range content {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch block["type"] {
		case "text":
			if text, ok := block["text"].(string); ok {
				textBuilder.WriteString(text)
			}
		case "thinking", "redacted_thinking":
			if thinking, ok := block["thinking"].(string); ok {
				reasoningBuilder.WriteString(thinking)
			}
		case "tool_use":
			call := map[string]any{"type": "function"}
			if id, ok := block["id"]; ok {
				call["id"] = id
			}
			function := map[string]any{}
			if name, ok := block["name"]; ok {
				function["name"] = name
			}
			if input, ok := block["input"]; ok {
				if args, err := common.Marshal(input); err == nil {
					function["arguments"] = string(args)
				}
			}
			call["function"] = function
			toolCalls = append(toolCalls, call)
		}
	}
	message["content"] = textBuilder.String()
	if reasoningBuilder.Len() > 0 {
		message["reasoning_content"] = reasoningBuilder.String()
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	if _, ok := payload["role"]; ok {
		message["role"] = payload["role"]
	}
	return message
}
