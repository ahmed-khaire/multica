package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func (d *Daemon) executeClaudeCodeGatewayRuntimeRequest(ctx context.Context, job *GatewayJob) (map[string]any, error) {
	entry, ok := d.cfg.Agents["claude"]
	if !ok {
		return nil, fmt.Errorf("no agent configured for provider %q", "claude")
	}
	prompt, err := openAIChatToClaudeCodePrompt(job.RequestBody)
	if err != nil {
		return nil, err
	}
	model, _ := job.RequestBody["model"].(string)
	model = strings.TrimPrefix(model, "claude_code:")
	model = strings.TrimPrefix(model, "claude:")

	backend, err := agent.New("claude", agent.Config{
		ExecutablePath: entry.Path,
		Env:            d.gatewayCredentialEnv(job),
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
		return nil, fmt.Errorf("claude code execution %s", result.Status)
	}
	if result.Output == "" {
		return nil, fmt.Errorf("claude code returned empty output")
	}
	return codexOutputToOpenAIChat(model, result.Output), nil
}

func openAIChatToClaudeCodePrompt(body map[string]any) (string, error) {
	if stream, _ := body["stream"].(bool); stream {
		return "", fmt.Errorf("streaming gateway requests are not supported for claude code subscription runtime")
	}
	if _, ok := body["tools"]; ok {
		return "", fmt.Errorf("tools are not supported for claude code subscription runtime")
	}
	return openAIChatToCodexPrompt(body)
}
