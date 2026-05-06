import type { Issue, IssueStatus, IssuePriority, IssueAssigneeType } from "./issue";
import type { MemberRole } from "./workspace";

// Issue API
export interface CreateIssueRequest {
  title: string;
  description?: string;
  status?: IssueStatus;
  priority?: IssuePriority;
  assignee_type?: IssueAssigneeType;
  assignee_id?: string;
  parent_issue_id?: string;
  project_id?: string;
  due_date?: string;
  attachment_ids?: string[];
}

export interface UpdateIssueRequest {
  title?: string;
  description?: string;
  status?: IssueStatus;
  priority?: IssuePriority;
  assignee_type?: IssueAssigneeType | null;
  assignee_id?: string | null;
  position?: number;
  due_date?: string | null;
  parent_issue_id?: string | null;
  project_id?: string | null;
}

export interface ListIssuesParams {
  limit?: number;
  offset?: number;
  workspace_id?: string;
  status?: IssueStatus;
  priority?: IssuePriority;
  assignee_id?: string;
  open_only?: boolean;
}

export interface ListIssuesResponse {
  issues: Issue[];
  total: number;
  /** True total of done issues in the workspace (for load-more pagination). Not returned by backend API — set by the frontend query function. */
  doneTotal?: number;
}

export interface SearchIssueResult extends Issue {
  match_source: "title" | "description" | "comment";
  matched_snippet?: string;
}

export interface SearchIssuesResponse {
  issues: SearchIssueResult[];
  total: number;
}

export interface UpdateMeRequest {
  name?: string;
  avatar_url?: string;
}

export interface CreateMemberRequest {
  email: string;
  role?: MemberRole;
}

export interface UpdateMemberRequest {
  role: MemberRole;
}

// Personal Access Tokens
export interface PersonalAccessToken {
  id: string;
  name: string;
  token_prefix: string;
  expires_at: string | null;
  last_used_at: string | null;
  created_at: string;
}

export interface CreatePersonalAccessTokenRequest {
  name: string;
  expires_in_days?: number;
}

export interface CreatePersonalAccessTokenResponse extends PersonalAccessToken {
  token: string;
}

// Pagination
export interface PaginationParams {
  limit?: number;
  offset?: number;
}

// Gateway observability
export interface GatewayObservabilityParams {
  since?: string;
  limit?: number;
  status?: string;
  backend?: string;
  model?: string;
  signal?: AbortSignal;
}

export type GatewayCapturePolicy = "metadata_only" | "redacted_content" | "full_content";

