package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var gatewayCmd = newGatewayCommand()

func newGatewayCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "Manage Observer Gateway keys and backends",
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show Observer Gateway status",
		RunE:  runGatewayStatus,
	}

	keyCmd := &cobra.Command{
		Use:   "key",
		Short: "Create or print your Observer Gateway key",
		RunE:  runGatewayKey,
	}

	keysCmd := &cobra.Command{
		Use:   "keys",
		Short: "List your Observer Gateway keys",
		RunE:  runGatewayKeys,
	}

	revokeCmd := &cobra.Command{
		Use:   "revoke <key-id>",
		Short: "Revoke an Observer Gateway key",
		Args:  exactArgs(1),
		RunE:  runGatewayRevoke,
	}

	addCmd := &cobra.Command{
		Use:   "add <provider>",
		Short: "Add an Observer Gateway backend",
		Args:  exactArgs(1),
		RunE:  runGatewayAdd,
	}

	backendsCmd := &cobra.Command{
		Use:   "backends",
		Short: "List Observer Gateway backends",
		RunE:  runGatewayBackends,
	}

	defaultCmd := &cobra.Command{
		Use:   "default <backend-slug>",
		Short: "Set the default Observer Gateway backend",
		Args:  exactArgs(1),
		RunE:  runGatewayDefault,
	}

	policyCmd := &cobra.Command{
		Use:   "policy <capture-policy>",
		Short: "Set the Observer Gateway capture policy",
		Args:  exactArgs(1),
		RunE:  runGatewayPolicy,
	}

	cmd.AddCommand(statusCmd)
	cmd.AddCommand(keyCmd)
	cmd.AddCommand(keysCmd)
	cmd.AddCommand(revokeCmd)
	cmd.AddCommand(addCmd)
	cmd.AddCommand(backendsCmd)
	cmd.AddCommand(defaultCmd)
	cmd.AddCommand(policyCmd)

	statusCmd.Flags().String("output", "table", "Output format: table or json")
	keyCmd.Flags().String("output", "env", "Output format: env or json")
	keysCmd.Flags().String("output", "table", "Output format: table or json")

	addCmd.Flags().String("key", "", "Upstream provider API key")
	addCmd.Flags().String("base-url", "", "Upstream provider base URL")
	addCmd.Flags().String("name", "", "Backend display name")
	addCmd.Flags().String("slug", "", "Backend slug")
	addCmd.Flags().Bool("set-default", false, "Set this backend as the workspace default")
	addCmd.Flags().String("output", "table", "Output format: table or json")

	backendsCmd.Flags().String("output", "table", "Output format: table or json")
	defaultCmd.Flags().String("output", "table", "Output format: table or json")
	policyCmd.Flags().String("output", "table", "Output format: table or json")

	return cmd
}

