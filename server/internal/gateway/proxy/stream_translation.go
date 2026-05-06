package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
)

func TranslateStreamChunk(frame []byte, mode TranslationMode) [][]byte {
	if len(frame) == 0 {
		return nil
	}
	if mode == TranslationNone {
		return [][]byte{ensureSSEFrame(frame)}
	}

	dataLines := sseDataLines(frame)
	if len(dataLines) == 0 {
		return nil
	}
	data := strings.Join(dataLines, "\n")

	switch mode {
	case TranslationOpenAIToAnthropic:
		return translateAnthropicStreamEventToOpenAI(data)
	case TranslationAnthropicToOpenAI:
		return translateOpenAIStreamEventToAnthropic(data)
	default:
		return [][]byte{ensureSSEFrame(frame)}
	}
}

func sseDataLines(frame []byte) []string {
	lines := strings.Split(strings.ReplaceAll(string(frame), "\r\n", "\n"), "\n")
	data := []string{}
	for _, line := range lines {
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return data
}

func translateAnthropicStreamEventToOpenAI(data string) [][]byte {
	var event map[string]any
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}
	eventType, _ := event["type"].(string)
	switch eventType {
	case "content_block_delta":
		delta, _ := event["delta"].(map[string]any)
		if delta == nil || delta["type"] != "text_delta" {
			return nil
		}
		text, _ := delta["text"].(string)
		if text == "" {
			return nil
		}
		index := int(numberValue(event["index"]))
		return [][]byte{openAIStreamFrame(map[string]any{
			"choices": []any{map[string]any{
				"index": index,
				"delta": map[string]any{"content": text},
			}},
		})}
	case "message_stop":
		return [][]byte{[]byte("data: [DONE]\n\n")}
	default:
		return nil
	}
}

func translateOpenAIStreamEventToAnthropic(data string) [][]byte {
	if data == "[DONE]" {
		return [][]byte{anthropicStreamFrame("message_stop", map[string]any{"type": "message_stop"})}
	}

	var event map[string]any
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}
	choices, _ := event["choices"].([]any)
	if len(choices) == 0 {
		return nil
	}
	first, _ := choices[0].(map[string]any)
	if first == nil {
		return nil
	}
	delta, _ := first["delta"].(map[string]any)
	content, _ := delta["content"].(string)
	if content != "" {
		return [][]byte{anthropicStreamFrame("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": int(numberValue(first["index"])),
			"delta": map[string]any{
				"type": "text_delta",
				"text": content,
			},
		})}
	}
	if finishReason, _ := first["finish_reason"].(string); finishReason != "" {
		return [][]byte{anthropicStreamFrame("message_stop", map[string]any{"type": "message_stop"})}
	}
	return nil
}

func openAIStreamFrame(payload map[string]any) []byte {
	body, _ := json.Marshal(payload)
	return []byte("data: " + string(body) + "\n\n")
}

func anthropicStreamFrame(eventName string, payload map[string]any) []byte {
	body, _ := json.Marshal(payload)
	return []byte("event: " + eventName + "\ndata: " + string(body) + "\n\n")
}

func ensureSSEFrame(frame []byte) []byte {
	out := bytes.TrimRight(frame, "\r\n")
	return append(out, []byte("\n\n")...)
}
