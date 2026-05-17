package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func (d *Daemon) executeCodexGatewayRuntimeRequest(ctx context.Context, job *GatewayJob) (map[string]any, error) {
	entry, ok := d.cfg.Agents["codex"]
	if !ok {
		return nil, fmt.Errorf("no agent configured for provider %q", "codex")
	}
	prompt, err := openAIChatToCodexPrompt(job.RequestBody)
	if err != nil {
		return nil, err
	}
	model, _ := job.RequestBody["model"].(string)
	model = strings.TrimPrefix(model, "codex:")

	backend, err := agent.New("codex", agent.Config{
		ExecutablePath: entry.Path,
		Logger:         d.logger,
	})
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	session, err := backend.Execute(ctx, prompt, agent.ExecOptions{
		Cwd:     cwd,
		Model:   model,
		Timeout: d.cfg.AgentTimeout,
	})
	if err != nil {
		return nil, err
	}
	go func() {
		for range session.Messages {
		}
	}()
	result := <-session.Result
	if result.Status != "completed" {
		if result.Error != "" {
			return nil, errors.New(result.Error)
		}
		return nil, fmt.Errorf("codex execution %s", result.Status)
	}
	if result.Output == "" {
		return nil, fmt.Errorf("codex returned empty output")
	}
	return codexOutputToOpenAIChat(model, result.Output), nil
}

func openAIChatToCodexPrompt(body map[string]any) (string, error) {
	if stream, _ := body["stream"].(bool); stream {
		return "", fmt.Errorf("streaming gateway requests are not supported for codex subscription runtime")
	}
	if _, ok := body["tools"]; ok {
		return "", fmt.Errorf("tools are not supported for codex subscription runtime")
	}
	if _, ok := body["response_format"]; ok {
		return "", fmt.Errorf("response_format is not supported for codex subscription runtime")
	}
	rawMessages, ok := body["messages"].([]any)
	if !ok || len(rawMessages) == 0 {
		return "", fmt.Errorf("messages are required")
	}

	var b strings.Builder
	for _, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("message must be an object")
		}
		role, _ := msg["role"].(string)
		content, err := openAIMessageContentText(msg["content"])
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		b.WriteString(titleRole(role))
		b.WriteString(":\n")
		b.WriteString(content)
		b.WriteString("\n\n")
	}
	prompt := strings.TrimSpace(b.String())
	if prompt == "" {
		return "", fmt.Errorf("messages contain no text content")
	}
	return prompt, nil
}

func openAIMessageContentText(raw any) (string, error) {
	switch content := raw.(type) {
	case string:
		return content, nil
	case []any:
		var parts []string
		for _, item := range content {
			block, ok := item.(map[string]any)
			if !ok {
				return "", fmt.Errorf("message content block must be an object")
			}
			blockType, _ := block["type"].(string)
			if blockType != "text" {
				return "", fmt.Errorf("only text content blocks are supported")
			}
			text, _ := block["text"].(string)
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n"), nil
	default:
		return "", fmt.Errorf("message content must be text")
	}
}

func titleRole(role string) string {
	switch role {
	case "system":
		return "System"
	case "assistant":
		return "Assistant"
	case "user":
		return "User"
	default:
		return "Message"
	}
}

func codexOutputToOpenAIChat(model, output string) map[string]any {
	return map[string]any{
		"id":      fmt.Sprintf("chatcmpl-multica-%d", time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": output,
				},
				"finish_reason": "stop",
			},
		},
	}
}