type gatewayBackendDTO struct {
	ID             string         `json:"id"`
	Slug           string         `json:"slug"`
	DisplayName    string         `json:"display_name"`
	BackendType    string         `json:"backend_type"`
	BaseURL        string         `json:"base_url"`
	CredentialHint string         `json:"credential_hint"`
	Enabled        bool           `json:"enabled"`
	IsDefault      bool           `json:"is_default"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
}

type gatewayStatusDTO struct {
	OpenAIBaseURL       string             `json:"openai_base_url"`
	AnthropicBaseURL    string             `json:"anthropic_base_url"`
	CapturePolicy       string             `json:"capture_policy"`
	DefaultBackend      *gatewayBackendDTO `json:"default_backend"`
	BackendCount        int                `json:"backend_count"`
	EnabledBackendCount int                `json:"enabled_backend_count"`
	HasActiveKey        bool               `json:"has_active_key"`
}

type gatewayKeyDTO struct {
	ID               string  `json:"id"`
	Key              string  `json:"key"`
	KeyPrefix        string  `json:"key_prefix"`
	OpenAIBaseURL    string  `json:"openai_base_url"`
	OpenAIAPIKey     string  `json:"openai_api_key"`
	AnthropicBaseURL string  `json:"anthropic_base_url"`
	AnthropicAPIKey  string  `json:"anthropic_api_key"`
	CreatedAt        string  `json:"created_at"`
	LastUsedAt       *string `json:"last_used_at"`
}

type gatewayKeyListItemDTO struct {
	ID         string  `json:"id"`
	KeyPrefix  string  `json:"key_prefix"`
	RevokedAt  *string `json:"revoked_at"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

type gatewaySettingsDTO struct {
	CapturePolicy  string             `json:"capture_policy"`
	DefaultBackend *gatewayBackendDTO `json:"default_backend"`
}

func gatewayClient(cmd *cobra.Command) (*cli.APIClient, error) {
	client, err := newAPIClient(cmd)
	if err != nil {
		return nil, err
	}
	workspaceID, err := requireWorkspaceID(cmd)
	if err != nil {
		return nil, err
	}
	client.WorkspaceID = workspaceID
	return client, nil
}

func runGatewayKey(cmd *cobra.Command, _ []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var resp gatewayKeyDTO
	if err := client.PostJSON(ctx, "/api/gateway/key", map[string]any{}, &resp); err != nil {
		return fmt.Errorf("create gateway key: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), resp)
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "OPENAI_BASE_URL=%s\n", resp.OpenAIBaseURL)
	fmt.Fprintf(w, "OPENAI_API_KEY=%s\n", resp.OpenAIAPIKey)
	fmt.Fprintf(w, "ANTHROPIC_BASE_URL=%s\n", resp.AnthropicBaseURL)
	fmt.Fprintf(w, "ANTHROPIC_API_KEY=%s\n", resp.AnthropicAPIKey)
	return nil
}

func runGatewayStatus(cmd *cobra.Command, _ []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var resp gatewayStatusDTO
	if err := client.GetJSON(ctx, "/api/gateway/status", &resp); err != nil {
		return fmt.Errorf("show gateway status: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), resp)
	}

	defaultBackend := "none"
	if resp.DefaultBackend != nil && resp.DefaultBackend.Slug != "" {
		defaultBackend = resp.DefaultBackend.Slug
	}
	rows := [][]string{
		{"OPENAI BASE URL", resp.OpenAIBaseURL},
		{"ANTHROPIC BASE URL", resp.AnthropicBaseURL},
		{"CAPTURE POLICY", resp.CapturePolicy},
		{"DEFAULT BACKEND", defaultBackend},
		{"BACKENDS", strconv.Itoa(resp.BackendCount)},
		{"ENABLED BACKENDS", strconv.Itoa(resp.EnabledBackendCount)},
		{"ACTIVE KEY", yesNo(resp.HasActiveKey)},
	}
	cli.PrintTable(cmd.OutOrStdout(), []string{"FIELD", "VALUE"}, rows)
	return nil
}

func runGatewayKeys(cmd *cobra.Command, _ []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var keys []gatewayKeyListItemDTO
	if err := client.GetJSON(ctx, "/api/gateway/keys", &keys); err != nil {
		return fmt.Errorf("list gateway keys: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), keys)
	}

	rows := make([][]string, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, []string{
			key.ID,
			key.KeyPrefix,
			key.CreatedAt,
			nullableString(key.LastUsedAt),
			nullableString(key.RevokedAt),
		})
	}
	cli.PrintTable(cmd.OutOrStdout(), []string{"ID", "PREFIX", "CREATED", "LAST USED", "REVOKED"}, rows)
	return nil
}

func runGatewayRevoke(cmd *cobra.Command, args []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var resp gatewayKeyListItemDTO
	keyID := args[0]
	path := "/api/gateway/keys/" + url.PathEscape(keyID) + "/revoke"
	if err := client.PostJSON(ctx, path, map[string]any{}, &resp); err != nil {
		return fmt.Errorf("revoke gateway key: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), resp)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s\n", keyID)
	return nil
}

func runGatewayAdd(cmd *cobra.Command, args []string) error {
	provider := strings.ToLower(strings.TrimSpace(args[0]))
	key, _ := cmd.Flags().GetString("key")
	key = strings.TrimSpace(key)
	if provider != "claude-oauth" && key == "" {
		return fmt.Errorf("--key is required for provider %s", provider)
	}

	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	body := map[string]any{
		"provider": provider,
	}
	if key != "" {
		body["key"] = key
	}
	if v, _ := cmd.Flags().GetString("base-url"); strings.TrimSpace(v) != "" {
		body["base_url"] = strings.TrimSpace(v)
	}
	if v, _ := cmd.Flags().GetString("name"); strings.TrimSpace(v) != "" {
		body["display_name"] = strings.TrimSpace(v)
	}
	if v, _ := cmd.Flags().GetString("slug"); strings.TrimSpace(v) != "" {
		body["slug"] = strings.TrimSpace(v)
	}
	if v, _ := cmd.Flags().GetBool("set-default"); v {
		body["set_default"] = true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var resp gatewayBackendDTO
	if err := client.PostJSON(ctx, "/api/gateway/backends", body, &resp); err != nil {
		return fmt.Errorf("add gateway backend: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), resp)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Added %s (%s)\n", resp.Slug, resp.BaseURL)
	if resp.IsDefault {
		fmt.Fprintln(cmd.OutOrStdout(), "Default: yes")
	}
	return nil
}

func runGatewayBackends(cmd *cobra.Command, _ []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var backends []gatewayBackendDTO
	if err := client.GetJSON(ctx, "/api/gateway/backends", &backends); err != nil {
		return fmt.Errorf("list gateway backends: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), backends)
	}

	rows := make([][]string, 0, len(backends))
	for _, backend := range backends {
		rows = append(rows, []string{
			backend.Slug,
			backend.BackendType,
			backend.BaseURL,
			yesNo(backend.Enabled),
			yesNo(backend.IsDefault),
			backend.CredentialHint,
		})
	}
	cli.PrintTable(cmd.OutOrStdout(), []string{"SLUG", "TYPE", "BASE URL", "ENABLED", "DEFAULT", "KEY"}, rows)
	return nil
}

func runGatewayDefault(cmd *cobra.Command, args []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	backendSlug := strings.TrimSpace(args[0])
	body := map[string]any{"backend_slug": backendSlug}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var resp gatewaySettingsDTO
	if err := client.PostJSON(ctx, "/api/gateway/default", body, &resp); err != nil {
		return fmt.Errorf("set gateway default backend: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), resp)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Default backend set to %s\n", backendSlug)
	return nil
}

func runGatewayPolicy(cmd *cobra.Command, args []string) error {
	policy := strings.TrimSpace(args[0])
	switch policy {
	case "metadata_only", "redacted_content", "full_content":
	default:
		return fmt.Errorf("capture policy must be one of metadata_only, redacted_content, full_content")
	}

	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var resp gatewaySettingsDTO
	if err := client.PostJSON(ctx, "/api/gateway/policy", map[string]any{"capture_policy": policy}, &resp); err != nil {
		return fmt.Errorf("set gateway capture policy: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), resp)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Capture policy set to %s\n", policy)
	return nil
}

func nullableString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
