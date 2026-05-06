package proxy

import (
	"encoding/json"
	"testing"
)

func TestTranslateOpenAIChatToAnthropicMessages(t *testing.T) {
	body := []byte(`{
		"model": "claude-3-5-sonnet",
		"messages": [
			{"role": "system", "content": "Be concise."},
			{"role": "user", "content": "Hello"}
		],
		"max_tokens": 64,
		"temperature": 0.2,
		"tools": [
			{"type":"function","function":{"name":"lookup","description":"Lookup data","parameters":{"type":"object"}}}
		]
	}`)

	translated, decoded, err := TranslateRequestBody(RequestSummary{Body: body, Protocol: ProtocolOpenAI}, TranslationOpenAIToAnthropic)
	if err != nil {
		t.Fatalf("TranslateRequestBody: %v", err)
	}
	if len(translated) == 0 {
		t.Fatal("translated body is empty")
	}
	if decoded["model"] != "claude-3-5-sonnet" {
		t.Fatalf("model = %#v", decoded["model"])
	}
	if decoded["system"] != "Be concise." {
		t.Fatalf("system = %#v", decoded["system"])
	}
	messages, _ := decoded["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["role"] != "user" || messages[0].(map[string]any)["content"] != "Hello" {
		t.Fatalf("messages = %#v", decoded["messages"])
	}
	tools, _ := decoded["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", decoded["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "lookup" || tool["description"] != "Lookup data" {
		t.Fatalf("tool = %#v", tool)
	}
	if _, ok := tool["input_schema"].(map[string]any); !ok {
		t.Fatalf("tool input_schema = %#v", tool["input_schema"])
	}
}

func TestTranslateAnthropicMessagesToOpenAIChat(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4.1",
		"system": "Be concise.",
		"messages": [{"role":"user","content":"Hello"}],
		"max_tokens": 64,
		"temperature": 0.2,
		"tools": [{"name":"lookup","description":"Lookup data","input_schema":{"type":"object"}}]
	}`)

	translated, decoded, err := TranslateRequestBody(RequestSummary{Body: body, Protocol: ProtocolAnthropic}, TranslationAnthropicToOpenAI)
	if err != nil {
		t.Fatalf("TranslateRequestBody: %v", err)
	}
	if len(translated) == 0 {
		t.Fatal("translated body is empty")
	}
	messages, _ := decoded["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %#v", decoded["messages"])
	}
	if messages[0].(map[string]any)["role"] != "system" || messages[0].(map[string]any)["content"] != "Be concise." {
		t.Fatalf("system message = %#v", messages[0])
	}
	tools, _ := decoded["tools"].([]any)
	tool := tools[0].(map[string]any)
	function := tool["function"].(map[string]any)
	if tool["type"] != "function" || function["name"] != "lookup" {
		t.Fatalf("tool = %#v", tool)
	}
}

func TestTranslateAnthropicMessageToOpenAIChatCompletion(t *testing.T) {
	body := []byte(`{
		"id":"msg_123",
		"type":"message",
		"role":"assistant",
		"model":"claude-3-5-sonnet",
		"content":[{"type":"text","text":"Hello there"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":3,"output_tokens":4}
	}`)

	translated, decoded, err := TranslateResponseBody(body, TranslationOpenAIToAnthropic)
	if err != nil {
		t.Fatalf("TranslateResponseBody: %v", err)
	}
	if !json.Valid(translated) {
		t.Fatalf("translated response is not valid JSON: %s", string(translated))
	}
	if decoded["object"] != "chat.completion" {
		t.Fatalf("object = %#v", decoded["object"])
	}
	choices := decoded["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "Hello there" {
		t.Fatalf("message = %#v", message)
	}
	usage := decoded["usage"].(map[string]any)
	if usage["prompt_tokens"].(float64) != 3 || usage["completion_tokens"].(float64) != 4 || usage["total_tokens"].(float64) != 7 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestTranslateOpenAIChatCompletionToAnthropicMessage(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl_123",
		"object":"chat.completion",
		"model":"gpt-4.1",
		"choices":[{"message":{"role":"assistant","content":"Hello there"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}
	}`)

	translated, decoded, err := TranslateResponseBody(body, TranslationAnthropicToOpenAI)
	if err != nil {
		t.Fatalf("TranslateResponseBody: %v", err)
	}
	if !json.Valid(translated) {
		t.Fatalf("translated response is not valid JSON: %s", string(translated))
	}
	if decoded["type"] != "message" || decoded["role"] != "assistant" {
		t.Fatalf("response = %#v", decoded)
	}
	content := decoded["content"].([]any)
	if content[0].(map[string]any)["text"] != "Hello there" {
		t.Fatalf("content = %#v", content)
	}
	usage := decoded["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 3 || usage["output_tokens"].(float64) != 4 {
		t.Fatalf("usage = %#v", usage)
	}
}
