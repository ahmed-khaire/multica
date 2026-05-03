package proxy

import (
	"encoding/json"

	"github.com/multica-ai/multica/server/internal/gateway/management"
	"github.com/multica-ai/multica/server/pkg/redact"
)

type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Source           string
}

func CaptureJSON(policy string, value any) []byte {
	switch policy {
	case management.CaptureMetadataOnly:
		return nil
	case management.CaptureFullContent:
		return mustMarshal(value)
	default:
		return mustMarshal(redactValue(value))
	}
}

func redactValue(value any) any {
	switch v := value.(type) {
	case string:
		return redact.Text(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = redactValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactValue(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(v))
		for i, item := range v {
			redacted, _ := redactValue(item).(map[string]any)
			out[i] = redacted
		}
		return out
	default:
		return v
	}
}

func ExtractOpenAIUsage(resp map[string]any) Usage {
	usageMap, _ := resp["usage"].(map[string]any)
	prompt := int64Value(usageMap["prompt_tokens"])
	completion := int64Value(usageMap["completion_tokens"])
	total := int64Value(usageMap["total_tokens"])
	if total == 0 && (prompt > 0 || completion > 0) {
		total = prompt + completion
	}
	return usageWithSource(prompt, completion, total)
}

func ExtractAnthropicUsage(resp map[string]any) Usage {
	usageMap, _ := resp["usage"].(map[string]any)
	prompt := int64Value(usageMap["input_tokens"])
	completion := int64Value(usageMap["output_tokens"])
	return usageWithSource(prompt, completion, prompt+completion)
}

func usageWithSource(prompt, completion, total int64) Usage {
	source := "unknown"
	if prompt > 0 || completion > 0 || total > 0 {
		source = "upstream"
	}
	return Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		Source:           source,
	}
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func mustMarshal(value any) []byte {
	if value == nil {
		return nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return b
}
