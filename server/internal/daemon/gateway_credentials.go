package daemon

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type gatewayCredentialBundle struct {
	AccountHint string            `json:"account_hint"`
	Files       map[string]string `json:"files"`
	Env         map[string]string `json:"env"`
	Token       string            `json:"token"`
}

func (d *Daemon) materializeGatewaySubscriptionCredential(job *GatewayJob) (GatewayValidationResult, error) {
	payload, err := decodeGatewayCredentialPayload(job)
	if err != nil {
		return GatewayValidationResult{}, err
	}

	root, err := d.gatewayCredentialRoot(job)
	if err != nil {
		return GatewayValidationResult{}, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return GatewayValidationResult{}, fmt.Errorf("create gateway credential directory: %w", err)
	}

	accountHint := strings.TrimSpace(job.SubscriptionProvider)
	switch strings.TrimSpace(job.PayloadFormat) {
	case "", "raw_token":
		if err := os.WriteFile(filepath.Join(root, "token.txt"), payload, 0o600); err != nil {
			return GatewayValidationResult{}, fmt.Errorf("write gateway credential token: %w", err)
		}
	default:
		bundle, err := parseGatewayCredentialBundle(job, payload)
		if err != nil {
			return GatewayValidationResult{}, err
		}
		if strings.TrimSpace(bundle.AccountHint) != "" {
			accountHint = strings.TrimSpace(bundle.AccountHint)
		}
		if strings.TrimSpace(bundle.Token) != "" {
			if err := os.WriteFile(filepath.Join(root, "token.txt"), []byte(bundle.Token), 0o600); err != nil {
				return GatewayValidationResult{}, fmt.Errorf("write gateway credential token: %w", err)
			}
		}
		if len(bundle.Env) > 0 {
			env, err := json.Marshal(bundle.Env)
			if err != nil {
				return GatewayValidationResult{}, fmt.Errorf("encode gateway credential env: %w", err)
			}
			if err := os.WriteFile(filepath.Join(root, "env.json"), env, 0o600); err != nil {
				return GatewayValidationResult{}, fmt.Errorf("write gateway credential env: %w", err)
			}
		}
		for name, content := range bundle.Files {
			path, err := safeCredentialPath(root, name)
			if err != nil {
				return GatewayValidationResult{}, err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return GatewayValidationResult{}, fmt.Errorf("create gateway credential file directory: %w", err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				return GatewayValidationResult{}, fmt.Errorf("write gateway credential file %q: %w", name, err)
			}
		}
	}

	sum := sha256.Sum256(append([]byte(job.SubscriptionProvider+":"), payload...))
	return GatewayValidationResult{
		AccountHint:        accountHint,
		AccountFingerprint: hex.EncodeToString(sum[:16]),
	}, nil
}

func decodeGatewayCredentialPayload(job *GatewayJob) ([]byte, error) {
	raw := strings.TrimSpace(job.Payload)
	if raw == "" {
		raw = strings.TrimSpace(job.EncryptedPayload)
	}
	if raw == "" {
		return nil, fmt.Errorf("gateway subscription credential payload is required")
	}
	payload, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode gateway subscription credential payload: %w", err)
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return nil, fmt.Errorf("gateway subscription credential payload is empty")
	}
	return payload, nil
}

func parseGatewayCredentialBundle(job *GatewayJob, payload []byte) (gatewayCredentialBundle, error) {
	expected := job.SubscriptionProvider + "_auth_bundle_v1"
	if job.SubscriptionProvider == "claude_code" {
		expected = "claude_code_auth_bundle_v1"
	}
	if job.PayloadFormat != expected {
		return gatewayCredentialBundle{}, fmt.Errorf("unsupported %s payload format %q", job.SubscriptionProvider, job.PayloadFormat)
	}
	var bundle gatewayCredentialBundle
	if err := json.Unmarshal(payload, &bundle); err != nil {
		return gatewayCredentialBundle{}, fmt.Errorf("parse gateway credential bundle: %w", err)
	}
	if len(bundle.Files) == 0 && len(bundle.Env) == 0 && strings.TrimSpace(bundle.Token) == "" {
		return gatewayCredentialBundle{}, fmt.Errorf("gateway credential bundle must include files, env, or token")
	}
	return bundle, nil
}

func (d *Daemon) gatewayCredentialRoot(job *GatewayJob) (string, error) {
	root := strings.TrimSpace(d.cfg.WorkspacesRoot)
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		root = filepath.Join(home, "multica_workspaces")
	}
	id := safeCredentialSegment(job.CredentialID)
	if id == "" {
		id = safeCredentialSegment(job.ID)
	}
	if id == "" {
		return "", fmt.Errorf("gateway credential id is required")
	}
	return filepath.Join(root, "gateway_credentials", id), nil
}

func safeCredentialPath(root, name string) (string, error) {
	name = filepath.Clean(strings.TrimSpace(name))
	if name == "." || filepath.IsAbs(name) || strings.HasPrefix(name, ".."+string(filepath.Separator)) || name == ".." {
		return "", fmt.Errorf("unsafe gateway credential file path %q", name)
	}
	return filepath.Join(root, name), nil
}

func safeCredentialSegment(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (d *Daemon) gatewayCredentialEnv(job *GatewayJob) map[string]string {
	root, err := d.gatewayCredentialRoot(job)
	if err != nil {
		return nil
	}
	env := map[string]string{}
	if job.SubscriptionProvider == "codex" {
		env["CODEX_HOME"] = root
	}
	if job.SubscriptionProvider == "claude_code" {
		env["CLAUDE_CONFIG_DIR"] = root
	}
	raw, err := os.ReadFile(filepath.Join(root, "env.json"))
	if err == nil {
		var stored map[string]string
		if json.Unmarshal(raw, &stored) == nil {
			for key, value := range stored {
				env[key] = value
			}
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}
