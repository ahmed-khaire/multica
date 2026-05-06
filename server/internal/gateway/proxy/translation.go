package proxy

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/multica-ai/multica/server/internal/gateway/management"
)

func BackendProtocolForType(backendType string) string {
	switch backendType {
	case management.BackendTypeAnthropic, management.BackendTypeClaudeOAuth:
		return ProtocolAnthropic
	default:
		return ProtocolOpenAI
	}
}

func TranslationModeFor(clientProtocol, upstreamProtocol string) TranslationMode {
	switch {
	case clientProtocol == ProtocolOpenAI && upstreamProtocol == ProtocolAnthropic:
		return TranslationOpenAIToAnthropic
	case clientProtocol == ProtocolAnthropic && upstreamProtocol == ProtocolOpenAI:
		return TranslationAnthropicToOpenAI
	default:
		return TranslationNone
	}
}

func TranslateRequestBody(summary RequestSummary, mode TranslationMode) ([]byte, map[string]any, error) {
	body, decoded, err := decodeTranslationBody(summary.Body)
	if err != nil || mode == TranslationNone {
		return body, decoded, err
	}

	var translated map[string]any
	switch mode {
	case TranslationOpenAIToAnthropic:
		translated, err = openAIRequestToAnthropic(decoded)
	case TranslationAnthropicToOpenAI:
		translated, err = anthropicRequestToOpenAI(decoded)
	default:
		translated = decoded
	}
	if err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(translated)
	return out, translated, err
}

func TranslateResponseBody(body []byte, mode TranslationMode) ([]byte, map[string]any, error) {
	_, decoded, err := decodeTranslationBody(body)
	if err != nil || mode == TranslationNone {
		return body, decoded, err
	}

	var translated map[string]any
	switch mode {
	case TranslationOpenAIToAnthropic:
		translated, err = anthropicResponseToOpenAI(decoded)
	case TranslationAnthropicToOpenAI:
		translated, err = openAIResponseToAnthropic(decoded)
	default:
		translated = decoded
	}
	if err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(translated)
	return out, translated, err
}

func decodeTranslationBody(body []byte) ([]byte, map[string]any, error) {
	if len(body) == 0 {
		return body, nil, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, nil, err
	}
	return body, decoded, nil
}

func openAIRequestToAnthropic(in map[string]any) (map[string]any, error) {
	out := copyCommonRequestFields(in)
	messages, _ := in["messages"].([]any)
	anthropicMessages := make([]any, 0, len(messages))
	systemParts := []string{}
	for _, raw := range messages {
		msg, _ := raw.(map[string]any)
		role, _ := msg["role"].(string)
		content := textContent(msg["content"])
		if role == "system" {
			if content != "" {
				systemParts = append(systemParts, content)
			}
			continue
		}
		if role == "tool" {
			role = "user"
		}
		anthropicMessages = append(anthropicMessages, map[string]any{
			"role":    role,
			"content": content,
		})
	}
	if len(systemParts) > 0 {
		out["system"] = strings.Join(systemParts, "\n\n")
	}
	out["messages"] = anthropicMessages
	if tools, ok := in["tools"].([]any); ok {
		out["tools"] = openAIToolsToAnthropic(tools)
	}
	return out, nil
}

func anthropicRequestToOpenAI(in map[string]any) (map[string]any, error) {
	out := copyCommonRequestFields(in)
	messages := []any{}
	if system := textContent(in["system"]); system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	if rawMessages, ok := in["messages"].([]any); ok {
		for _, raw := range rawMessages {
			msg, _ := raw.(map[string]any)
			messages = append(messages, map[string]any{
				"role":    msg["role"],
				"content": textContent(msg["content"]),
			})
		}
	}
	out["messages"] = messages
	if tools, ok := in["tools"].([]any); ok {
		out["tools"] = anthropicToolsToOpenAI(tools)
	}
	return out, nil
}

func copyCommonRequestFields(in map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"model", "max_tokens", "temperature", "top_p", "stop", "stream"} {
		if value, ok := in[key]; ok {
			out[key] = value
		}
	}
	return out
}

func openAIToolsToAnthropic(tools []any) []any {
	out := make([]any, 0, len(tools))
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		if fn == nil {
			continue
		}
		item := map[string]any{
			"name":         fn["name"],
			"description":  fn["description"],
			"input_schema": fn["parameters"],
		}
		out = append(out, item)
	}
	return out
}

func anthropicToolsToOpenAI(tools []any) []any {
	out := make([]any, 0, len(tools))
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool["name"],
				"description": tool["description"],
				"parameters":  tool["input_schema"],
			},
		})
	}
	return out
}

func anthropicResponseToOpenAI(in map[string]any) (map[string]any, error) {
	content := anthropicText(in["content"])
	usage, _ := in["usage"].(map[string]any)
	prompt := numberValue(usage["input_tokens"])
	completion := numberValue(usage["output_tokens"])
	return map[string]any{
		"id":     in["id"],
		"object": "chat.completion",
		"model":  in["model"],
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": in["stop_reason"],
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      prompt + completion,
		},
	}, nil
}

func openAIResponseToAnthropic(in map[string]any) (map[string]any, error) {
	choices, _ := in["choices"].([]any)
	if len(choices) == 0 {
		return nil, errors.New("OpenAI response missing choices")
	}
	first, _ := choices[0].(map[string]any)
	message, _ := first["message"].(map[string]any)
	content := textContent(message["content"])
	usage, _ := in["usage"].(map[string]any)
	return map[string]any{
		"id":            in["id"],
		"type":          "message",
		"role":          "assistant",
		"model":         in["model"],
		"content":       []any{map[string]any{"type": "text", "text": content}},
		"stop_reason":   first["finish_reason"],
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  numberValue(usage["prompt_tokens"]),
			"output_tokens": numberValue(usage["completion_tokens"]),
		},
	}, nil
}

func textContent(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		parts := []string{}
		for _, raw := range v {
			block, _ := raw.(map[string]any)
			if block["type"] == "text" {
				if text, _ := block["text"].(string); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func anthropicText(value any) string {
	return textContent(value)
}

func numberValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		n, _ := v.Float64()
		return n
	default:
		return 0
	}
}
