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

	doctorCmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose Observer Gateway setup and health",
		RunE:  runGatewayDoctor,
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

	ingestKeyCmd := &cobra.Command{
		Use:   "ingest-key",
		Short: "Create an Observer SDK ingest key",
		RunE:  runGatewayIngestKey,
	}

	ingestKeysCmd := &cobra.Command{
		Use:   "ingest-keys",
		Short: "List Observer SDK ingest keys",
		RunE:  runGatewayIngestKeys,
	}

	revokeIngestKeyCmd := &cobra.Command{
		Use:   "revoke-ingest-key <key-id>",
		Short: "Revoke an Observer SDK ingest key",
		Args:  exactArgs(1),
		RunE:  runGatewayRevokeIngestKey,
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

	credentialsCmd := &cobra.Command{
		Use:   "credentials <backend-id>",
		Short: "List Observer Gateway backend credentials",
		Args:  exactArgs(1),
		RunE:  runGatewayCredentials,
	}

	credentialCmd := &cobra.Command{
		Use:   "credential",
		Short: "Manage Observer Gateway backend credentials",
	}

	credentialAddCmd := &cobra.Command{
		Use:   "add <backend-id>",
		Short: "Add an Observer Gateway backend credential",
		Args:  exactArgs(1),
		RunE:  runGatewayCredentialAdd,
	}

	credentialDisableCmd := &cobra.Command{
		Use:   "disable <backend-id> <credential-id>",
		Short: "Disable an Observer Gateway backend credential",
		Args:  exactArgs(2),
		RunE:  runGatewayCredentialDisable,
	}

	exportCmd := &cobra.Command{
		Use:   "export",
		Short: "Export Observer Gateway observability and governance data",
		RunE:  runGatewayExport,
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
	cmd.AddCommand(doctorCmd)
	cmd.AddCommand(keyCmd)
	cmd.AddCommand(keysCmd)
	cmd.AddCommand(revokeCmd)
	cmd.AddCommand(ingestKeyCmd)
	cmd.AddCommand(ingestKeysCmd)
	cmd.AddCommand(revokeIngestKeyCmd)
	cmd.AddCommand(addCmd)
	cmd.AddCommand(backendsCmd)
	cmd.AddCommand(credentialsCmd)
	cmd.AddCommand(credentialCmd)
	cmd.AddCommand(exportCmd)
	cmd.AddCommand(defaultCmd)
	cmd.AddCommand(policyCmd)
	credentialCmd.AddCommand(credentialAddCmd)
	credentialCmd.AddCommand(credentialDisableCmd)

	statusCmd.Flags().String("output", "table", "Output format: table or json")
	doctorCmd.Flags().String("output", "table", "Output format: table or json")
	keyCmd.Flags().String("output", "env", "Output format: env or json")
	keysCmd.Flags().String("output", "table", "Output format: table or json")
	ingestKeyCmd.Flags().String("app-id", "", "Application identifier attached to ingested traces")
	ingestKeyCmd.Flags().String("name", "", "Ingest key display name")
	ingestKeyCmd.Flags().String("output", "env", "Output format: env or json")
	ingestKeysCmd.Flags().String("output", "table", "Output format: table or json")
	revokeIngestKeyCmd.Flags().String("output", "table", "Output format: table or json")

	addCmd.Flags().String("key", "", "Upstream provider API key")
	addCmd.Flags().String("base-url", "", "Upstream provider base URL")
	addCmd.Flags().String("name", "", "Backend display name")
	addCmd.Flags().String("slug", "", "Backend slug")
	addCmd.Flags().Bool("set-default", false, "Set this backend as the workspace default")
	addCmd.Flags().String("output", "table", "Output format: table or json")

	backendsCmd.Flags().String("output", "table", "Output format: table or json")
	credentialsCmd.Flags().String("output", "table", "Output format: table or json")
	credentialAddCmd.Flags().String("key", "", "Upstream provider API key")
	credentialAddCmd.Flags().String("label", "", "Credential label")
	credentialAddCmd.Flags().Int32("priority", 100, "Routing priority, lower values are tried first")
	credentialAddCmd.Flags().String("output", "table", "Output format: table or json")
	credentialDisableCmd.Flags().String("output", "table", "Output format: table or json")
	exportCmd.Flags().String("since", "24h", "Export lookback window, such as 24h, 7d, or an RFC3339 timestamp")
	exportCmd.Flags().Int32("limit", 50, "Maximum rows per exported section")
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

type gatewayBackendCredentialDTO struct {
	ID                 string  `json:"id"`
	BackendID          string  `json:"backend_id"`
	Label              string  `json:"label"`
	CredentialHint     string  `json:"credential_hint"`
	Enabled            bool    `json:"enabled"`
	Priority           int32   `json:"priority"`
	LastUsedAt         *string `json:"last_used_at"`
	LastErrorAt        *string `json:"last_error_at"`
	LastError          string  `json:"last_error"`
	RateLimitedUntil   *string `json:"rate_limited_until"`
	RateLimitRemaining *int32  `json:"rate_limit_remaining"`
	RateLimitResetAt   *string `json:"rate_limit_reset_at"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
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

type gatewayDoctorDTO struct {
	Status      string                  `json:"status"`
	Checks      []gatewayDoctorCheckDTO `json:"checks"`
	GeneratedAt string                  `json:"generated_at"`
}

type gatewayDoctorCheckDTO struct {
	ID          string         `json:"id"`
	Category    string         `json:"category"`
	Status      string         `json:"status"`
	Title       string         `json:"title"`
	Detail      string         `json:"detail"`
	Remediation string         `json:"remediation"`
	Metadata    map[string]any `json:"metadata"`
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

type gatewayIngestKeyDTO struct {
	ID             string  `json:"id"`
	Key            string  `json:"key"`
	KeyPrefix      string  `json:"key_prefix"`
	AppID          string  `json:"app_id"`
	DisplayName    string  `json:"display_name"`
	GatewayBaseURL string  `json:"gateway_base_url"`
	RevokedAt      *string `json:"revoked_at"`
	LastUsedAt     *string `json:"last_used_at"`
	CreatedAt      string  `json:"created_at"`
}

type gatewayIngestKeyListItemDTO struct {
	ID          string  `json:"id"`
	KeyPrefix   string  `json:"key_prefix"`
	AppID       string  `json:"app_id"`
	DisplayName string  `json:"display_name"`
	RevokedAt   *string `json:"revoked_at"`
	LastUsedAt  *string `json:"last_used_at"`
	CreatedAt   string  `json:"created_at"`
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

func runGatewayDoctor(cmd *cobra.Command, _ []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var resp gatewayDoctorDTO
	if err := client.GetJSON(ctx, "/api/gateway/doctor", &resp); err != nil {
		return fmt.Errorf("run gateway doctor: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), resp)
	}

	w := cmd.OutOrStdout()
	fmt.Fprintln(w, "Observer Gateway Doctor")
	fmt.Fprintf(w, "Result: %s\n", resp.Status)
	if resp.GeneratedAt != "" {
		fmt.Fprintf(w, "Generated: %s\n", resp.GeneratedAt)
	}
	fmt.Fprintln(w)

	rows := make([][]string, 0, len(resp.Checks))
	for _, check := range resp.Checks {
		rows = append(rows, []string{
			check.Status,
			check.Category,
			check.Title,
			check.Detail,
			check.Remediation,
		})
	}
	cli.PrintTable(w, []string{"STATUS", "CATEGORY", "CHECK", "DETAIL", "REMEDIATION"}, rows)
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

func runGatewayIngestKey(cmd *cobra.Command, _ []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	body := map[string]any{}
	if appID, _ := cmd.Flags().GetString("app-id"); strings.TrimSpace(appID) != "" {
		body["app_id"] = strings.TrimSpace(appID)
	}
	if name, _ := cmd.Flags().GetString("name"); strings.TrimSpace(name) != "" {
		body["display_name"] = strings.TrimSpace(name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var resp gatewayIngestKeyDTO
	if err := client.PostJSON(ctx, "/api/gateway/ingest-keys", body, &resp); err != nil {
		return fmt.Errorf("create gateway ingest key: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), resp)
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "MULTICA_OBSERVER_GATEWAY_BASE_URL=%s\n", resp.GatewayBaseURL)
	fmt.Fprintf(w, "MULTICA_OBSERVER_KEY=%s\n", resp.Key)
	if resp.AppID != "" {
		fmt.Fprintf(w, "MULTICA_OBSERVER_APP_ID=%s\n", resp.AppID)
	}
	return nil
}

func runGatewayIngestKeys(cmd *cobra.Command, _ []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var keys []gatewayIngestKeyListItemDTO
	if err := client.GetJSON(ctx, "/api/gateway/ingest-keys", &keys); err != nil {
		return fmt.Errorf("list gateway ingest keys: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), keys)
	}

	rows := make([][]string, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, []string{
			key.ID,
			key.KeyPrefix,
			key.AppID,
			key.DisplayName,
			key.CreatedAt,
			nullableString(key.LastUsedAt),
			nullableString(key.RevokedAt),
		})
	}
	cli.PrintTable(cmd.OutOrStdout(), []string{"ID", "PREFIX", "APP ID", "NAME", "CREATED", "LAST USED", "REVOKED"}, rows)
	return nil
}

func runGatewayRevokeIngestKey(cmd *cobra.Command, args []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var resp gatewayIngestKeyListItemDTO
	keyID := args[0]
	path := "/api/gateway/ingest-keys/" + url.PathEscape(keyID) + "/revoke"
	if err := client.PostJSON(ctx, path, map[string]any{}, &resp); err != nil {
		return fmt.Errorf("revoke gateway ingest key: %w", err)
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

func runGatewayCredentials(cmd *cobra.Command, args []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	backendID := strings.TrimSpace(args[0])
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var credentials []gatewayBackendCredentialDTO
	path := "/api/gateway/backends/" + url.PathEscape(backendID) + "/credentials"
	if err := client.GetJSON(ctx, path, &credentials); err != nil {
		return fmt.Errorf("list gateway backend credentials: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), credentials)
	}

	rows := make([][]string, 0, len(credentials))
	for _, credential := range credentials {
		rows = append(rows, []string{
			credential.ID,
			credential.Label,
			credential.CredentialHint,
			yesNo(credential.Enabled),
			strconv.Itoa(int(credential.Priority)),
			nullableString(credential.RateLimitedUntil),
			nullableString(credential.LastUsedAt),
			nullableString(credential.LastErrorAt),
		})
	}
	cli.PrintTable(cmd.OutOrStdout(), []string{"ID", "LABEL", "KEY", "ENABLED", "PRIORITY", "RATE LIMITED", "LAST USED", "LAST ERROR"}, rows)
	return nil
}

func runGatewayCredentialAdd(cmd *cobra.Command, args []string) error {
	key, _ := cmd.Flags().GetString("key")
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("--key is required")
	}

	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	body := map[string]any{
		"key":     key,
		"enabled": true,
	}
	if label, _ := cmd.Flags().GetString("label"); strings.TrimSpace(label) != "" {
		body["label"] = strings.TrimSpace(label)
	}
	if priority, _ := cmd.Flags().GetInt32("priority"); priority > 0 {
		body["priority"] = priority
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	backendID := strings.TrimSpace(args[0])
	var resp gatewayBackendCredentialDTO
	path := "/api/gateway/backends/" + url.PathEscape(backendID) + "/credentials"
	if err := client.PostJSON(ctx, path, body, &resp); err != nil {
		return fmt.Errorf("add gateway backend credential: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), resp)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Added credential %s (%s)\n", resp.ID, resp.CredentialHint)
	return nil
}

func runGatewayCredentialDisable(cmd *cobra.Command, args []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	backendID := strings.TrimSpace(args[0])
	credentialID := strings.TrimSpace(args[1])
	var resp gatewayBackendCredentialDTO
	path := "/api/gateway/backends/" + url.PathEscape(backendID) + "/credentials/" + url.PathEscape(credentialID)
	if err := client.PatchJSON(ctx, path, map[string]any{"enabled": false}, &resp); err != nil {
		return fmt.Errorf("disable gateway backend credential: %w", err)
	}

	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(cmd.OutOrStdout(), resp)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Disabled credential %s\n", credentialID)
	return nil
}

func runGatewayExport(cmd *cobra.Command, _ []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}

	params := url.Values{}
	if since, _ := cmd.Flags().GetString("since"); strings.TrimSpace(since) != "" {
		params.Set("since", strings.TrimSpace(since))
	}
	if limit, _ := cmd.Flags().GetInt32("limit"); limit > 0 {
		params.Set("limit", strconv.Itoa(int(limit)))
	}
	path := "/api/gateway/export"
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var resp map[string]any
	if err := client.GetJSON(ctx, path, &resp); err != nil {
		return fmt.Errorf("export gateway data: %w", err)
	}
	return cli.PrintJSON(cmd.OutOrStdout(), resp)
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