export interface GatewayBackend {
  id: string;
  slug: string;
  display_name: string;
  backend_type: string;
  base_url: string;
  credential_hint: string;
  enabled: boolean;
  is_default: boolean;
  metadata: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface GatewaySettingsResponse {
  capture_policy: GatewayCapturePolicy | string;
  default_backend: GatewayBackend | null;
}

export interface GatewayStatusResponse extends GatewaySettingsResponse {
  openai_base_url: string;
  anthropic_base_url: string;
  backend_count: number;
  enabled_backend_count: number;
  has_active_key: boolean;
}

export type GatewayDoctorStatus = "healthy" | "healthy_with_warnings" | "unhealthy";
export type GatewayDoctorCheckStatus = "pass" | "warning" | "fail";

export interface GatewayDoctorCheck {
  id: string;
  category: string;
  status: GatewayDoctorCheckStatus;
  title: string;
  detail: string;
  remediation: string;
  metadata?: Record<string, unknown>;
}

export interface GatewayDoctorResponse {
  status: GatewayDoctorStatus;
  checks: GatewayDoctorCheck[];
  generated_at: string;
}

export interface GatewayUserKeyResponse {
  id: string;
  key: string;
  key_prefix: string;
  openai_base_url: string;
  openai_api_key: string;
  anthropic_base_url: string;
  anthropic_api_key: string;
  created_at: string;
  last_used_at: string | null;
}

export interface GatewayUserKeyListItem {
  id: string;
  key_prefix: string;
  revoked_at: string | null;
  last_used_at: string | null;
  created_at: string;
}

export interface CreateGatewayBackendRequest {
  provider: string;
  slug?: string;
  display_name?: string;
  backend_type?: string;
  base_url?: string;
  key?: string;
  enabled?: boolean;
  set_default?: boolean;
  metadata?: Record<string, unknown>;
}

export interface UpdateGatewayBackendRequest {
  display_name?: string;
  base_url?: string;
  key?: string;
  enabled?: boolean;
  metadata?: Record<string, unknown>;
}

export interface DeleteGatewayBackendResponse {
  deleted: boolean;
}

export interface GatewayAuditLogItem {
  id: string;
  actor_user_id: string;
  actor_name: string;
  actor_email: string;
  action: string;
  target_type: string;
  target_id: string;
  before_state: Record<string, unknown> | null;
  after_state: Record<string, unknown> | null;
  request_id: string;
  created_at: string;
}

export interface GatewayPolicyDecisionItem {
  id: string;
  policy_id: string;
  policy_version: number | null;
  subject_user_id: string;
  subject_agent_id: string;
  resource_type: string;
  resource_id: string;
  resource_label: string;
  decision: string;
  reason_code: string;
  matched_rules: unknown;
  request_id: string;
  session_id: string;
  span_row_id: string;
  approval_status: string;
  evidence_references: unknown;
  created_at: string;
}

export interface GatewayPolicyDecisionApprovalRequest {
  reason?: string;
  expires_at?: string;
}

export interface GatewayPolicyDecisionApprovalResponse {
  decision: GatewayPolicyDecisionItem;
  exception?: GatewayPolicyExceptionItem;
}

export interface GatewayEvidenceItem {
  id: string;
  evidence_type: string;
  framework_refs: unknown;
  linked_request_id: string;
  linked_session_id: string;
  linked_span_row_id: string;
  linked_policy_id: string;
  linked_backend_id: string;
  linked_provider_risk_id: string;
  summary: string;
  payload: unknown;
  attachment_ref: string;
  generated_at: string;
  retain_until: string | null;
}

export interface GatewayControlMappingItem {
  id: string;
  framework: string;
  control_id: string;
  control_title: string;
  mapped_policy_ids: unknown;
  mapped_evidence_queries: unknown;
  status: string;
  owner_user_id: string;
  evidence_count: number;
  last_evidence_generated_at: string;
  updated_at: string;
}

export interface GatewayIncidentItem {
  id: string;
  severity: string;
  category: string;
  linked_request_id: string;
  linked_session_id: string;
  linked_span_row_id: string;
  linked_policy_id: string;
  linked_provider_risk_id: string;
  summary: string;
  status: string;
  remediation_notes: string;
  opened_at: string;
  closed_at: string | null;
}

export interface GatewayPolicyExceptionItem {
  id: string;
  policy_id: string;
  requester_user_id: string;
  approver_user_id: string;
  reason: string;
  scope: unknown;
  status: string;
  expires_at: string | null;
  evidence_references: unknown;
  created_at: string;
  updated_at: string;
}

export interface GatewayGovernancePolicyItem {
  id: string;
  name: string;
  description: string;
  policy_type: string;
  enabled: boolean;
  version: number;
  rule_definition: unknown;
  enforcement_mode: string;
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
}

export interface CreateGatewayGovernancePolicyRequest {
  name: string;
  description?: string;
  policy_type: string;
  enabled: boolean;
  rule_definition: unknown;
  enforcement_mode: string;
}

export interface UpdateGatewayGovernancePolicyRequest extends CreateGatewayGovernancePolicyRequest {}

export interface CreateGatewayPolicyExceptionRequest {
  reason: string;
  resource_type: string;
  resource_id?: string;
  resource_label?: string;
  expires_at?: string;
}

export interface UpdateGatewayPolicyExceptionRequest {
  status: string;
  expires_at?: string;
}

export interface UpdateGatewayIncidentRequest {
  status: string;
  remediation_notes?: string;
}

export interface GatewayProviderRisk {
  id: string;
  backend_id: string;
  provider_name: string;
  owner_user_id: string;
  approved_use_cases: string[];
  data_categories: string[];
  regions: string[];
  hosting_notes: string;
  contract_status: string;
  security_review_status: string;
  evidence_links: string[];
  limitations: string;
  prohibited_uses: string;
  model_list: string[];
  capability_class: string;
  risk_score: number;
  review_cadence_days: number;
  last_assessment_at: string | null;
  next_review_at: string | null;
  active_exception_count: number;
  created_at: string;
  updated_at: string;
}

export interface UpsertGatewayProviderRiskRequest {
  provider_name: string;
  backend_id?: string;
  approved_use_cases?: string[];
  data_categories?: string[];
  regions?: string[];
  hosting_notes?: string;
  contract_status?: string;
  security_review_status?: string;
  evidence_links?: string[];
  limitations?: string;
  prohibited_uses?: string;
  model_list?: string[];
  capability_class?: string;
  risk_score?: number;
  review_cadence_days?: number;
  last_assessment_at?: string;
  next_review_at?: string;
  active_exception_count?: number;
}

export interface GatewayIngestKeyListItem {
  id: string;
  key_prefix: string;
  app_id: string;
  display_name: string;
  revoked_at: string | null;
  last_used_at: string | null;
  created_at: string;
}

export interface GatewayIngestKeyResponse extends GatewayIngestKeyListItem {
  key: string;
  gateway_base_url: string;
}

export interface CreateGatewayIngestKeyRequest {
  app_id?: string;
  display_name?: string;
}

export interface GatewayOverviewResponse {
  since: string;
  until: string;
  bucket_width: "hour" | "day" | string;
  summary: GatewayOverviewSummary;
  time_series: GatewayOverviewBucket[];
  top_models: GatewayModelUsage[];
  top_backends: GatewayBackendUsage[];
}

export interface GatewayOverviewSummary {
  session_count: number;
  request_count: number;
  llm_call_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  total_cost: number | null;
  error_count: number;
  streaming_request_count: number;
  avg_latency_ms: number;
}

export interface GatewayOverviewBucket {
  bucket_start: string;
  request_count: number;
  error_count: number;
  total_tokens: number;
  total_cost: number | null;
}

export interface GatewayModelUsage {
  model: string;
  call_count: number;
  total_tokens: number;
  total_cost: number | null;
}

export interface GatewayBackendUsage {
  backend: string;
  call_count: number;
  error_count: number;
  avg_latency_ms: number;
  total_tokens: number;
  total_cost: number | null;
}

export interface GatewaySessionListResponse {
  sessions: GatewaySessionListItem[];
  total: number;
  limit: number;
  since: string;
}

export interface GatewaySessionListItem {
  id: string;
  trace_id: string;
  root_span_id: string;
  name: string;
  client_protocol: string;
  client_tool_hint: string;
  service_name: string;
  tags: unknown;
  status: string;
  started_at: string;
  ended_at: string | null;
  duration_ms: number | null;
  span_count: number;
  error_count: number;
  total_cost: number | null;
  resource_attributes: Record<string, unknown>;
  request_count: number;
  llm_call_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  usage_cost: number | null;
  request_error_count: number;
  streaming_request_count: number;
  avg_latency_ms: number;
  models: string[];
  backends: string[];
}

export interface GatewaySessionDetail extends Omit<GatewaySessionListItem, "request_count" | "llm_call_count" | "prompt_tokens" | "completion_tokens" | "total_tokens" | "usage_cost" | "request_error_count" | "streaming_request_count" | "avg_latency_ms" | "models" | "backends"> {
  requests: GatewayRequestObservation[];
  model_calls: GatewayModelCallObservation[];
  events: GatewayEventObservation[];
  logs: GatewayLogObservation[];
  agents: GatewayAgentObservation[];
  tools: GatewayToolObservation[];
}

export interface GatewayRequestObservation {
  id: string;
  session_id: string;
  backend_id: string;
  route: string;
  method: string;
  model_requested: string;
  model_forwarded: string;
  provider_slug: string;
  streaming: boolean;
  status: string;
  http_status: number | null;
  latency_ms: number | null;
  error_type: string;
  error_message: string;
  capture_policy: string;
  request_metadata: Record<string, unknown>;
  response_metadata: Record<string, unknown>;
  created_at: string;
  completed_at: string | null;
}

export interface GatewayModelCallObservation {
  id: string;
  request_id: string;
  session_id: string;
  backend_id: string;
  provider_slug: string;
  request_model: string;
  response_model: string;
  request_type: string;
  streaming: boolean;
  prompt_messages: unknown;
  completion_messages: unknown;
  completion_chunks: unknown;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  cache_creation_input_tokens: number;
  cache_read_input_tokens: number;
  reasoning_tokens: number;
  streaming_tokens: number;
  usage_source: string;
  prompt_cost: number | null;
  completion_cost: number | null;
  total_cost: number | null;
  response_id: string;
  finish_reason: string;
  stop_reason: string;
  time_to_first_token_ms: number | null;
  time_to_generate_ms: number | null;
  streaming_duration_ms: number | null;
  streaming_chunk_count: number;
  created_at: string;
}

export interface GatewayEventObservation {
  id: string;
  session_id: string;
  request_id: string;
  span_id: string;
  event_type: string;
  payload: unknown;
  occurred_at: string;
}

export interface GatewayLogObservation {
  id: string;
  session_id: string;
  request_id: string;
  span_id: string;
  severity: string;
  body: string;
  attributes: Record<string, unknown>;
  occurred_at: string;
}

export interface GatewayAgentObservation {
  id: string;
  session_id: string;
  span_row_id: string;
  agent_id: string;
  agent_name: string;
  role: string;
  models: unknown;
  tools: unknown;
  handoff_source: string;
  handoff_destination: string;
  reasoning_summary: string;
  created_at: string;
}

export interface GatewayToolObservation {
  id: string;
  session_id: string;
  span_row_id: string;
  tool_id: string;
  tool_name: string;
  canonical_tool_type: string;
  tool_risk_level: string;
  description: string;
  parameters: unknown;
  result: unknown;
  status: string;
  duration_ms: number | null;
  created_at: string;
}

export interface GatewaySessionSpansResponse {
  session_id: string;
  trace_id: string;
  spans: GatewaySpanObservation[];
}

export interface GatewaySpanObservation {
  id: string;
  session_id: string;
  request_id: string;
  trace_id: string;
  span_id: string;
  parent_span_id: string;
  name: string;
  span_name: string;
  span_kind: string;
  span_type: string;
  service_name: string;
  start_time: string;
  end_time: string | null;
  duration: number;
  duration_ms: number | null;
  status_code: string;
  status_message: string;
  attributes: Record<string, unknown>;
  resource_attributes: Record<string, unknown>;
}

export interface GatewayLLMCallListResponse {
  calls: GatewayLLMCallListItem[];
  total: number;
  limit: number;
  since: string;
}

export interface GatewayLLMCallListItem extends GatewayModelCallObservation {
  trace_id: string;
  session_name: string;
  route: string;
  request_status: string;
  http_status: number | null;
  latency_ms: number | null;
  capture_policy: string;
}
