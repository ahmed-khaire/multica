"use client";

import { Fragment, useEffect, useMemo, useState } from "react";
import type React from "react";
import {
  Activity,
  AlertTriangle,
  Bot,
  Boxes,
  Check,
  CircleDollarSign,
  Copy,
  DatabaseZap,
  Eye,
  Gauge,
  History,
  KeyRound,
  LockKeyhole,
  MessageSquareText,
  Pencil,
  RefreshCw,
  Route,
  Save,
  Server,
  ShieldCheck,
  TerminalSquare,
  Trash2,
  Wrench,
  X,
} from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  CreateGatewayBackendRequest,
  CreateGatewayBackendCredentialRequest,
  CreateGatewayGovernancePolicyRequest,
  GatewayAuditLogItem,
  GatewayBackend,
  GatewayBackendCredential,
  GatewayCapturePolicy,
  GatewayControlMappingItem,
  GatewayEvidenceItem,
  GatewayExportResponse,
  GatewayHealthReportResponse,
  GatewayIncidentItem,
  GatewayIngestKeyListItem,
  GatewayIngestKeyResponse,
  GatewayGovernanceActionItem,
  GatewayGovernanceInsightsResponse,
  GatewayGovernancePolicyItem,
  GatewayStatusResponse,
  GatewayUserKeyResponse,
  GatewayBackendUsage,
  GatewayOverviewResponse,
  GatewayLLMCallListItem,
  GatewayModelCallObservation,
  GatewayModelUsage,
  GatewayOverviewBucket,
  GatewayPolicyDecisionItem,
  GatewayPolicyExceptionItem,
  GatewaySessionDetail,
  GatewaySessionListItem,
  GatewaySpanObservation,
  GatewayProviderRisk,
  UpsertGatewayProviderRiskRequest,
  UpdateGatewayBackendRequest,
  UpdateGatewayBackendCredentialRequest,
} from "@multica/core/types";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";
import {
  gatewayKeys,
  gatewayAuditOptions,
  gatewayBackendCredentialsOptions,
  gatewayBackendsOptions,
  gatewayControlMappingsOptions,
  gatewayEvidenceOptions,
  gatewayHealthReportOptions,
  gatewayGovernanceInsightsOptions,
  gatewayIncidentsOptions,
  gatewayIngestKeysOptions,
  gatewayLLMCallsOptions,
  gatewayOverviewOptions,
  gatewayGovernancePoliciesOptions,
  gatewayPolicyDecisionsOptions,
  gatewayPolicyExceptionsOptions,
  gatewayProviderRisksOptions,
  gatewaySessionDetailOptions,
  gatewaySessionSpansOptions,
  gatewaySessionsOptions,
  gatewayStatusOptions,
} from "@multica/core/gateway/queries";
import { memberListOptions } from "@multica/core/workspace/queries";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@multica/ui/components/ui/native-select";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Textarea } from "@multica/ui/components/ui/textarea";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@multica/ui/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { cn } from "@multica/ui/lib/utils";

type WindowOption = {
  label: string;
  value: string;
};

const windowOptions: WindowOption[] = [
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
  { label: "30d", value: "30d" },
];

const gatewayProviderPresets = [
  { label: "OpenAI", value: "openai", baseUrl: "https://api.openai.com/v1" },
  { label: "Groq", value: "groq", baseUrl: "https://api.groq.com/openai/v1" },
  { label: "OpenRouter", value: "openrouter", baseUrl: "https://openrouter.ai/api/v1" },
  { label: "Local", value: "local", baseUrl: "http://127.0.0.1:11434/v1" },
  { label: "Anthropic", value: "anthropic", baseUrl: "https://api.anthropic.com" },
  { label: "Claude OAuth", value: "claude-oauth", baseUrl: "claude-oauth://sidecar" },
];

const capturePolicies: GatewayCapturePolicy[] = ["metadata_only", "redacted_content", "full_content"];

const governanceStatuses = ["unknown", "not_started", "in_review", "approved", "rejected", "expired"];
const gatewayPolicyTypes = ["provider", "model", "tool", "data", "budget", "approval", "routing", "capture"];
const gatewayPolicyModes = ["enforce", "monitor"];
const defaultGatewayPolicyRule = JSON.stringify(
  {
    rules: [
      {
        id: "block-gpt-test",
        action: "block",
        reason_code: "model_blocked",
        message: "This model is blocked by workspace policy.",
        match: {
          models: ["gpt-test"],
        },
      },
    ],
  },
  null,
  2,
);

function formatCount(value: number | null | undefined): string {
  if (!value) return "0";
  return new Intl.NumberFormat(undefined, {
    notation: value >= 10000 ? "compact" : "standard",
    maximumFractionDigits: 1,
  }).format(value);
}

function formatMoney(value: number | null | undefined): string {
  if (value == null) return "$0.0000";
  return `$${value.toFixed(4)}`;
}

function formatMS(value: number | null | undefined): string {
  if (value == null) return "0ms";
  if (value >= 1000) return `${(value / 1000).toFixed(1)}s`;
  return `${Math.round(value)}ms`;
}

function formatTime(value: string | null | undefined): string {
  if (!value) return "";
  return new Date(value).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatNullableTime(value: string | null | undefined): string {
  return value ? formatTime(value) : "Never";
}

function formatDuration(value: number | null | undefined): string {
  if (value == null) return "running";
  if (value >= 60000) return `${Math.round(value / 60000)}m`;
  if (value >= 1000) return `${Math.round(value / 1000)}s`;
  return `${value}ms`;
}

function statusVariant(status: string): "secondary" | "destructive" | "outline" {
  if (status === "success" || status === "ok") return "secondary";
  if (status.includes("error") || status === "policy_blocked" || status === "client_cancelled") {
    return "destructive";
  }
  return "outline";
}

function doctorStatusVariant(status: string): "secondary" | "destructive" | "outline" {
  if (status === "healthy" || status === "pass") return "secondary";
  if (status === "unhealthy" || status === "fail") return "destructive";
  return "outline";
}

function actionSeverityVariant(severity: string): "secondary" | "destructive" | "outline" {
  if (severity === "critical" || severity === "high") return "destructive";
  if (severity === "medium" || severity === "warning") return "secondary";
  return "outline";
}

function StatCard({
  label,
  value,
  sub,
  icon: Icon,
}: {
  label: string;
  value: string;
  sub: string;
  icon: React.ComponentType<{ className?: string }>;
}) {
  return (
    <Card size="sm" className="rounded-lg">
      <CardContent className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-xs text-muted-foreground">{label}</p>
          <p className="mt-1 text-2xl font-semibold tracking-normal tabular-nums">{value}</p>
          <p className="mt-1 text-xs text-muted-foreground">{sub}</p>
        </div>
        <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
          <Icon className="size-4" />
        </span>
      </CardContent>
    </Card>
  );
}

function GovernanceInsightMetric({
  label,
  value,
  detail,
  icon: Icon,
  variant = "outline",
}: {
  label: string;
  value: string;
  detail: string;
  icon: React.ComponentType<{ className?: string }>;
  variant?: "destructive" | "outline" | "secondary";
}) {
  return (
    <Card
      size="sm"
      className={cn(
        "rounded-lg",
        variant === "destructive" && "border-destructive/30 bg-destructive/5",
        variant === "secondary" && "bg-muted/30",
      )}
    >
      <CardContent className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-xs text-muted-foreground">{label}</p>
          <p className="mt-1 text-2xl font-semibold tracking-normal tabular-nums">{value}</p>
          <p className="mt-1 truncate text-xs text-muted-foreground">{detail}</p>
        </div>
        <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
          <Icon className="size-4" />
        </span>
      </CardContent>
    </Card>
  );
}

function BreakdownRow({
  label,
  value,
  detail,
  max,
}: {
  label: string;
  value: number;
  detail: string;
  max: number;
}) {
  const width = max > 0 ? Math.max(4, Math.round((value / max) * 100)) : 0;
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="truncate font-medium">{label}</span>
        <span className="shrink-0 text-muted-foreground tabular-nums">{detail}</span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-muted">
        <div className="h-full rounded-full bg-foreground/70" style={{ width: `${width}%` }} />
      </div>
    </div>
  );
}

function GovernanceActionQueue({ actions }: { actions: GatewayGovernanceActionItem[] }) {
  if (actions.length === 0) {
    return (
      <div className="flex h-56 flex-col items-center justify-center rounded-md border text-center">
        <ShieldCheck className="size-8 text-muted-foreground/40" />
        <p className="mt-3 text-sm font-medium">No governance actions queued</p>
        <p className="mt-1 text-xs text-muted-foreground">Incidents, provider reviews, control gaps, and expiring exceptions appear here.</p>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {actions.slice(0, 8).map((action) => (
        <div key={`${action.kind}-${action.resource_id}-${action.created_at}`} className="rounded-md border p-3">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="truncate text-sm font-medium">{action.title}</p>
              <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{action.detail}</p>
            </div>
            <Badge variant={actionSeverityVariant(action.severity)}>{action.severity}</Badge>
          </div>
          <div className="mt-3 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <Badge variant="outline">{action.kind}</Badge>
            <span>{action.resource_type}</span>
            <span className="truncate">{action.resource_id}</span>
            <span className="ml-auto shrink-0">{formatTime(action.created_at)}</span>
          </div>
        </div>
      ))}
    </div>
  );
}

function TrendStrip({ buckets }: { buckets: GatewayOverviewBucket[] }) {
  const max = Math.max(...buckets.map((b) => b.request_count), 1);
  if (buckets.length === 0) {
    return <div className="flex h-24 items-center justify-center text-xs text-muted-foreground">No Gateway traffic in this window</div>;
  }
  return (
    <div className="flex h-24 items-end gap-1" aria-label="Gateway request trend">
      {buckets.map((bucket) => (
        <div key={bucket.bucket_start} className="flex min-w-5 flex-1 flex-col items-center gap-1">
          <div
            className={cn(
              "w-full rounded-t-sm bg-muted-foreground/35",
              bucket.error_count > 0 && "bg-destructive/60",
            )}
            title={`${formatTime(bucket.bucket_start)}: ${bucket.request_count} requests`}
            style={{ height: `${Math.max(8, (bucket.request_count / max) * 80)}px` }}
          />
        </div>
      ))}
    </div>
  );
}

function OverviewSection({
  overview,
  loading,
}: {
  overview: GatewayOverviewResponse | undefined;
  loading: boolean;
}) {
  if (loading && !overview) {
    return (
      <div className="grid gap-3 md:grid-cols-4">
        {Array.from({ length: 4 }).map((_, index) => (
          <Skeleton key={index} className="h-28 rounded-lg" />
        ))}
      </div>
    );
  }
  if (!overview) return null;

  const maxModelCalls = Math.max(...overview.top_models.map((m) => m.call_count), 1);
  const maxBackendCalls = Math.max(...overview.top_backends.map((b) => b.call_count), 1);

  return (
    <div className="space-y-3">
      <div className="grid gap-3 md:grid-cols-4">
        <StatCard
          label="Sessions"
          value={`${formatCount(overview.summary.session_count)} sessions`}
          sub={`${formatCount(overview.summary.request_count)} requests`}
          icon={Activity}
        />
        <StatCard
          label="Tokens"
          value={`${formatCount(overview.summary.total_tokens)} tokens`}
          sub={`${formatCount(overview.summary.llm_call_count)} model calls`}
          icon={DatabaseZap}
        />
        <StatCard
          label="Cost"
          value={formatMoney(overview.summary.total_cost)}
          sub={`${formatCount(overview.summary.streaming_request_count)} streaming requests`}
          icon={CircleDollarSign}
        />
        <StatCard
          label="Reliability"
          value={`${formatCount(overview.summary.error_count)} errors`}
          sub={`${formatMS(overview.summary.avg_latency_ms)} average latency`}
          icon={Gauge}
        />
      </div>

      <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_280px_280px]">
        <Card size="sm" className="rounded-lg">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <Route className="size-4 text-muted-foreground" />
              Request Volume
            </CardTitle>
          </CardHeader>
          <CardContent>
            <TrendStrip buckets={overview.time_series} />
          </CardContent>
        </Card>

        <Card size="sm" className="rounded-lg">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <MessageSquareText className="size-4 text-muted-foreground" />
              Top Models
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {overview.top_models.length === 0 ? (
              <p className="text-xs text-muted-foreground">No model calls yet</p>
            ) : (
              overview.top_models.slice(0, 4).map((model: GatewayModelUsage) => (
                <BreakdownRow
                  key={model.model}
                  label={model.model}
                  value={model.call_count}
                  max={maxModelCalls}
                  detail={`${formatCount(model.total_tokens)} tokens`}
                />
              ))
            )}
          </CardContent>
        </Card>

        <Card size="sm" className="rounded-lg">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <Server className="size-4 text-muted-foreground" />
              Top Backends
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {overview.top_backends.length === 0 ? (
              <p className="text-xs text-muted-foreground">No backend usage yet</p>
            ) : (
              overview.top_backends.slice(0, 4).map((backend: GatewayBackendUsage) => (
                <BreakdownRow
                  key={backend.backend}
                  label={backend.backend}
                  value={backend.call_count}
                  max={maxBackendCalls}
                  detail={`${formatMS(backend.avg_latency_ms)} avg`}
                />
              ))
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function SessionsTable({
  sessions,
  selectedId,
  onSelect,
}: {
  sessions: GatewaySessionListItem[];
  selectedId: string;
  onSelect: (id: string) => void;
}) {
  if (sessions.length === 0) {
    return (
      <div className="flex h-56 flex-col items-center justify-center border-t text-center">
        <Eye className="size-8 text-muted-foreground/40" />
        <p className="mt-3 text-sm font-medium">No sessions captured</p>
        <p className="mt-1 max-w-sm text-xs text-muted-foreground">
          Use the generated Gateway base URL and key from `multica gateway key` to start observing agent traffic.
        </p>
      </div>
    );
  }

  return (
    <Table aria-label="Gateway sessions">
      <TableHeader>
        <TableRow>
          <TableHead>Session</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Backend</TableHead>
          <TableHead>Model</TableHead>
          <TableHead className="text-right">Calls</TableHead>
          <TableHead className="text-right">Tokens</TableHead>
          <TableHead className="text-right">Latency</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {sessions.map((session) => (
          <TableRow key={session.id} data-state={selectedId === session.id ? "selected" : undefined}>
            <TableCell>
              <button
                type="button"
                onClick={() => onSelect(session.id)}
                className="flex max-w-72 flex-col text-left"
              >
                <span className="truncate font-medium">{session.name}</span>
                <span className="truncate text-xs text-muted-foreground">{formatTime(session.started_at)} · {session.trace_id}</span>
              </button>
            </TableCell>
            <TableCell>
              <Badge variant={statusVariant(session.status)}>{session.status}</Badge>
            </TableCell>
            <TableCell>{session.backends[0] ?? "unknown"}</TableCell>
            <TableCell>{session.models[0] ?? "unknown"}</TableCell>
            <TableCell className="text-right tabular-nums">{session.llm_call_count}</TableCell>
            <TableCell className="text-right tabular-nums">{formatCount(session.total_tokens)}</TableCell>
            <TableCell className="text-right tabular-nums">{formatMS(session.avg_latency_ms)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function LLMCallsTable({
  calls,
  onSelectSession,
}: {
  calls: GatewayLLMCallListItem[];
  onSelectSession: (id: string) => void;
}) {
  if (calls.length === 0) {
    return (
      <div className="flex h-56 flex-col items-center justify-center border-t text-center">
        <MessageSquareText className="size-8 text-muted-foreground/40" />
        <p className="mt-3 text-sm font-medium">No LLM calls captured</p>
        <p className="mt-1 text-xs text-muted-foreground">Captured calls appear here according to workspace policy.</p>
      </div>
    );
  }

  return (
    <Table aria-label="LLM calls">
      <TableHeader>
        <TableRow>
          <TableHead>Model</TableHead>
          <TableHead>Route</TableHead>
          <TableHead>Backend</TableHead>
          <TableHead>Capture</TableHead>
          <TableHead className="text-right">Tokens</TableHead>
          <TableHead className="text-right">Latency</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {calls.map((call) => (
          <TableRow key={call.id}>
            <TableCell>
              <button
                type="button"
                onClick={() => onSelectSession(call.session_id)}
                className="flex max-w-72 flex-col text-left"
              >
                <span className="truncate font-medium">{call.response_model || call.request_model}</span>
                <span className="truncate text-xs text-muted-foreground">{call.session_name}</span>
              </button>
            </TableCell>
            <TableCell>{call.route}</TableCell>
            <TableCell>{call.provider_slug}</TableCell>
            <TableCell>
              <Badge variant="outline">{call.capture_policy}</Badge>
            </TableCell>
            <TableCell className="text-right tabular-nums">{formatCount(call.total_tokens)}</TableCell>
            <TableCell className="text-right tabular-nums">{formatMS(call.latency_ms)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function GatewayValue({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 rounded-md border p-3">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="mt-1 truncate text-sm font-medium">{value || "Not configured"}</p>
    </div>
  );
}

function gatewayBackendLabel(backend: GatewayBackend): string {
  return backend.display_name || backend.slug;
}

function canManageGateway(role: string | undefined): boolean {
  return role === "owner" || role === "admin";
}

function gatewayUserKeyEnv(key: GatewayUserKeyResponse): string {
  return [
    `OPENAI_BASE_URL=${key.openai_base_url}`,
    `OPENAI_API_KEY=${key.openai_api_key}`,
    `ANTHROPIC_BASE_URL=${key.anthropic_base_url}`,
    `ANTHROPIC_API_KEY=${key.anthropic_api_key}`,
  ].join("\n");
}

function commaList(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function joinList(values: string[] | undefined): string {
  return (values ?? []).join(", ");
}

function policyExceptionScopeLabel(scope: unknown): string {
  if (!scope || typeof scope !== "object" || Array.isArray(scope)) return "workspace";
  const typed = scope as Record<string, unknown>;
  const resource = typeof typed.resource_label === "string"
    ? typed.resource_label
    : typeof typed.resource_id === "string"
      ? typed.resource_id
      : "workspace";
  const resourceType = typeof typed.resource_type === "string" ? typed.resource_type : "resource";
  return `${resourceType}: ${resource}`;
}

function gatewayExportSummary(exportData: GatewayExportResponse): string {
  const generatedDate = new Date(exportData.generated_at).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  });
  return `Export generated ${generatedDate}: ${formatCount(exportData.overview.summary.session_count)} sessions, ${formatCount(exportData.overview.summary.llm_call_count)} LLM calls`;
}

function credentialRateStatus(credential: GatewayBackendCredential): {
  label: string;
  variant: "secondary" | "destructive" | "outline";
} {
  if (credential.rate_limited_until) return { label: "rate limited", variant: "destructive" };
  if (!credential.enabled) return { label: "disabled", variant: "outline" };
  return { label: "ready", variant: "secondary" };
}

function GatewayCredentialPool({
  backend,
  canManage,
  colSpan,
  wsId,
}: {
  backend: GatewayBackend;
  canManage: boolean;
  colSpan: number;
  wsId: string;
}) {
  const qc = useQueryClient();
  const label = gatewayBackendLabel(backend);
  const credentialsQuery = useQuery(gatewayBackendCredentialsOptions(wsId, backend.id));
  const credentials = credentialsQuery.data ?? [];
  const [credentialLabel, setCredentialLabel] = useState("");
  const [credentialPriority, setCredentialPriority] = useState("100");
  const [credentialKey, setCredentialKey] = useState("");

  const invalidateCredentials = () => {
    void qc.invalidateQueries({ queryKey: gatewayKeys.backendCredentials(wsId, backend.id) });
  };

  const addCredentialMutation = useMutation({
    mutationFn: (input: CreateGatewayBackendCredentialRequest) =>
      api.createGatewayBackendCredential(backend.id, input),
    onSuccess: () => {
      setCredentialLabel("");
      setCredentialPriority("100");
      setCredentialKey("");
      invalidateCredentials();
    },
  });

  const updateCredentialMutation = useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpdateGatewayBackendCredentialRequest }) =>
      api.updateGatewayBackendCredential(backend.id, id, input),
    onSuccess: invalidateCredentials,
  });

  const submitCredential = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const priority = Number.parseInt(credentialPriority, 10);
    addCredentialMutation.mutate({
      label: credentialLabel.trim(),
      key: credentialKey.trim(),
      priority: Number.isFinite(priority) ? priority : 100,
      enabled: true,
    });
  };

  const toggleCredential = (credential: GatewayBackendCredential) => {
    updateCredentialMutation.mutate({
      id: credential.id,
      input: { enabled: !credential.enabled },
    });
  };

  if (!canManage) return null;

  return (
    <TableRow>
      <TableCell colSpan={colSpan} className="bg-muted/20 p-0">
        <div className="space-y-3 border-t p-3">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <p className="text-sm font-medium">Credential pool</p>
              <p className="text-xs text-muted-foreground">
                Route traffic across enterprise-managed keys without showing raw provider secrets.
              </p>
            </div>
            <form className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_96px_minmax(0,1.2fr)_auto]" onSubmit={submitCredential}>
              <div className="space-y-1">
                <Label htmlFor={`gateway-credential-label-${backend.id}`} className="sr-only">
                  Credential label for {label}
                </Label>
                <Input
                  id={`gateway-credential-label-${backend.id}`}
                  aria-label={`Credential label for ${label}`}
                  value={credentialLabel}
                  onChange={(event) => setCredentialLabel(event.target.value)}
                  placeholder="Label"
                  autoComplete="off"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor={`gateway-credential-priority-${backend.id}`} className="sr-only">
                  Credential priority for {label}
                </Label>
                <Input
                  id={`gateway-credential-priority-${backend.id}`}
                  aria-label={`Credential priority for ${label}`}
                  value={credentialPriority}
                  onChange={(event) => setCredentialPriority(event.target.value)}
                  inputMode="numeric"
                  autoComplete="off"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor={`gateway-credential-key-${backend.id}`} className="sr-only">
                  Credential API key for {label}
                </Label>
                <Input
                  id={`gateway-credential-key-${backend.id}`}
                  aria-label={`Credential API key for ${label}`}
                  value={credentialKey}
                  onChange={(event) => setCredentialKey(event.target.value)}
                  placeholder="Provider API key"
                  autoComplete="off"
                  type="password"
                />
              </div>
              <Button
                type="submit"
                size="sm"
                disabled={addCredentialMutation.isPending || !credentialKey.trim()}
                aria-label={`Add credential for ${label}`}
              >
                <KeyRound className="size-3.5" />
                Add
              </Button>
            </form>
          </div>
          {credentialsQuery.isLoading ? (
            <div className="space-y-2">
              {Array.from({ length: 2 }).map((_, index) => (
                <Skeleton key={index} className="h-10 rounded-md" />
              ))}
            </div>
          ) : credentials.length === 0 ? (
            <div className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">
              No additional credentials. This backend uses its primary configured key.
            </div>
          ) : (
            <Table aria-label={`${label} credential pool`}>
              <TableHeader>
                <TableRow>
                  <TableHead>Credential</TableHead>
                  <TableHead>Priority</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Rate limit</TableHead>
                  <TableHead>Last used</TableHead>
                  <TableHead>Last error</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {credentials.map((credential) => {
                  const status = credentialRateStatus(credential);
                  return (
                    <TableRow key={credential.id}>
                      <TableCell>
                        <div className="flex max-w-64 flex-col">
                          <span className="truncate font-medium">{credential.label || "Unlabeled key"}</span>
                          <span className="truncate text-xs text-muted-foreground">{credential.credential_hint}</span>
                        </div>
                      </TableCell>
                      <TableCell className="tabular-nums">{credential.priority}</TableCell>
                      <TableCell>
                        <Badge variant={status.variant}>{status.label}</Badge>
                      </TableCell>
                      <TableCell>
                        <span className="text-sm tabular-nums">
                          {credential.rate_limit_remaining == null ? "unknown" : credential.rate_limit_remaining}
                        </span>
                        <span className="block text-xs text-muted-foreground">
                          reset {formatNullableTime(credential.rate_limit_reset_at)}
                        </span>
                      </TableCell>
                      <TableCell>{formatNullableTime(credential.last_used_at)}</TableCell>
                      <TableCell>
                        <span className="line-clamp-2 max-w-64 text-sm text-muted-foreground">
                          {credential.last_error || "None"}
                        </span>
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          type="button"
                          size="xs"
                          variant="outline"
                          disabled={updateCredentialMutation.isPending && updateCredentialMutation.variables?.id === credential.id}
                          onClick={() => toggleCredential(credential)}
                          aria-label={`${credential.enabled ? "Disable" : "Enable"} ${credential.label || "credential"}`}
                        >
                          {credential.enabled ? "Disable" : "Enable"}
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
          {credentialsQuery.error instanceof Error ? (
            <p className="text-xs text-destructive">{credentialsQuery.error.message}</p>
          ) : null}
          {addCredentialMutation.error instanceof Error ? (
            <p className="text-xs text-destructive">{addCredentialMutation.error.message}</p>
          ) : null}
          {updateCredentialMutation.error instanceof Error ? (
            <p className="text-xs text-destructive">{updateCredentialMutation.error.message}</p>
          ) : null}
        </div>
      </TableCell>
    </TableRow>
  );
}

function GatewayBackendsTable({
  backends,
  canManage,
  editBaseUrl,
  editEnabled,
  editKey,
  editingBackendId,
  editName,
  onCancelEdit,
  onDeleteBackend,
  onSaveEdit,
  onSetDefault,
  onStartEdit,
  pendingDeleteId,
  pendingDefaultSlug,
  pendingEditId,
  setEditBaseUrl,
  setEditEnabled,
  setEditKey,
  setEditName,
  wsId,
}: {
  backends: GatewayBackend[];
  canManage: boolean;
  editBaseUrl: string;
  editEnabled: boolean;
  editKey: string;
  editingBackendId: string;
  editName: string;
  onCancelEdit: () => void;
  onDeleteBackend: (backend: GatewayBackend) => void;
  onSaveEdit: (backend: GatewayBackend) => void;
  onSetDefault: (slug: string) => void;
  onStartEdit: (backend: GatewayBackend) => void;
  pendingDeleteId: string;
  pendingDefaultSlug: string;
  pendingEditId: string;
  setEditBaseUrl: (value: string) => void;
  setEditEnabled: (value: boolean) => void;
  setEditKey: (value: string) => void;
  setEditName: (value: string) => void;
  wsId: string;
}) {
  const [credentialBackendId, setCredentialBackendId] = useState("");

  if (backends.length === 0) {
    return (
      <div className="flex h-40 flex-col items-center justify-center border-t text-center">
        <Server className="size-8 text-muted-foreground/40" />
        <p className="mt-3 text-sm font-medium">No Gateway backends</p>
        <p className="mt-1 text-xs text-muted-foreground">Add a managed backend before routing user agent traffic.</p>
      </div>
    );
  }

  return (
    <Table aria-label="Gateway backends">
      <TableHeader>
        <TableRow>
          <TableHead>Backend</TableHead>
          <TableHead>Type</TableHead>
          <TableHead>Base URL</TableHead>
          <TableHead>Key</TableHead>
          <TableHead>Status</TableHead>
          <TableHead className="text-right">Routing</TableHead>
          {canManage ? <TableHead className="text-right">Actions</TableHead> : null}
        </TableRow>
      </TableHeader>
      <TableBody>
        {backends.map((backend) => {
          const label = gatewayBackendLabel(backend);
          const isEditing = editingBackendId === backend.id;
          const editNameId = `gateway-edit-name-${backend.id}`;
          const editBaseUrlId = `gateway-edit-base-url-${backend.id}`;
          const editKeyId = `gateway-edit-key-${backend.id}`;
          const credentialsExpanded = credentialBackendId === backend.id;
          const toggleCredentials = () => {
            setCredentialBackendId(credentialsExpanded ? "" : backend.id);
          };

          if (isEditing) {
            return (
              <Fragment key={backend.id}>
                <TableRow>
                  <TableCell>
                    <div className="w-56 space-y-1">
                      <Label htmlFor={editNameId} className="sr-only">
                        Edit backend name
                      </Label>
                      <Input
                        id={editNameId}
                        aria-label="Edit backend name"
                        value={editName}
                        onChange={(event) => setEditName(event.target.value)}
                        autoComplete="off"
                      />
                      <p className="truncate text-xs text-muted-foreground">{backend.slug}</p>
                    </div>
                  </TableCell>
                  <TableCell>{backend.backend_type}</TableCell>
                  <TableCell>
                    <div className="w-72">
                      <Label htmlFor={editBaseUrlId} className="sr-only">
                        Edit backend base URL
                      </Label>
                      <Input
                        id={editBaseUrlId}
                        aria-label="Edit backend base URL"
                        value={editBaseUrl}
                        onChange={(event) => setEditBaseUrl(event.target.value)}
                        autoComplete="off"
                      />
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="w-44">
                      <Label htmlFor={editKeyId} className="sr-only">
                        Rotate backend API key
                      </Label>
                      <Input
                        id={editKeyId}
                        aria-label="Rotate backend API key"
                        value={editKey}
                        onChange={(event) => setEditKey(event.target.value)}
                        placeholder="Leave unchanged"
                        autoComplete="off"
                      />
                    </div>
                  </TableCell>
                  <TableCell>
                    <Button
                      type="button"
                      size="xs"
                      variant={editEnabled ? "outline" : "secondary"}
                      onClick={() => setEditEnabled(!editEnabled)}
                    >
                      {editEnabled ? "Disable backend" : "Enable backend"}
                    </Button>
                  </TableCell>
                  <TableCell className="text-right">
                    {backend.is_default ? (
                      <Badge variant="secondary">Default</Badge>
                    ) : (
                      <Badge variant="outline">Optional</Badge>
                    )}
                  </TableCell>
                  {canManage ? (
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
                        <Button
                          type="button"
                          size="icon-xs"
                          disabled={pendingEditId === backend.id || !editBaseUrl.trim()}
                          onClick={() => onSaveEdit(backend)}
                          aria-label="Save backend changes"
                          title="Save backend changes"
                        >
                          <Save className="size-3" />
                        </Button>
                        <Button
                          type="button"
                          size="xs"
                          variant="ghost"
                          disabled={pendingEditId === backend.id}
                          onClick={onCancelEdit}
                        >
                          Cancel
                        </Button>
                      </div>
                    </TableCell>
                  ) : null}
                </TableRow>
                {canManage && credentialsExpanded ? (
                  <GatewayCredentialPool backend={backend} canManage colSpan={7} wsId={wsId} />
                ) : null}
              </Fragment>
            );
          }

          return (
            <Fragment key={backend.id}>
              <TableRow>
                <TableCell>
                  <div className="flex max-w-56 flex-col">
                    <span className="truncate font-medium">{label}</span>
                    <span className="truncate text-xs text-muted-foreground">{backend.slug}</span>
                  </div>
                </TableCell>
                <TableCell>{backend.backend_type}</TableCell>
                <TableCell>
                  <span className="block max-w-72 truncate">{backend.base_url}</span>
                </TableCell>
                <TableCell>{backend.credential_hint}</TableCell>
                <TableCell>
                  <Badge variant={backend.enabled ? "secondary" : "outline"}>
                    {backend.enabled ? "enabled" : "disabled"}
                  </Badge>
                </TableCell>
                <TableCell className="text-right">
                  {backend.is_default ? (
                    <Badge variant="secondary">Default</Badge>
                  ) : canManage ? (
                    <Button
                      type="button"
                      size="xs"
                      variant="outline"
                      disabled={pendingDefaultSlug === backend.slug}
                      onClick={() => onSetDefault(backend.slug)}
                      aria-label={`Make ${label} default`}
                    >
                      Make default
                    </Button>
                  ) : (
                    <Badge variant="outline">Optional</Badge>
                  )}
                </TableCell>
                {canManage ? (
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      <Button
                        type="button"
                        size="icon-xs"
                        variant={credentialsExpanded ? "secondary" : "ghost"}
                        disabled={Boolean(editingBackendId)}
                        onClick={toggleCredentials}
                        aria-label={`Manage credentials for ${label}`}
                        title={`Manage credentials for ${label}`}
                      >
                        <KeyRound className="size-3" />
                      </Button>
                      <Button
                        type="button"
                        size="icon-xs"
                        variant="ghost"
                        disabled={Boolean(editingBackendId)}
                        onClick={() => onStartEdit(backend)}
                        aria-label={`Edit ${label}`}
                        title={`Edit ${label}`}
                      >
                        <Pencil className="size-3" />
                      </Button>
                      <Button
                        type="button"
                        size="icon-xs"
                        variant="destructive"
                        disabled={backend.is_default || pendingDeleteId === backend.id || Boolean(editingBackendId)}
                        onClick={() => onDeleteBackend(backend)}
                        aria-label={`Delete ${label}`}
                        title={backend.is_default ? "Default backends cannot be deleted" : `Delete ${label}`}
                      >
                        <Trash2 className="size-3" />
                      </Button>
                    </div>
                  </TableCell>
                ) : null}
              </TableRow>
              {canManage && credentialsExpanded ? (
                <GatewayCredentialPool backend={backend} canManage colSpan={7} wsId={wsId} />
              ) : null}
            </Fragment>
          );
        })}
      </TableBody>
    </Table>
  );
}

function GatewayConfigurationSetup({
  canManage,
  memberRole,
  permissionsLoading,
  wsId,
}: {
  canManage: boolean;
  memberRole: string | undefined;
  permissionsLoading: boolean;
  wsId: string;
}) {
  const qc = useQueryClient();
  const statusQuery = useQuery(gatewayStatusOptions(wsId));
  const backendsQuery = useQuery(gatewayBackendsOptions(wsId));
  const [generatedKey, setGeneratedKey] = useState<GatewayUserKeyResponse | null>(null);
  const [provider, setProvider] = useState(gatewayProviderPresets[0]!.value);
  const [baseUrl, setBaseUrl] = useState(gatewayProviderPresets[0]!.baseUrl);
  const [backendKey, setBackendKey] = useState("");
  const [backendName, setBackendName] = useState("");
  const [setAsDefault, setSetAsDefault] = useState(false);
  const [editingBackendId, setEditingBackendId] = useState("");
  const [editName, setEditName] = useState("");
  const [editBaseUrl, setEditBaseUrl] = useState("");
  const [editKey, setEditKey] = useState("");
  const [editEnabled, setEditEnabled] = useState(true);
  const [exportWindow, setExportWindow] = useState("24h");
  const [lastExport, setLastExport] = useState<GatewayExportResponse | null>(null);

  const invalidateConfig = () => {
    void qc.invalidateQueries({ queryKey: gatewayKeys.status(wsId) });
    void qc.invalidateQueries({ queryKey: gatewayKeys.backends(wsId) });
  };

  const resetBackendEdit = () => {
    setEditingBackendId("");
    setEditName("");
    setEditBaseUrl("");
    setEditKey("");
    setEditEnabled(true);
  };

  const createUserKeyMutation = useMutation({
    mutationFn: () => api.createGatewayUserKey(),
    onSuccess: (key) => {
      setGeneratedKey(key);
      invalidateConfig();
    },
  });

  const createBackendMutation = useMutation({
    mutationFn: (input: CreateGatewayBackendRequest) => api.createGatewayBackend(input),
    onSuccess: () => {
      setBackendKey("");
      setBackendName("");
      setSetAsDefault(false);
      invalidateConfig();
    },
  });

  const updateBackendMutation = useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpdateGatewayBackendRequest }) =>
      api.updateGatewayBackend(id, input),
    onSuccess: () => {
      resetBackendEdit();
      invalidateConfig();
    },
  });

  const deleteBackendMutation = useMutation({
    mutationFn: (id: string) => api.deleteGatewayBackend(id),
    onSuccess: invalidateConfig,
  });

  const defaultMutation = useMutation({
    mutationFn: (slug: string) => api.setGatewayDefaultBackend(slug),
    onSuccess: invalidateConfig,
  });

  const policyMutation = useMutation({
    mutationFn: (policy: GatewayCapturePolicy) => api.updateGatewayCapturePolicy(policy),
    onSuccess: invalidateConfig,
  });

  const exportMutation = useMutation({
    mutationFn: (since: string) => api.exportGatewayData({ since, limit: 50 }),
    onSuccess: (exportData) => setLastExport(exportData),
  });

  const status = statusQuery.data as GatewayStatusResponse | undefined;
  const backends = backendsQuery.data ?? [];
  const generatedEnv = generatedKey ? gatewayUserKeyEnv(generatedKey) : "";
  const selectedProviderRequiresKey = provider !== "claude-oauth";

  const submitBackend = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const input: CreateGatewayBackendRequest = {
      provider,
      set_default: setAsDefault,
    };
    if (baseUrl.trim()) input.base_url = baseUrl.trim();
    if (backendKey.trim()) input.key = backendKey.trim();
    if (backendName.trim()) input.display_name = backendName.trim();
    createBackendMutation.mutate(input);
  };

  const startBackendEdit = (backend: GatewayBackend) => {
    setEditingBackendId(backend.id);
    setEditName(gatewayBackendLabel(backend));
    setEditBaseUrl(backend.base_url);
    setEditKey("");
    setEditEnabled(backend.enabled);
  };

  const saveBackendEdit = (backend: GatewayBackend) => {
    const input: UpdateGatewayBackendRequest = {
      display_name: editName.trim(),
      base_url: editBaseUrl.trim(),
      enabled: editEnabled,
    };
    if (editKey.trim()) input.key = editKey.trim();
    updateBackendMutation.mutate({ id: backend.id, input });
  };

  const deleteBackend = (backend: GatewayBackend) => {
    if (backend.is_default) return;
    deleteBackendMutation.mutate(backend.id);
  };

  const selectProvider = (value: string) => {
    setProvider(value);
    setBaseUrl(gatewayProviderPresets.find((preset) => preset.value === value)?.baseUrl ?? "");
  };

  const copyGeneratedGatewayKey = () => {
    if (typeof navigator !== "undefined" && navigator.clipboard && generatedEnv) {
      void navigator.clipboard.writeText(generatedEnv);
    }
  };

  return (
    <div className="space-y-3">
      {!permissionsLoading && !canManage ? (
        <Card size="sm" className="rounded-lg">
          <CardContent className="flex items-start gap-3">
            <LockKeyhole className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            <div className="space-y-1">
              <p className="text-sm font-medium">Gateway administration requires owner or admin access.</p>
              <p className="text-xs text-muted-foreground">
                Your current role is {memberRole ?? "unknown"}. You can generate your own Gateway key and view routing policy, but workspace backends, capture policy, and ingest keys are managed by admins.
              </p>
            </div>
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-3 xl:grid-cols-4">
        <Card size="sm" className="rounded-lg">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <Route className="size-4 text-muted-foreground" />
              Gateway URLs
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {statusQuery.isLoading && !status ? (
              <>
                <Skeleton className="h-16 rounded-md" />
                <Skeleton className="h-16 rounded-md" />
              </>
            ) : (
              <>
                <GatewayValue label="OpenAI-compatible" value={status?.openai_base_url ?? ""} />
                <GatewayValue label="Anthropic-compatible" value={status?.anthropic_base_url ?? ""} />
                <div className="grid gap-2 sm:grid-cols-2">
                  <GatewayValue label="Default backend" value={status?.default_backend?.slug ?? "None"} />
                  <GatewayValue label="Enabled backends" value={`${status?.enabled_backend_count ?? 0}/${status?.backend_count ?? 0}`} />
                </div>
              </>
            )}
          </CardContent>
        </Card>

        <Card size="sm" className="rounded-lg">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <KeyRound className="size-4 text-muted-foreground" />
              User Gateway Key
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <Badge variant={status?.has_active_key ? "secondary" : "outline"}>
                {status?.has_active_key ? "active key" : "no active key"}
              </Badge>
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={createUserKeyMutation.isPending}
                onClick={() => createUserKeyMutation.mutate()}
              >
                <KeyRound className="size-3.5" />
                Generate Gateway key
              </Button>
            </div>
            {generatedKey ? (
              <div className="space-y-2">
                <div className="flex items-center justify-between gap-2">
                  <p className="text-xs text-muted-foreground">Copy this secret now. It will not be shown again.</p>
                  <Button
                    type="button"
                    size="icon-xs"
                    variant="ghost"
                    onClick={copyGeneratedGatewayKey}
                    aria-label="Copy generated Gateway key"
                  >
                    <Copy className="size-3.5" />
                  </Button>
                </div>
                <pre className="max-h-44 overflow-auto rounded-md border bg-muted/30 p-3 text-xs leading-relaxed">
                  {generatedEnv}
                </pre>
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">
                Generate a key for Claude Code, Codex, or any OpenAI/Anthropic-compatible client.
              </p>
            )}
            {createUserKeyMutation.error instanceof Error ? (
              <p className="text-xs text-destructive">{createUserKeyMutation.error.message}</p>
            ) : null}
          </CardContent>
        </Card>

        <Card size="sm" className="rounded-lg">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <ShieldCheck className="size-4 text-muted-foreground" />
              Capture Policy
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-xs text-muted-foreground">Current: {status?.capture_policy ?? "full_content"}</p>
            {canManage ? (
              <div className="flex flex-wrap gap-2">
                {capturePolicies.map((policy) => (
                  <Button
                    key={policy}
                    type="button"
                    size="xs"
                    variant={status?.capture_policy === policy ? "default" : "outline"}
                    disabled={policyMutation.isPending || status?.capture_policy === policy}
                    onClick={() => policyMutation.mutate(policy)}
                  >
                    {policy}
                  </Button>
                ))}
              </div>
            ) : permissionsLoading ? (
              <p className="text-xs text-muted-foreground">Checking workspace permissions...</p>
            ) : (
              <p className="text-xs text-muted-foreground">Only workspace owners and admins can change capture policy.</p>
            )}
            {policyMutation.error instanceof Error ? (
              <p className="text-xs text-destructive">{policyMutation.error.message}</p>
            ) : null}
          </CardContent>
        </Card>

        {canManage ? (
          <Card size="sm" className="rounded-lg">
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-sm">
                <DatabaseZap className="size-4 text-muted-foreground" />
                Export Controls
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="grid gap-2 sm:grid-cols-[1fr_auto]">
                <div className="space-y-1.5">
                  <Label htmlFor="gateway-export-window">Gateway export window</Label>
                  <NativeSelect
                    id="gateway-export-window"
                    className="w-full"
                    value={exportWindow}
                    onChange={(event) => setExportWindow(event.target.value)}
                  >
                    {windowOptions.map((option) => (
                      <NativeSelectOption key={option.value} value={option.value}>
                        {option.label}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </div>
                <div className="flex items-end">
                  <Button
                    type="button"
                    size="sm"
                    disabled={exportMutation.isPending}
                    onClick={() => exportMutation.mutate(exportWindow)}
                  >
                    <DatabaseZap className="size-3.5" />
                    Export Gateway data
                  </Button>
                </div>
              </div>
              <p className="text-xs text-muted-foreground">
                Produces the same observability bundle used by audit review and compliance workflows.
              </p>
              {lastExport ? (
                <p className="rounded-md border bg-muted/30 p-2 text-xs text-muted-foreground">
                  {gatewayExportSummary(lastExport)}
                </p>
              ) : null}
              {exportMutation.error instanceof Error ? (
                <p className="text-xs text-destructive">{exportMutation.error.message}</p>
              ) : null}
            </CardContent>
          </Card>
        ) : null}
      </div>

      <div className={cn("grid gap-3", canManage ? "xl:grid-cols-[360px_minmax(0,1fr)]" : "xl:grid-cols-1")}>
        {canManage ? (
          <Card size="sm" className="rounded-lg">
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-sm">
                <Server className="size-4 text-muted-foreground" />
                Add Backend
              </CardTitle>
            </CardHeader>
            <CardContent>
              <form className="space-y-3" onSubmit={submitBackend}>
                <div className="space-y-1.5">
                  <Label htmlFor="gateway-backend-provider">Provider</Label>
                  <NativeSelect
                    id="gateway-backend-provider"
                    className="w-full"
                    value={provider}
                    onChange={(event) => selectProvider(event.target.value)}
                  >
                    {gatewayProviderPresets.map((preset) => (
                      <NativeSelectOption key={preset.value} value={preset.value}>
                        {preset.label}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="gateway-backend-base-url">Backend base URL</Label>
                  <Input
                    id="gateway-backend-base-url"
                    value={baseUrl}
                    onChange={(event) => setBaseUrl(event.target.value)}
                    autoComplete="off"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="gateway-backend-key">Backend API key</Label>
                  <Input
                    id="gateway-backend-key"
                    value={backendKey}
                    onChange={(event) => setBackendKey(event.target.value)}
                    placeholder={selectedProviderRequiresKey ? "sk-..." : "managed by sidecar"}
                    autoComplete="off"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="gateway-backend-name">Backend name</Label>
                  <Input
                    id="gateway-backend-name"
                    value={backendName}
                    onChange={(event) => setBackendName(event.target.value)}
                    placeholder="Optional display name"
                    autoComplete="off"
                  />
                </div>
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    className="size-4 rounded border-input"
                    checked={setAsDefault}
                    onChange={(event) => setSetAsDefault(event.target.checked)}
                  />
                  Set as default
                </label>
                {createBackendMutation.error instanceof Error ? (
                  <p className="text-xs text-destructive">{createBackendMutation.error.message}</p>
                ) : null}
                <Button
                  type="submit"
                  size="sm"
                  disabled={
                    createBackendMutation.isPending ||
                    !provider ||
                    !baseUrl.trim() ||
                    (selectedProviderRequiresKey && !backendKey.trim())
                  }
                >
                  <Server className="size-3.5" />
                  Add backend
                </Button>
              </form>
            </CardContent>
          </Card>
        ) : null}

        <Card size="sm" className="rounded-lg">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <Server className="size-4 text-muted-foreground" />
              Managed Backends
            </CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {backendsQuery.isLoading ? (
              <div className="space-y-2 p-3">
                {Array.from({ length: 3 }).map((_, index) => (
                  <Skeleton key={index} className="h-12 rounded-md" />
                ))}
              </div>
            ) : (
              <GatewayBackendsTable
                backends={backends}
                canManage={canManage}
                editBaseUrl={editBaseUrl}
                editEnabled={editEnabled}
                editKey={editKey}
                editingBackendId={editingBackendId}
                editName={editName}
                onCancelEdit={resetBackendEdit}
                onDeleteBackend={deleteBackend}
                onSaveEdit={saveBackendEdit}
                pendingDeleteId={deleteBackendMutation.isPending ? deleteBackendMutation.variables ?? "" : ""}
                pendingDefaultSlug={defaultMutation.isPending ? defaultMutation.variables ?? "" : ""}
                pendingEditId={updateBackendMutation.isPending ? updateBackendMutation.variables?.id ?? "" : ""}
                onSetDefault={(slug) => defaultMutation.mutate(slug)}
                onStartEdit={startBackendEdit}
                setEditBaseUrl={setEditBaseUrl}
                setEditEnabled={setEditEnabled}
                setEditKey={setEditKey}
                setEditName={setEditName}
                wsId={wsId}
              />
            )}
            {backendsQuery.error instanceof Error ? (
              <p className="border-t p-3 text-xs text-destructive">{backendsQuery.error.message}</p>
            ) : null}
            {updateBackendMutation.error instanceof Error ? (
              <p className="border-t p-3 text-xs text-destructive">{updateBackendMutation.error.message}</p>
            ) : null}
            {deleteBackendMutation.error instanceof Error ? (
              <p className="border-t p-3 text-xs text-destructive">{deleteBackendMutation.error.message}</p>
            ) : null}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function GatewayGovernanceInsights({
  canManage,
  wsId,
}: {
  canManage: boolean;
  wsId: string;
}) {
  const insightsQuery = useQuery(gatewayGovernanceInsightsOptions(wsId, canManage));
  const insights = insightsQuery.data as GatewayGovernanceInsightsResponse | undefined;

  if (!canManage) {
    return (
      <Card size="sm" className="rounded-lg">
        <CardContent className="flex items-start gap-3">
          <LockKeyhole className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          <div className="space-y-1">
            <p className="text-sm font-medium">Governance insights require owner or admin access.</p>
            <p className="text-xs text-muted-foreground">
              Workspace admins can review risk, behavior trends, compliance coverage, and action queue items.
            </p>
          </div>
        </CardContent>
      </Card>
    );
  }

  if (insightsQuery.isLoading && !insights) {
    return (
      <div className="space-y-3">
        <div className="grid gap-3 md:grid-cols-4">
          {Array.from({ length: 4 }).map((_, index) => (
            <Skeleton key={index} className="h-28 rounded-lg" />
          ))}
        </div>
        <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_360px]">
          <Skeleton className="h-80 rounded-lg" />
          <Skeleton className="h-80 rounded-lg" />
        </div>
      </div>
    );
  }

  if (!insights) {
    return (
      <Card size="sm" className="rounded-lg">
        <CardContent className="flex h-44 flex-col items-center justify-center text-center">
          <Gauge className="size-8 text-muted-foreground/40" />
          <p className="mt-3 text-sm font-medium">No governance insights returned</p>
          <p className="mt-1 text-xs text-muted-foreground">Refresh after Gateway traffic and governance records are available.</p>
          {insightsQuery.error instanceof Error ? (
            <p className="mt-3 text-xs text-destructive">{insightsQuery.error.message}</p>
          ) : null}
        </CardContent>
      </Card>
    );
  }

  const risk = insights.risk_overview;
  const behavior = insights.behavior_trends;
  const coverage = insights.compliance_coverage;
  const maxModelCalls = Math.max(...behavior.top_models.map((model) => model.call_count), 1);
  const maxBackendCalls = Math.max(...behavior.top_backends.map((backend) => backend.call_count), 1);
  const maxReasonCount = Math.max(...behavior.policy_reason_counts.map((reason) => reason.count), 1);
  const maxBlockedCount = Math.max(...behavior.blocked_resources.map((resource) => resource.count), 1);

  return (
    <div className="space-y-3">
      <Card size="sm" className="rounded-lg">
        <CardHeader>
          <CardTitle className="flex items-center justify-between gap-3 text-sm">
            <span className="flex items-center gap-2">
              <ShieldCheck className="size-4 text-muted-foreground" />
              Governance Insights
            </span>
            <span className="text-xs font-normal text-muted-foreground">
              Generated {formatTime(insights.generated_at)}
            </span>
          </CardTitle>
        </CardHeader>
        <CardContent className="grid gap-3 md:grid-cols-4">
          <GovernanceInsightMetric
            label="Open Incidents"
            value={formatCount(risk.open_incident_count)}
            detail={`${formatCount(risk.high_severity_open_incident_count)} high severity`}
            icon={AlertTriangle}
            variant={risk.high_severity_open_incident_count > 0 ? "destructive" : "outline"}
          />
          <GovernanceInsightMetric
            label="Approvals"
            value={formatCount(risk.pending_approval_count)}
            detail={`${formatCount(risk.pending_approval_count)} pending approvals`}
            icon={Check}
          />
          <GovernanceInsightMetric
            label="Policy Decisions"
            value={formatCount(risk.blocked_decision_count + risk.warn_decision_count)}
            detail={`${formatCount(risk.blocked_decision_count)} blocked decisions`}
            icon={ShieldCheck}
          />
          <GovernanceInsightMetric
            label="Provider Risk"
            value={formatCount(risk.high_risk_provider_count)}
            detail={`${formatCount(risk.high_risk_provider_count)} high-risk providers`}
            icon={Server}
            variant={risk.high_risk_provider_count > 0 ? "destructive" : "outline"}
          />
          <GovernanceInsightMetric
            label="Review Warnings"
            value={formatCount(risk.provider_review_warning_count)}
            detail={`${formatCount(risk.provider_review_warning_count)} provider warnings`}
            icon={Gauge}
          />
          <GovernanceInsightMetric
            label="Control Gaps"
            value={formatCount(risk.control_gap_count)}
            detail={`${formatCount(coverage.gap_control_count)} gaps`}
            icon={Boxes}
            variant={risk.control_gap_count > 0 ? "destructive" : "outline"}
          />
          <GovernanceInsightMetric
            label="Policy Exceptions"
            value={formatCount(risk.active_policy_exception_count)}
            detail={`${formatCount(risk.active_policy_exception_count)} active exceptions`}
            icon={Route}
          />
          <GovernanceInsightMetric
            label="Evidence"
            value={formatCount(coverage.evidence_count)}
            detail={`${formatCount(coverage.evidence_count)} evidence records`}
            icon={DatabaseZap}
          />
        </CardContent>
      </Card>

      <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_360px]">
        <div className="space-y-3">
          <div className="grid gap-3 lg:grid-cols-2">
            <Card size="sm" className="rounded-lg">
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-sm">
                  <MessageSquareText className="size-4 text-muted-foreground" />
                  Model Usage
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                {behavior.top_models.length === 0 ? (
                  <p className="text-xs text-muted-foreground">No model usage recorded in the governance window.</p>
                ) : (
                  behavior.top_models.slice(0, 6).map((model) => (
                    <BreakdownRow
                      key={model.model}
                      label={model.model}
                      value={model.call_count}
                      max={maxModelCalls}
                      detail={`${formatCount(model.total_tokens)} tokens · ${formatMoney(model.total_cost)}`}
                    />
                  ))
                )}
              </CardContent>
            </Card>

            <Card size="sm" className="rounded-lg">
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-sm">
                  <Server className="size-4 text-muted-foreground" />
                  Backend Usage
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                {behavior.top_backends.length === 0 ? (
                  <p className="text-xs text-muted-foreground">No backend usage recorded in the governance window.</p>
                ) : (
                  behavior.top_backends.slice(0, 6).map((backend) => (
                    <BreakdownRow
                      key={backend.backend}
                      label={backend.backend}
                      value={backend.call_count}
                      max={maxBackendCalls}
                      detail={`${formatMS(backend.avg_latency_ms)} avg · ${formatCount(backend.error_count)} errors`}
                    />
                  ))
                )}
              </CardContent>
            </Card>
          </div>

          <div className="grid gap-3 lg:grid-cols-2">
            <Card size="sm" className="rounded-lg">
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-sm">
                  <ShieldCheck className="size-4 text-muted-foreground" />
                  Policy Reasons
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                {behavior.policy_reason_counts.length === 0 ? (
                  <p className="text-xs text-muted-foreground">No policy reason counts recorded.</p>
                ) : (
                  behavior.policy_reason_counts.slice(0, 6).map((reason) => (
                    <BreakdownRow
                      key={reason.reason_code}
                      label={reason.reason_code}
                      value={reason.count}
                      max={maxReasonCount}
                      detail={`${formatCount(reason.count)} decisions`}
                    />
                  ))
                )}
              </CardContent>
            </Card>

            <Card size="sm" className="rounded-lg">
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-sm">
                  <AlertTriangle className="size-4 text-muted-foreground" />
                  Blocked Resources
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                {behavior.blocked_resources.length === 0 ? (
                  <p className="text-xs text-muted-foreground">No blocked resources recorded.</p>
                ) : (
                  behavior.blocked_resources.slice(0, 6).map((resource) => (
                    <BreakdownRow
                      key={`${resource.resource_type}-${resource.resource_id}`}
                      label={resource.resource_label || resource.resource_id || resource.resource_type}
                      value={resource.count}
                      max={maxBlockedCount}
                      detail={`${formatCount(resource.count)} blocks · ${resource.resource_type}`}
                    />
                  ))
                )}
              </CardContent>
            </Card>
          </div>

          <Card size="sm" className="rounded-lg">
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-sm">
                <Boxes className="size-4 text-muted-foreground" />
                Compliance Coverage
              </CardTitle>
            </CardHeader>
            <CardContent>
              <div className="grid gap-3 md:grid-cols-5">
                <GatewayValue label="Controls" value={`${formatCount(coverage.control_count)} mapped`} />
                <GatewayValue label="Covered" value={`${formatCount(coverage.covered_control_count)} covered`} />
                <GatewayValue label="Partial" value={`${formatCount(coverage.partial_control_count)} partial`} />
                <GatewayValue label="Gaps" value={`${formatCount(coverage.gap_control_count)} gaps`} />
                <GatewayValue label="Stale" value={`${formatCount(coverage.stale_control_count)} stale`} />
              </div>
              <p className="mt-3 text-xs text-muted-foreground">
                {formatCount(coverage.evidence_count)} evidence records · latest evidence {formatNullableTime(coverage.last_evidence_generated_at)}
              </p>
            </CardContent>
          </Card>
        </div>

        <Card size="sm" className="rounded-lg">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <AlertTriangle className="size-4 text-muted-foreground" />
              Action Queue
            </CardTitle>
          </CardHeader>
          <CardContent>
            <GovernanceActionQueue actions={insights.action_queue} />
          </CardContent>
        </Card>
      </div>

      {insightsQuery.error instanceof Error ? (
        <p className="text-xs text-destructive">{insightsQuery.error.message}</p>
      ) : null}
    </div>
  );
}

function GatewayHealthPanel({
  canManage,
  wsId,
}: {
  canManage: boolean;
  wsId: string;
}) {
  const qc = useQueryClient();
  const reportQuery = useQuery(gatewayHealthReportOptions(wsId, canManage));
  const report = reportQuery.data as GatewayHealthReportResponse | undefined;
  const checks = report?.checks ?? [];
  const backends = report?.backends ?? [];
  const governance = report?.governance;

  if (!canManage) return null;

  const refreshReport = () => {
    void qc.invalidateQueries({ queryKey: gatewayKeys.healthReport(wsId) });
  };

  return (
    <Card size="sm" className="rounded-lg">
      <CardHeader>
        <CardTitle className="flex items-center justify-between gap-3 text-sm">
          <span className="flex items-center gap-2">
            <Gauge className="size-4 text-muted-foreground" />
            Gateway Health
          </span>
          <span className="flex items-center gap-2">
            <Badge variant={doctorStatusVariant(report?.status ?? "unknown")}>
              {report?.status ?? "checking"}
            </Badge>
            <Button
              type="button"
              size="icon-xs"
              variant="ghost"
              onClick={refreshReport}
              aria-label="Refresh Gateway health"
              title="Refresh Gateway health"
            >
              <RefreshCw className="size-3.5" />
            </Button>
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 p-3">
        {reportQuery.isLoading ? (
          <div className="space-y-2 p-3">
            {Array.from({ length: 4 }).map((_, index) => (
              <Skeleton key={index} className="h-12 rounded-md" />
            ))}
          </div>
        ) : !report ? (
          <div className="flex h-32 flex-col items-center justify-center text-center">
            <Gauge className="size-8 text-muted-foreground/40" />
            <p className="mt-3 text-sm font-medium">No health report returned</p>
            <p className="mt-1 text-xs text-muted-foreground">Refresh after Gateway is configured.</p>
          </div>
        ) : (
          <>
            <div className="grid gap-2 md:grid-cols-4">
              <div className="rounded-md border p-3">
                <p className="text-xs text-muted-foreground">Capture Policy</p>
                <p className="mt-1 truncate text-sm font-medium">Capture: {governance?.capture_policy ?? "unknown"}</p>
              </div>
              <div className="rounded-md border p-3">
                <p className="text-xs text-muted-foreground">Backends</p>
                <p className="mt-1 text-sm font-medium">{backends.length} configured</p>
              </div>
              <div className="rounded-md border p-3">
                <p className="text-xs text-muted-foreground">Open Incidents</p>
                <p className="mt-1 text-sm font-medium">{formatCount(governance?.open_incident_count ?? 0)} open incidents</p>
              </div>
              <div className="rounded-md border p-3">
                <p className="text-xs text-muted-foreground">Approvals</p>
                <p className="mt-1 text-sm font-medium">{formatCount(governance?.pending_approval_count ?? 0)} pending approvals</p>
              </div>
            </div>

            {backends.length > 0 ? (
              <Table aria-label="Gateway backend health">
                <TableHeader>
                  <TableRow>
                    <TableHead>Backend</TableHead>
                    <TableHead>Probe</TableHead>
                    <TableHead>Models</TableHead>
                    <TableHead>Credentials</TableHead>
                    <TableHead>Latency</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {backends.map((backend) => (
                    <TableRow key={backend.id}>
                      <TableCell>
                        <div className="min-w-0">
                          <p className="truncate font-medium">{backend.slug}</p>
                          <p className="truncate text-xs text-muted-foreground">{backend.backend_type}</p>
                        </div>
                      </TableCell>
                      <TableCell>
                        <Badge variant={doctorStatusVariant(backend.probe_status)}>{backend.probe_status}</Badge>
                      </TableCell>
                      <TableCell>{formatCount(backend.model_count)} models</TableCell>
                      <TableCell>
                        <span className="text-sm">
                          {formatCount(backend.credential_summary.enabled)}/{formatCount(backend.credential_summary.total)} active
                        </span>
                        {(backend.credential_summary.rate_limited > 0 || backend.credential_summary.last_errors > 0) && (
                          <span className="ml-2 text-xs text-muted-foreground">
                            {formatCount(backend.credential_summary.rate_limited)} limited · {formatCount(backend.credential_summary.last_errors)} errors
                          </span>
                        )}
                      </TableCell>
                      <TableCell>{formatMS(backend.probe_latency_ms)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            ) : null}

            <Table aria-label="Gateway health checks">
              <TableHeader>
                <TableRow>
                  <TableHead>Status</TableHead>
                  <TableHead>Category</TableHead>
                  <TableHead>Check</TableHead>
                  <TableHead>Detail</TableHead>
                  <TableHead>Remediation</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {checks.map((check) => (
                  <TableRow key={check.id}>
                    <TableCell>
                      <Badge variant={doctorStatusVariant(check.status)}>{check.status}</Badge>
                    </TableCell>
                    <TableCell>{check.category}</TableCell>
                    <TableCell>
                      <span className="font-medium">{check.title}</span>
                    </TableCell>
                    <TableCell>
                      <span className="line-clamp-2 max-w-md text-sm">{check.detail}</span>
                    </TableCell>
                    <TableCell>
                      <span className="line-clamp-2 max-w-md text-sm text-muted-foreground">{check.remediation || "None"}</span>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </>
        )}
        {reportQuery.error instanceof Error ? (
          <p className="border-t p-3 text-xs text-destructive">{reportQuery.error.message}</p>
        ) : null}
      </CardContent>
    </Card>
  );
}

function GatewayGovernanceSetup({
  canManage,
  wsId,
}: {
  canManage: boolean;
  wsId: string;
}) {
  const qc = useQueryClient();
  const backendsQuery = useQuery(gatewayBackendsOptions(wsId));
  const risksQuery = useQuery(gatewayProviderRisksOptions(wsId, canManage));
  const backendRows = backendsQuery.data;
  const riskRows = risksQuery.data;
  const backends = useMemo(() => backendRows ?? [], [backendRows]);
  const risks = useMemo(() => riskRows ?? [], [riskRows]);
  const [providerName, setProviderName] = useState("");
  const [riskScore, setRiskScore] = useState("0");
  const [securityReviewStatus, setSecurityReviewStatus] = useState("unknown");
  const [contractStatus, setContractStatus] = useState("unknown");
  const [approvedUseCases, setApprovedUseCases] = useState("");
  const [dataCategories, setDataCategories] = useState("");

  useEffect(() => {
    if (!providerName && backends.length > 0) {
      setProviderName(backends[0]!.slug);
    }
  }, [backends, providerName]);

  useEffect(() => {
    if (!providerName) return;
    const risk = risks.find((item) => item.provider_name === providerName);
    setRiskScore(String(risk?.risk_score ?? 0));
    setSecurityReviewStatus(risk?.security_review_status ?? "unknown");
    setContractStatus(risk?.contract_status ?? "unknown");
    setApprovedUseCases(joinList(risk?.approved_use_cases));
    setDataCategories(joinList(risk?.data_categories));
  }, [providerName, risks]);

  const upsertRiskMutation = useMutation({
    mutationFn: (input: UpsertGatewayProviderRiskRequest) => api.upsertGatewayProviderRisk(input),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: gatewayKeys.providerRisks(wsId) });
      void qc.invalidateQueries({ queryKey: gatewayKeys.audit(wsId, 20) });
    },
  });

  if (!canManage) return null;

  const selectedBackend = backends.find((backend) => backend.slug === providerName);
  const normalizedRiskScore = Math.min(100, Math.max(0, Number.parseInt(riskScore, 10) || 0));

  const submitRisk = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!providerName) return;
    upsertRiskMutation.mutate({
      provider_name: providerName,
      backend_id: selectedBackend?.id,
      approved_use_cases: commaList(approvedUseCases),
      data_categories: commaList(dataCategories),
      contract_status: contractStatus,
      security_review_status: securityReviewStatus,
      risk_score: normalizedRiskScore,
    });
  };

  return (
    <Card size="sm" className="rounded-lg">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm">
          <ShieldCheck className="size-4 text-muted-foreground" />
          Provider Risk Register
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid gap-4 xl:grid-cols-[360px_minmax(0,1fr)]">
          <form className="space-y-3" onSubmit={submitRisk}>
            <div className="space-y-1.5">
              <Label htmlFor="gateway-governance-provider">Governance provider</Label>
              <NativeSelect
                id="gateway-governance-provider"
                className="w-full"
                value={providerName}
                onChange={(event) => setProviderName(event.target.value)}
                disabled={backends.length === 0}
              >
                {backends.map((backend) => (
                  <NativeSelectOption key={backend.id} value={backend.slug}>
                    {gatewayBackendLabel(backend)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="gateway-risk-score">Risk score</Label>
                <Input
                  id="gateway-risk-score"
                  type="number"
                  min={0}
                  max={100}
                  value={riskScore}
                  onChange={(event) => setRiskScore(event.target.value)}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="gateway-security-review">Security review</Label>
                <NativeSelect
                  id="gateway-security-review"
                  className="w-full"
                  value={securityReviewStatus}
                  onChange={(event) => setSecurityReviewStatus(event.target.value)}
                >
                  {governanceStatuses.map((status) => (
                    <NativeSelectOption key={status} value={status}>
                      {status}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="gateway-contract-status">Contract status</Label>
              <NativeSelect
                id="gateway-contract-status"
                className="w-full"
                value={contractStatus}
                onChange={(event) => setContractStatus(event.target.value)}
              >
                {governanceStatuses.map((status) => (
                  <NativeSelectOption key={status} value={status}>
                    {status}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="gateway-approved-use-cases">Approved use cases</Label>
              <Input
                id="gateway-approved-use-cases"
                value={approvedUseCases}
                onChange={(event) => setApprovedUseCases(event.target.value)}
                placeholder="internal support, code review"
                autoComplete="off"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="gateway-data-categories">Data categories</Label>
              <Input
                id="gateway-data-categories"
                value={dataCategories}
                onChange={(event) => setDataCategories(event.target.value)}
                placeholder="source_code, customer_data"
                autoComplete="off"
              />
            </div>
            {upsertRiskMutation.error instanceof Error ? (
              <p className="text-xs text-destructive">{upsertRiskMutation.error.message}</p>
            ) : null}
            <Button
              type="submit"
              size="sm"
              disabled={backends.length === 0 || !providerName || upsertRiskMutation.isPending}
            >
              <ShieldCheck className="size-3.5" />
              Save provider risk
            </Button>
          </form>

          <div className="min-w-0">
            {risksQuery.isLoading ? (
              <div className="space-y-2">
                {Array.from({ length: 3 }).map((_, index) => (
                  <Skeleton key={index} className="h-14 rounded-md" />
                ))}
              </div>
            ) : risks.length === 0 ? (
              <div className="flex h-44 flex-col items-center justify-center rounded-md border text-center">
                <ShieldCheck className="size-8 text-muted-foreground/40" />
                <p className="mt-3 text-sm font-medium">No provider risks recorded</p>
                <p className="mt-1 text-xs text-muted-foreground">Assess managed backends before broad enterprise rollout.</p>
              </div>
            ) : (
              <Table aria-label="Gateway provider risk register">
                <TableHeader>
                  <TableRow>
                    <TableHead>Provider</TableHead>
                    <TableHead>Reviews</TableHead>
                    <TableHead>Data</TableHead>
                    <TableHead className="text-right">Risk</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {risks.map((risk: GatewayProviderRisk) => (
                    <TableRow key={risk.id}>
                      <TableCell>
                        <div className="flex max-w-56 flex-col">
                          <span className="truncate font-medium">{risk.provider_name}</span>
                          <span className="truncate text-xs text-muted-foreground">
                            {risk.capability_class || "general_purpose_llm"}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap gap-1">
                          <Badge variant="outline">{risk.security_review_status}</Badge>
                          <Badge variant="outline">{risk.contract_status}</Badge>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex max-w-72 flex-wrap gap-1">
                          {risk.data_categories.length === 0 ? (
                            <Badge variant="outline">unclassified</Badge>
                          ) : (
                            risk.data_categories.slice(0, 4).map((category) => (
                              <Badge key={category} variant="secondary">
                                {category}
                              </Badge>
                            ))
                          )}
                        </div>
                      </TableCell>
                      <TableCell className="text-right">
                        <Badge variant={risk.risk_score >= 80 ? "destructive" : "outline"}>
                          Risk {risk.risk_score}
                        </Badge>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
            {risksQuery.error instanceof Error ? (
              <p className="mt-3 text-xs text-destructive">{risksQuery.error.message}</p>
            ) : null}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function ingestKeyName(key: GatewayIngestKeyListItem): string {
  return key.display_name || key.app_id || key.key_prefix;
}

function GatewayAuditHistory({
  canManage,
  wsId,
}: {
  canManage: boolean;
  wsId: string;
}) {
  const auditQuery = useQuery(gatewayAuditOptions(wsId, 20, canManage));
  const rows = auditQuery.data ?? [];

  if (!canManage) return null;

  return (
    <Card size="sm" className="rounded-lg">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm">
          <History className="size-4 text-muted-foreground" />
          Gateway Change History
        </CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        {auditQuery.isLoading ? (
          <div className="space-y-2 p-3">
            {Array.from({ length: 3 }).map((_, index) => (
              <Skeleton key={index} className="h-12 rounded-md" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <div className="flex h-32 flex-col items-center justify-center border-t text-center">
            <History className="size-8 text-muted-foreground/40" />
            <p className="mt-3 text-sm font-medium">No Gateway changes recorded</p>
            <p className="mt-1 text-xs text-muted-foreground">Backend, policy, and key administration events will appear here.</p>
          </div>
        ) : (
          <Table aria-label="Gateway audit history">
            <TableHeader>
              <TableRow>
                <TableHead>Action</TableHead>
                <TableHead>Actor</TableHead>
                <TableHead>Target</TableHead>
                <TableHead className="text-right">Time</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row: GatewayAuditLogItem) => (
                <TableRow key={row.id}>
                  <TableCell>
                    <Badge variant="outline">{row.action}</Badge>
                  </TableCell>
                  <TableCell>
                    <div className="flex max-w-56 flex-col">
                      <span className="truncate font-medium">{row.actor_name || row.actor_email || row.actor_user_id}</span>
                      <span className="truncate text-xs text-muted-foreground">{row.actor_email}</span>
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="flex max-w-64 flex-col">
                      <span className="truncate font-medium">{row.target_id}</span>
                      <span className="truncate text-xs text-muted-foreground">{row.target_type}</span>
                    </div>
                  </TableCell>
                  <TableCell className="text-right text-xs text-muted-foreground">{formatTime(row.created_at)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {auditQuery.error instanceof Error ? (
          <p className="border-t p-3 text-xs text-destructive">{auditQuery.error.message}</p>
        ) : null}
      </CardContent>
    </Card>
  );
}

function GatewayPolicyDecisions({
  canManage,
  wsId,
}: {
  canManage: boolean;
  wsId: string;
}) {
  const qc = useQueryClient();
  const decisionsQuery = useQuery(gatewayPolicyDecisionsOptions(wsId, 20, canManage));
  const rows = decisionsQuery.data ?? [];
  const invalidateDecisions = () => {
    void qc.invalidateQueries({ queryKey: gatewayKeys.policyDecisions(wsId, 20) });
    void qc.invalidateQueries({ queryKey: gatewayKeys.policyExceptions(wsId, 20) });
    void qc.invalidateQueries({ queryKey: gatewayKeys.audit(wsId, 20) });
  };
  const approveDecisionMutation = useMutation({
    mutationFn: (id: string) =>
      api.approveGatewayPolicyDecision(id, {
        reason: "Approved from Gateway policy decisions",
      }),
    onSuccess: invalidateDecisions,
  });
  const denyDecisionMutation = useMutation({
    mutationFn: (id: string) =>
      api.denyGatewayPolicyDecision(id, {
        reason: "Denied from Gateway policy decisions",
      }),
    onSuccess: invalidateDecisions,
  });

  if (!canManage) return null;

  return (
    <Card size="sm" className="rounded-lg">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm">
          <ShieldCheck className="size-4 text-muted-foreground" />
          Policy Decisions
        </CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        {decisionsQuery.isLoading ? (
          <div className="space-y-2 p-3">
            {Array.from({ length: 3 }).map((_, index) => (
              <Skeleton key={index} className="h-12 rounded-md" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <div className="flex h-32 flex-col items-center justify-center border-t text-center">
            <ShieldCheck className="size-8 text-muted-foreground/40" />
            <p className="mt-3 text-sm font-medium">No policy decisions recorded</p>
            <p className="mt-1 text-xs text-muted-foreground">Gateway governance blocks and warnings will appear here.</p>
          </div>
        ) : (
          <Table aria-label="Gateway policy decisions">
            <TableHeader>
              <TableRow>
                <TableHead>Decision</TableHead>
                <TableHead>Reason</TableHead>
                <TableHead>Resource</TableHead>
                <TableHead className="text-right">Time</TableHead>
                <TableHead className="text-right">Action</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row: GatewayPolicyDecisionItem) => {
                const resourceLabel = row.resource_label || row.resource_id || row.resource_type;
                const canApprove = row.approval_status === "requested";
                const approving = approveDecisionMutation.isPending && approveDecisionMutation.variables === row.id;
                const denying = denyDecisionMutation.isPending && denyDecisionMutation.variables === row.id;
                return (
                  <TableRow key={row.id}>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <Badge variant={row.decision === "block" ? "destructive" : "outline"}>{row.decision}</Badge>
                        {row.approval_status ? <Badge variant="outline">{row.approval_status}</Badge> : null}
                      </div>
                    </TableCell>
                    <TableCell>
                      <span className="font-medium">{row.reason_code}</span>
                    </TableCell>
                    <TableCell>
                      <div className="flex max-w-64 flex-col">
                        <span className="truncate font-medium">{resourceLabel}</span>
                        <span className="truncate text-xs text-muted-foreground">{row.resource_type}</span>
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-xs text-muted-foreground">{formatTime(row.created_at)}</TableCell>
                    <TableCell className="text-right">
                      {canApprove ? (
                        <div className="flex justify-end gap-2">
                          <Button
                            type="button"
                            size="xs"
                            variant="outline"
                            disabled={approving || denying}
                            onClick={() => approveDecisionMutation.mutate(row.id)}
                            aria-label={`Approve ${resourceLabel}`}
                          >
                            <Check className="size-3.5" />
                            Approve
                          </Button>
                          <Button
                            type="button"
                            size="xs"
                            variant="outline"
                            disabled={approving || denying}
                            onClick={() => denyDecisionMutation.mutate(row.id)}
                            aria-label={`Deny ${resourceLabel}`}
                          >
                            <X className="size-3.5" />
                            Deny
                          </Button>
                        </div>
                      ) : (
                        <span className="text-xs text-muted-foreground">No action</span>
                      )}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
        {decisionsQuery.error instanceof Error ? (
          <p className="border-t p-3 text-xs text-destructive">{decisionsQuery.error.message}</p>
        ) : null}
        {approveDecisionMutation.error instanceof Error ? (
          <p className="border-t p-3 text-xs text-destructive">{approveDecisionMutation.error.message}</p>
        ) : null}
        {denyDecisionMutation.error instanceof Error ? (
          <p className="border-t p-3 text-xs text-destructive">{denyDecisionMutation.error.message}</p>
        ) : null}
      </CardContent>
    </Card>
  );
}

function GatewayGovernancePolicies({
  canManage,
  wsId,
}: {
  canManage: boolean;
  wsId: string;
}) {
  const qc = useQueryClient();
  const policiesQuery = useQuery(gatewayGovernancePoliciesOptions(wsId, canManage));
  const policies = policiesQuery.data ?? [];
  const [policyName, setPolicyName] = useState("");
  const [policyDescription, setPolicyDescription] = useState("");
  const [policyType, setPolicyType] = useState("model");
  const [policyMode, setPolicyMode] = useState("enforce");
  const [policyEnabled, setPolicyEnabled] = useState("true");
  const [ruleDefinition, setRuleDefinition] = useState(defaultGatewayPolicyRule);
  const [ruleError, setRuleError] = useState("");

  const invalidatePolicies = () => {
    void qc.invalidateQueries({ queryKey: gatewayKeys.governancePolicies(wsId) });
    void qc.invalidateQueries({ queryKey: gatewayKeys.audit(wsId, 20) });
  };

  const createPolicyMutation = useMutation({
    mutationFn: (input: CreateGatewayGovernancePolicyRequest) => api.createGatewayGovernancePolicy(input),
    onSuccess: () => {
      setPolicyName("");
      setPolicyDescription("");
      setPolicyType("model");
      setPolicyMode("enforce");
      setPolicyEnabled("true");
      setRuleDefinition(defaultGatewayPolicyRule);
      setRuleError("");
      invalidatePolicies();
    },
  });

  const updatePolicyMutation = useMutation({
    mutationFn: ({ id, input }: { id: string; input: CreateGatewayGovernancePolicyRequest }) =>
      api.updateGatewayGovernancePolicy(id, input),
    onSuccess: invalidatePolicies,
  });

  if (!canManage) return null;

  const submitPolicy = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    let parsed: unknown;
    try {
      parsed = JSON.parse(ruleDefinition);
    } catch {
      setRuleError("Rule definition must be valid JSON.");
      return;
    }
    setRuleError("");
    createPolicyMutation.mutate({
      name: policyName.trim(),
      description: policyDescription.trim(),
      policy_type: policyType,
      enabled: policyEnabled === "true",
      enforcement_mode: policyMode,
      rule_definition: parsed,
    });
  };

  const togglePolicy = (policy: GatewayGovernancePolicyItem) => {
    updatePolicyMutation.mutate({
      id: policy.id,
      input: {
        name: policy.name,
        description: policy.description,
        policy_type: policy.policy_type,
        enabled: !policy.enabled,
        enforcement_mode: policy.enforcement_mode,
        rule_definition: policy.rule_definition,
      },
    });
  };

  return (
    <Card size="sm" className="rounded-lg">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm">
          <ShieldCheck className="size-4 text-muted-foreground" />
          Governance Policies
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid gap-4 xl:grid-cols-[360px_minmax(0,1fr)]">
          <form className="space-y-3" onSubmit={submitPolicy}>
            <div className="space-y-1.5">
              <Label htmlFor="gateway-policy-name">Policy name</Label>
              <Input
                id="gateway-policy-name"
                value={policyName}
                onChange={(event) => setPolicyName(event.target.value)}
                placeholder="Block unapproved model"
                autoComplete="off"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="gateway-policy-description">Policy description</Label>
              <Input
                id="gateway-policy-description"
                value={policyDescription}
                onChange={(event) => setPolicyDescription(event.target.value)}
                placeholder="Short admin note"
                autoComplete="off"
              />
            </div>
            <div className="grid gap-3 sm:grid-cols-3">
              <div className="space-y-1.5">
                <Label htmlFor="gateway-policy-type">Policy type</Label>
                <NativeSelect
                  id="gateway-policy-type"
                  className="w-full"
                  value={policyType}
                  onChange={(event) => setPolicyType(event.target.value)}
                >
                  {gatewayPolicyTypes.map((type) => (
                    <NativeSelectOption key={type} value={type}>
                      {type}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="gateway-policy-mode">Enforcement mode</Label>
                <NativeSelect
                  id="gateway-policy-mode"
                  className="w-full"
                  value={policyMode}
                  onChange={(event) => setPolicyMode(event.target.value)}
                >
                  {gatewayPolicyModes.map((mode) => (
                    <NativeSelectOption key={mode} value={mode}>
                      {mode}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="gateway-policy-enabled">State</Label>
                <NativeSelect
                  id="gateway-policy-enabled"
                  className="w-full"
                  value={policyEnabled}
                  onChange={(event) => setPolicyEnabled(event.target.value)}
                >
                  <NativeSelectOption value="true">enabled</NativeSelectOption>
                  <NativeSelectOption value="false">disabled</NativeSelectOption>
                </NativeSelect>
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="gateway-policy-rule">Policy rule definition</Label>
              <Textarea
                id="gateway-policy-rule"
                className="min-h-44 font-mono text-xs"
                value={ruleDefinition}
                onChange={(event) => setRuleDefinition(event.target.value)}
                spellCheck={false}
              />
            </div>
            {ruleError ? <p className="text-xs text-destructive">{ruleError}</p> : null}
            {createPolicyMutation.error instanceof Error ? (
              <p className="text-xs text-destructive">{createPolicyMutation.error.message}</p>
            ) : null}
            <Button
              type="submit"
              size="sm"
              disabled={!policyName.trim() || createPolicyMutation.isPending}
            >
              <Save className="size-3.5" />
              Create policy
            </Button>
          </form>

          <div className="min-w-0">
            {policiesQuery.isLoading ? (
              <div className="space-y-2">
                {Array.from({ length: 3 }).map((_, index) => (
                  <Skeleton key={index} className="h-14 rounded-md" />
                ))}
              </div>
            ) : policies.length === 0 ? (
              <div className="flex h-44 flex-col items-center justify-center rounded-md border text-center">
                <ShieldCheck className="size-8 text-muted-foreground/40" />
                <p className="mt-3 text-sm font-medium">No governance policies configured</p>
                <p className="mt-1 text-xs text-muted-foreground">Create JSON-backed policies to enforce model, tool, data, routing, and capture rules.</p>
              </div>
            ) : (
              <Table aria-label="Gateway governance policies">
                <TableHeader>
                  <TableRow>
                    <TableHead>Policy</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>Mode</TableHead>
                    <TableHead>Version</TableHead>
                    <TableHead className="text-right">State</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {policies.map((policy: GatewayGovernancePolicyItem) => (
                    <TableRow key={policy.id}>
                      <TableCell>
                        <div className="flex max-w-64 flex-col">
                          <span className="truncate font-medium">{policy.name}</span>
                          <span className="truncate text-xs text-muted-foreground">
                            {policy.description || "No description"}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell>
                        <Badge variant="outline">{policy.policy_type}</Badge>
                      </TableCell>
                      <TableCell>
                        <Badge variant={policy.enforcement_mode === "enforce" ? "secondary" : "outline"}>
                          {policy.enforcement_mode}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">v{policy.version}</TableCell>
                      <TableCell className="text-right">
                        <Button
                          type="button"
                          size="xs"
                          variant={policy.enabled ? "outline" : "default"}
                          disabled={updatePolicyMutation.isPending}
                          onClick={() => togglePolicy(policy)}
                          aria-label={`${policy.enabled ? "Disable" : "Enable"} policy ${policy.name}`}
                        >
                          {policy.enabled ? "enabled" : "disabled"}
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
            {policiesQuery.error instanceof Error ? (
              <p className="mt-3 text-xs text-destructive">{policiesQuery.error.message}</p>
            ) : null}
            {updatePolicyMutation.error instanceof Error ? (
              <p className="mt-3 text-xs text-destructive">{updatePolicyMutation.error.message}</p>
            ) : null}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function evidenceFrameworks(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string" && item.trim() !== "");
}

function GatewayEvidence({
  canManage,
  wsId,
}: {
  canManage: boolean;
  wsId: string;
}) {
  const evidenceQuery = useQuery(gatewayEvidenceOptions(wsId, 20, canManage));
  const rows = evidenceQuery.data ?? [];

  if (!canManage) return null;

  return (
    <Card size="sm" className="rounded-lg">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm">
          <DatabaseZap className="size-4 text-muted-foreground" />
          Evidence
        </CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        {evidenceQuery.isLoading ? (
          <div className="space-y-2 p-3">
            {Array.from({ length: 3 }).map((_, index) => (
              <Skeleton key={index} className="h-12 rounded-md" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <div className="flex h-32 flex-col items-center justify-center border-t text-center">
            <DatabaseZap className="size-8 text-muted-foreground/40" />
            <p className="mt-3 text-sm font-medium">No Gateway evidence recorded</p>
            <p className="mt-1 text-xs text-muted-foreground">Observer Gateway evidence will appear when governance rules create records.</p>
          </div>
        ) : (
          <Table aria-label="Gateway evidence">
            <TableHeader>
              <TableRow>
                <TableHead>Type</TableHead>
                <TableHead>Summary</TableHead>
                <TableHead>Framework</TableHead>
                <TableHead className="text-right">Time</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row: GatewayEvidenceItem) => {
                const frameworks = evidenceFrameworks(row.framework_refs);
                return (
                  <TableRow key={row.id}>
                    <TableCell>
                      <Badge variant="outline">{row.evidence_type}</Badge>
                    </TableCell>
                    <TableCell>
                      <span className="line-clamp-2 max-w-xl text-sm font-medium">{row.summary}</span>
                    </TableCell>
                    <TableCell>
                      <div className="flex max-w-64 flex-wrap gap-1">
                        {frameworks.length === 0 ? (
                          <Badge variant="secondary">unmapped</Badge>
                        ) : (
                          frameworks.slice(0, 3).map((framework) => (
                            <Badge key={framework} variant="secondary">
                              {framework}
                            </Badge>
                          ))
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-xs text-muted-foreground">{formatTime(row.generated_at)}</TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
        {evidenceQuery.error instanceof Error ? (
          <p className="border-t p-3 text-xs text-destructive">{evidenceQuery.error.message}</p>
        ) : null}
      </CardContent>
    </Card>
  );
}

function GatewayComplianceControls({
  canManage,
  wsId,
}: {
  canManage: boolean;
  wsId: string;
}) {
  const controlsQuery = useQuery(gatewayControlMappingsOptions(wsId, canManage));
  const rows = controlsQuery.data ?? [];

  if (!canManage) return null;

  return (
    <Card size="sm" className="rounded-lg">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm">
          <Boxes className="size-4 text-muted-foreground" />
          Compliance Controls
        </CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        {controlsQuery.isLoading ? (
          <div className="space-y-2 p-3">
            {Array.from({ length: 3 }).map((_, index) => (
              <Skeleton key={index} className="h-12 rounded-md" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <div className="flex h-32 flex-col items-center justify-center border-t text-center">
            <Boxes className="size-8 text-muted-foreground/40" />
            <p className="mt-3 text-sm font-medium">No compliance controls mapped</p>
            <p className="mt-1 text-xs text-muted-foreground">Gateway controls will appear after governance mappings are created.</p>
          </div>
        ) : (
          <Table aria-label="Gateway compliance controls">
            <TableHeader>
              <TableRow>
                <TableHead>Control</TableHead>
                <TableHead>Title</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Evidence</TableHead>
                <TableHead className="text-right">Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row: GatewayControlMappingItem) => (
                <TableRow key={row.id}>
                  <TableCell>
                    <div className="flex max-w-52 flex-col">
                      <span className="truncate font-medium">{row.control_id}</span>
                      <span className="truncate text-xs text-muted-foreground">{row.framework}</span>
                    </div>
                  </TableCell>
                  <TableCell>
                    <span className="line-clamp-2 max-w-xl text-sm font-medium">{row.control_title}</span>
                  </TableCell>
                  <TableCell>
                    <Badge variant={row.status === "gap" ? "destructive" : "outline"}>{row.status}</Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant={row.evidence_count > 0 ? "secondary" : "outline"}>Evidence {row.evidence_count}</Badge>
                  </TableCell>
                  <TableCell className="text-right text-xs text-muted-foreground">{formatTime(row.updated_at)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {controlsQuery.error instanceof Error ? (
          <p className="border-t p-3 text-xs text-destructive">{controlsQuery.error.message}</p>
        ) : null}
      </CardContent>
    </Card>
  );
}

function GatewayIncidents({
  canManage,
  wsId,
}: {
  canManage: boolean;
  wsId: string;
}) {
  const qc = useQueryClient();
  const incidentsQuery = useQuery(gatewayIncidentsOptions(wsId, 20, canManage));
  const rows = incidentsQuery.data ?? [];
  const updateIncidentMutation = useMutation({
    mutationFn: (id: string) =>
      api.updateGatewayIncident(id, {
        status: "remediated",
        remediation_notes: "Provider review completed.",
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: gatewayKeys.incidents(wsId, 20) });
      void qc.invalidateQueries({ queryKey: gatewayKeys.audit(wsId, 20) });
    },
  });

  if (!canManage) return null;

  return (
    <Card size="sm" className="rounded-lg">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm">
          <AlertTriangle className="size-4 text-muted-foreground" />
          Incidents
        </CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        {incidentsQuery.isLoading ? (
          <div className="space-y-2 p-3">
            {Array.from({ length: 3 }).map((_, index) => (
              <Skeleton key={index} className="h-12 rounded-md" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <div className="flex h-32 flex-col items-center justify-center border-t text-center">
            <AlertTriangle className="size-8 text-muted-foreground/40" />
            <p className="mt-3 text-sm font-medium">No Gateway incidents recorded</p>
            <p className="mt-1 text-xs text-muted-foreground">Provider risk blocks and compliance events will appear here.</p>
          </div>
        ) : (
          <Table aria-label="Gateway incidents">
            <TableHeader>
              <TableRow>
                <TableHead>Severity</TableHead>
                <TableHead>Category</TableHead>
                <TableHead>Summary</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Opened</TableHead>
                <TableHead className="text-right">Action</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row: GatewayIncidentItem) => (
                <TableRow key={row.id}>
                  <TableCell>
                    <Badge variant={row.severity === "critical" || row.severity === "high" ? "destructive" : "outline"}>{row.severity}</Badge>
                  </TableCell>
                  <TableCell>
                    <span className="font-medium">{row.category}</span>
                  </TableCell>
                  <TableCell>
                    <span className="line-clamp-2 max-w-xl text-sm font-medium">{row.summary}</span>
                  </TableCell>
                  <TableCell>
                    <Badge variant={row.status === "open" ? "secondary" : "outline"}>{row.status}</Badge>
                  </TableCell>
                  <TableCell className="text-right text-xs text-muted-foreground">{formatTime(row.opened_at)}</TableCell>
                  <TableCell className="text-right">
                    {row.status === "closed" || row.status === "remediated" ? (
                      <Badge variant="outline">Done</Badge>
                    ) : (
                      <Button
                        type="button"
                        size="xs"
                        variant="outline"
                        disabled={updateIncidentMutation.isPending && updateIncidentMutation.variables === row.id}
                        onClick={() => updateIncidentMutation.mutate(row.id)}
                        aria-label="Mark incident remediated"
                      >
                        <ShieldCheck className="size-3.5" />
                        Remediate
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {incidentsQuery.error instanceof Error ? (
          <p className="border-t p-3 text-xs text-destructive">{incidentsQuery.error.message}</p>
        ) : null}
        {updateIncidentMutation.error instanceof Error ? (
          <p className="border-t p-3 text-xs text-destructive">{updateIncidentMutation.error.message}</p>
        ) : null}
      </CardContent>
    </Card>
  );
}

function GatewayPolicyExceptions({
  canManage,
  wsId,
}: {
  canManage: boolean;
  wsId: string;
}) {
  const qc = useQueryClient();
  const exceptionsQuery = useQuery(gatewayPolicyExceptionsOptions(wsId, 20, canManage));
  const rows = exceptionsQuery.data ?? [];
  const approveExceptionMutation = useMutation({
    mutationFn: (id: string) =>
      api.updateGatewayPolicyException(id, {
        status: "approved",
        expires_at: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: gatewayKeys.policyExceptions(wsId, 20) });
      void qc.invalidateQueries({ queryKey: gatewayKeys.policyDecisions(wsId, 20) });
      void qc.invalidateQueries({ queryKey: gatewayKeys.audit(wsId, 20) });
    },
  });

  if (!canManage) return null;

  return (
    <Card size="sm" className="rounded-lg">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm">
          <ShieldCheck className="size-4 text-muted-foreground" />
          Policy Exceptions
        </CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        {exceptionsQuery.isLoading ? (
          <div className="space-y-2 p-3">
            {Array.from({ length: 3 }).map((_, index) => (
              <Skeleton key={index} className="h-12 rounded-md" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <div className="flex h-32 flex-col items-center justify-center border-t text-center">
            <ShieldCheck className="size-8 text-muted-foreground/40" />
            <p className="mt-3 text-sm font-medium">No policy exceptions requested</p>
            <p className="mt-1 text-xs text-muted-foreground">Temporary provider approvals and other governance overrides will appear here.</p>
          </div>
        ) : (
          <Table aria-label="Gateway policy exceptions">
            <TableHeader>
              <TableRow>
                <TableHead>Status</TableHead>
                <TableHead>Reason</TableHead>
                <TableHead>Scope</TableHead>
                <TableHead className="text-right">Expires</TableHead>
                <TableHead className="text-right">Action</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row: GatewayPolicyExceptionItem) => (
                <TableRow key={row.id}>
                  <TableCell>
                    <Badge variant={row.status === "approved" ? "secondary" : "outline"}>{row.status}</Badge>
                  </TableCell>
                  <TableCell>
                    <span className="line-clamp-2 max-w-xl text-sm font-medium">{row.reason}</span>
                  </TableCell>
                  <TableCell>{policyExceptionScopeLabel(row.scope)}</TableCell>
                  <TableCell className="text-right text-xs text-muted-foreground">{formatNullableTime(row.expires_at)}</TableCell>
                  <TableCell className="text-right">
                    {row.status === "requested" ? (
                      <Button
                        type="button"
                        size="xs"
                        variant="outline"
                        disabled={approveExceptionMutation.isPending && approveExceptionMutation.variables === row.id}
                        onClick={() => approveExceptionMutation.mutate(row.id)}
                        aria-label="Approve exception"
                      >
                        <ShieldCheck className="size-3.5" />
                        Approve
                      </Button>
                    ) : (
                      <Badge variant="outline">{row.status}</Badge>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {exceptionsQuery.error instanceof Error ? (
          <p className="border-t p-3 text-xs text-destructive">{exceptionsQuery.error.message}</p>
        ) : null}
        {approveExceptionMutation.error instanceof Error ? (
          <p className="border-t p-3 text-xs text-destructive">{approveExceptionMutation.error.message}</p>
        ) : null}
      </CardContent>
    </Card>
  );
}

function IngestKeySetup({ wsId }: { wsId: string }) {
  const qc = useQueryClient();
  const [appId, setAppId] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [createdKey, setCreatedKey] = useState<GatewayIngestKeyResponse | null>(null);
  const keysQuery = useQuery(gatewayIngestKeysOptions(wsId));
  const keys = keysQuery.data ?? [];
  const activeKeys = keys.filter((key) => !key.revoked_at).length;

  const createdEnv = createdKey
    ? [
        `MULTICA_OBSERVER_GATEWAY_BASE_URL=${createdKey.gateway_base_url}`,
        `MULTICA_OBSERVER_KEY=${createdKey.key}`,
        `MULTICA_OBSERVER_APP_ID=${createdKey.app_id}`,
      ].join("\n")
    : "";

  const createMutation = useMutation({
    mutationFn: (input: { app_id?: string; display_name?: string }) =>
      api.createGatewayIngestKey(input),
    onSuccess: (key) => {
      setCreatedKey(key);
      setAppId("");
      setDisplayName("");
      void qc.invalidateQueries({ queryKey: gatewayKeys.ingestKeys(wsId) });
    },
  });

  const revokeMutation = useMutation({
    mutationFn: (id: string) => api.revokeGatewayIngestKey(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: gatewayKeys.ingestKeys(wsId) });
    },
  });

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    createMutation.mutate({
      app_id: appId.trim() || undefined,
      display_name: displayName.trim() || undefined,
    });
  };

  const copyCreatedKey = () => {
    if (typeof navigator !== "undefined" && navigator.clipboard && createdEnv) {
      void navigator.clipboard.writeText(createdEnv);
    }
  };

  return (
    <div className="space-y-3">
      <div className="grid gap-3 xl:grid-cols-[360px_minmax(0,1fr)]">
        <Card size="sm" className="rounded-lg">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <KeyRound className="size-4 text-muted-foreground" />
              Create Ingest Key
            </CardTitle>
          </CardHeader>
          <CardContent>
            <form className="space-y-3" onSubmit={submit}>
              <div className="space-y-1.5">
                <Label htmlFor="gateway-ingest-app-id">App ID</Label>
                <Input
                  id="gateway-ingest-app-id"
                  value={appId}
                  onChange={(event) => setAppId(event.target.value)}
                  placeholder="checkout-api"
                  autoComplete="off"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="gateway-ingest-name">Name</Label>
                <Input
                  id="gateway-ingest-name"
                  value={displayName}
                  onChange={(event) => setDisplayName(event.target.value)}
                  placeholder="Checkout API"
                  autoComplete="off"
                />
              </div>
              {createMutation.error instanceof Error ? (
                <p className="text-xs text-destructive">{createMutation.error.message}</p>
              ) : null}
              <Button
                type="submit"
                size="sm"
                disabled={!appId.trim() || createMutation.isPending}
              >
                <KeyRound className="size-3.5" />
                Create ingest key
              </Button>
            </form>
          </CardContent>
        </Card>

        <Card size="sm" className="rounded-lg">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <ShieldCheck className="size-4 text-muted-foreground" />
              SDK Connection
            </CardTitle>
          </CardHeader>
          <CardContent>
            {createdKey ? (
              <div className="space-y-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div>
                    <p className="text-sm font-medium">{createdKey.display_name || createdKey.app_id}</p>
                    <p className="text-xs text-muted-foreground">Copy this secret now. It will not be shown again.</p>
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={copyCreatedKey}
                    aria-label="Copy generated ingest key"
                  >
                    <Copy className="size-3.5" />
                    Copy
                  </Button>
                </div>
                <pre className="max-h-40 overflow-auto rounded-md border bg-muted/30 p-3 text-xs leading-relaxed">
                  {createdEnv}
                </pre>
              </div>
            ) : (
              <div className="grid gap-3 md:grid-cols-3">
                <div className="rounded-md border p-3">
                  <p className="text-xs text-muted-foreground">Active keys</p>
                  <p className="mt-1 text-2xl font-semibold tabular-nums">{activeKeys}</p>
                </div>
                <div className="rounded-md border p-3">
                  <p className="text-xs text-muted-foreground">Key type</p>
                  <p className="mt-1 text-sm font-medium">mig_ ingest</p>
                </div>
                <div className="rounded-md border p-3">
                  <p className="text-xs text-muted-foreground">Routing</p>
                  <p className="mt-1 text-sm font-medium">Gateway traces</p>
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <Card size="sm" className="rounded-lg">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-sm">
            <KeyRound className="size-4 text-muted-foreground" />
            Ingest Keys
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {keysQuery.isLoading ? (
            <div className="space-y-2 p-3">
              {Array.from({ length: 3 }).map((_, index) => (
                <Skeleton key={index} className="h-12 rounded-md" />
              ))}
            </div>
          ) : keys.length === 0 ? (
            <div className="flex h-40 flex-col items-center justify-center border-t text-center">
              <KeyRound className="size-8 text-muted-foreground/40" />
              <p className="mt-3 text-sm font-medium">No ingest keys</p>
              <p className="mt-1 text-xs text-muted-foreground">Create a key for application traces and SDK events.</p>
            </div>
          ) : (
            <Table aria-label="Gateway ingest keys">
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>App ID</TableHead>
                  <TableHead>Prefix</TableHead>
                  <TableHead>Last used</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Action</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {keys.map((key) => {
                  const revoked = Boolean(key.revoked_at);
                  return (
                    <TableRow key={key.id}>
                      <TableCell>
                        <div className="flex max-w-56 flex-col">
                          <span className="truncate font-medium">{ingestKeyName(key)}</span>
                          <span className="truncate text-xs text-muted-foreground">{formatTime(key.created_at)}</span>
                        </div>
                      </TableCell>
                      <TableCell>{key.app_id}</TableCell>
                      <TableCell>
                        <code className="rounded bg-muted px-1.5 py-0.5 text-xs">{key.key_prefix}</code>
                      </TableCell>
                      <TableCell>{formatNullableTime(key.last_used_at)}</TableCell>
                      <TableCell>
                        <Badge variant={revoked ? "outline" : "secondary"}>
                          {revoked ? "revoked" : "active"}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          type="button"
                          size="icon-xs"
                          variant="ghost"
                          disabled={revoked || revokeMutation.isPending}
                          onClick={() => revokeMutation.mutate(key.id)}
                          aria-label={`Revoke ${ingestKeyName(key)} ingest key`}
                          title={`Revoke ${ingestKeyName(key)} ingest key`}
                        >
                          <Trash2 className="size-3.5" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
          {keysQuery.error instanceof Error ? (
            <p className="border-t p-3 text-xs text-destructive">{keysQuery.error.message}</p>
          ) : null}
        </CardContent>
      </Card>
    </div>
  );
}

function SpanWaterfall({ spans }: { spans: GatewaySpanObservation[] }) {
  const maxDuration = Math.max(...spans.map((s) => s.duration_ms ?? 0), 1);
  if (spans.length === 0) {
    return <p className="text-xs text-muted-foreground">No spans recorded for this session.</p>;
  }
  return (
    <div className="space-y-2">
      {spans.map((span) => {
        const width = Math.max(6, Math.round(((span.duration_ms ?? 0) / maxDuration) * 100));
        const depth = span.parent_span_id ? 16 : 0;
        return (
          <div key={span.id} className="space-y-1">
            <div className="flex items-center justify-between gap-2 text-xs" style={{ paddingLeft: depth }}>
              <span className="truncate font-medium">{span.span_name}</span>
              <span className="shrink-0 text-muted-foreground tabular-nums">{formatMS(span.duration_ms)}</span>
            </div>
            <div className="h-1.5 rounded-full bg-muted" style={{ marginLeft: depth }}>
              <div
                className={cn("h-full rounded-full", span.span_type === "llm" ? "bg-info" : "bg-foreground/60")}
                style={{ width: `${width}%` }}
              />
            </div>
          </div>
        );
      })}
    </div>
  );
}

function PayloadPreview({ title, value }: { title: string; value: unknown }) {
  if (value == null) return null;
  return (
    <div className="space-y-1">
      <p className="text-xs font-medium text-muted-foreground">{title}</p>
      <pre className="max-h-44 overflow-auto rounded-md border bg-muted/30 p-2 text-xs leading-relaxed">
        {JSON.stringify(value, null, 2)}
      </pre>
    </div>
  );
}

function ModelCallCard({ call }: { call: GatewayModelCallObservation }) {
  return (
    <div className="space-y-3 rounded-lg border p-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">{call.response_model || call.request_model}</p>
          <p className="text-xs text-muted-foreground">{call.provider_slug} · {formatCount(call.total_tokens)} tokens · {formatMS(call.time_to_generate_ms)}</p>
        </div>
        <Badge variant="outline">{call.usage_source}</Badge>
      </div>
      <PayloadPreview title="Prompt" value={call.prompt_messages} />
      <PayloadPreview title="Completion" value={call.completion_messages} />
    </div>
  );
}

function SessionDrilldown({
  selectedSession,
  detail,
  spans,
  loading,
}: {
  selectedSession: GatewaySessionListItem | undefined;
  detail: GatewaySessionDetail | undefined;
  spans: GatewaySpanObservation[];
  loading: boolean;
}) {
  if (!selectedSession) {
    return (
      <aside className="flex h-full flex-col items-center justify-center border-l p-6 text-center text-muted-foreground">
        <Boxes className="size-10 text-muted-foreground/30" />
        <p className="mt-3 text-sm">Select a session to inspect Gateway behavior.</p>
      </aside>
    );
  }

  if (loading && !detail) {
    return (
      <aside className="h-full overflow-y-auto border-l p-4">
        <Skeleton className="h-5 w-36" />
        <div className="mt-4 space-y-3">
          {Array.from({ length: 5 }).map((_, index) => (
            <Skeleton key={index} className="h-20 rounded-lg" />
          ))}
        </div>
      </aside>
    );
  }

  return (
    <aside className="h-full overflow-y-auto border-l bg-background">
      <div className="sticky top-0 z-10 border-b bg-background/95 p-4 backdrop-blur">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Session Drilldown</p>
            <h2 className="mt-1 truncate text-sm font-semibold">{selectedSession.name}</h2>
            <p className="mt-1 truncate text-xs text-muted-foreground">{selectedSession.trace_id}</p>
          </div>
          <Badge variant={statusVariant(selectedSession.status)}>{selectedSession.status}</Badge>
        </div>
      </div>

      <div className="space-y-5 p-4">
        <section className="grid grid-cols-2 gap-2 text-xs">
          <div className="rounded-md border p-2">
            <p className="text-muted-foreground">Duration</p>
            <p className="mt-1 font-medium tabular-nums">{formatDuration(selectedSession.duration_ms)}</p>
          </div>
          <div className="rounded-md border p-2">
            <p className="text-muted-foreground">Cost</p>
            <p className="mt-1 font-medium tabular-nums">{formatMoney(selectedSession.usage_cost)}</p>
          </div>
          <div className="rounded-md border p-2">
            <p className="text-muted-foreground">Model calls</p>
            <p className="mt-1 font-medium tabular-nums">{selectedSession.llm_call_count}</p>
          </div>
          <div className="rounded-md border p-2">
            <p className="text-muted-foreground">Tokens</p>
            <p className="mt-1 font-medium tabular-nums">{formatCount(selectedSession.total_tokens)}</p>
          </div>
        </section>

        <section className="space-y-3">
          <div className="flex items-center gap-2">
            <TerminalSquare className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">Session Waterfall</h3>
          </div>
          <SpanWaterfall spans={spans} />
        </section>

        <section className="space-y-3">
          <div className="flex items-center gap-2">
            <MessageSquareText className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">LLM Calls</h3>
          </div>
          {(detail?.model_calls ?? []).map((call) => (
            <ModelCallCard key={call.id} call={call} />
          ))}
        </section>

        <section className="space-y-2">
          <div className="flex items-center gap-2">
            <Bot className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">Agents</h3>
          </div>
          {(detail?.agents ?? []).length === 0 ? (
            <p className="text-xs text-muted-foreground">No agent observations recorded.</p>
          ) : (
            detail?.agents.map((agent) => (
              <div key={agent.id} className="rounded-md border p-2 text-sm">
                <p className="font-medium">{agent.agent_name || agent.agent_id}</p>
                <p className="text-xs text-muted-foreground">{agent.role} · {agent.reasoning_summary}</p>
              </div>
            ))
          )}
        </section>

        <section className="space-y-2">
          <div className="flex items-center gap-2">
            <Wrench className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">Tools</h3>
          </div>
          {(detail?.tools ?? []).length === 0 ? (
            <p className="text-xs text-muted-foreground">No tool observations recorded.</p>
          ) : (
            detail?.tools.map((tool) => (
              <div key={tool.id} className="rounded-md border p-2 text-sm">
                <div className="flex items-center justify-between gap-2">
                  <p className="font-medium">{tool.tool_name || tool.tool_id}</p>
                  <div className="flex items-center gap-2">
                    {tool.canonical_tool_type ? <Badge variant="outline">{tool.canonical_tool_type}</Badge> : null}
                    {tool.tool_risk_level ? <Badge variant="outline">{tool.tool_risk_level} risk</Badge> : null}
                    <Badge variant={statusVariant(tool.status)}>{tool.status}</Badge>
                  </div>
                </div>
                <p className="mt-1 text-xs text-muted-foreground">{tool.description}</p>
              </div>
            ))
          )}
        </section>

        <section className="space-y-2">
          <div className="flex items-center gap-2">
            <AlertTriangle className="size-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">Logs</h3>
          </div>
          {(detail?.logs ?? []).length === 0 ? (
            <p className="text-xs text-muted-foreground">No logs recorded.</p>
          ) : (
            detail?.logs.map((log) => (
              <div key={log.id} className="rounded-md border p-2 text-xs">
                <Badge variant={statusVariant(log.severity)}>{log.severity}</Badge>
                <p className="mt-2">{log.body}</p>
              </div>
            ))
          )}
        </section>
      </div>
    </aside>
  );
}

export function GatewayPage() {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const user = useAuthStore((state) => state.user);
  const [timeWindow, setTimeWindow] = useState("24h");
  const [selectedSessionId, setSelectedSessionId] = useState("");
  const filters = useMemo(() => ({ since: timeWindow, limit: 50 }), [timeWindow]);

  const membersQuery = useQuery(memberListOptions(wsId));
  const overviewQuery = useQuery(gatewayOverviewOptions(wsId, filters));
  const sessionsQuery = useQuery(gatewaySessionsOptions(wsId, filters));
  const llmCallsQuery = useQuery(gatewayLLMCallsOptions(wsId, filters));
  const currentMember = membersQuery.data?.find((member) => member.user_id === user?.id);
  const memberRole = currentMember?.role;
  const permissionsLoading = membersQuery.isLoading;
  const canManage = !permissionsLoading && canManageGateway(memberRole);

  const sessionRows = sessionsQuery.data?.sessions;
  const sessions = useMemo(() => sessionRows ?? [], [sessionRows]);
  useEffect(() => {
    if (!selectedSessionId && sessions.length > 0) {
      setSelectedSessionId(sessions[0]!.id);
    }
  }, [selectedSessionId, sessions]);

  const selectedSession = sessions.find((session) => session.id === selectedSessionId) ?? sessions[0];
  const effectiveSelectedId = selectedSession?.id ?? "";
  const detailQuery = useQuery(gatewaySessionDetailOptions(wsId, effectiveSelectedId));
  const spansQuery = useQuery(gatewaySessionSpansOptions(wsId, effectiveSelectedId));

  const refresh = () => {
    qc.invalidateQueries({ queryKey: gatewayKeys.all(wsId) });
  };

  return (
    <div className="flex min-h-0 flex-1">
      <main className="min-w-0 flex-1 overflow-y-auto">
        <div className="border-b px-5 py-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <div className="flex items-center gap-2">
                <Eye className="size-5 text-muted-foreground" />
                <h1 className="text-lg font-semibold">Gateway</h1>
              </div>
              <p className="mt-1 text-sm text-muted-foreground">
                Observer Gateway captures model traffic according to workspace policy.
              </p>
            </div>
            <div className="flex items-center gap-2">
              <div className="flex rounded-md border bg-background p-0.5">
                {windowOptions.map((option) => (
                  <button
                    key={option.value}
                    type="button"
                    onClick={() => setTimeWindow(option.value)}
                    className={cn(
                      "rounded px-2.5 py-1 text-xs font-medium transition-colors",
                      timeWindow === option.value
                        ? "bg-muted text-foreground"
                        : "text-muted-foreground hover:text-foreground",
                    )}
                  >
                    {option.label}
                  </button>
                ))}
              </div>
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={refresh}
                title="Refresh Gateway data"
                aria-label="Refresh Gateway data"
              >
                <RefreshCw className="size-4" />
              </Button>
            </div>
          </div>
        </div>

        <div className="space-y-4 p-5">
          <OverviewSection
            overview={overviewQuery.data ?? undefined}
            loading={overviewQuery.isLoading}
          />

          <Tabs defaultValue="sessions" className="min-h-[360px]">
            <div className="flex items-center justify-between gap-3">
              <TabsList>
                <TabsTrigger value="sessions">Sessions</TabsTrigger>
                <TabsTrigger value="llm-calls">LLM Calls</TabsTrigger>
                <TabsTrigger value="governance">Governance</TabsTrigger>
                <TabsTrigger value="setup">Setup</TabsTrigger>
              </TabsList>
              <p className="text-xs text-muted-foreground">
                {formatCount(sessionsQuery.data?.total ?? 0)} sessions · {formatCount(llmCallsQuery.data?.total ?? 0)} calls
              </p>
            </div>
            <TabsContent value="sessions" className="mt-3 rounded-lg border">
              {sessionsQuery.isLoading ? (
                <div className="space-y-2 p-3">
                  {Array.from({ length: 5 }).map((_, index) => (
                    <Skeleton key={index} className="h-12 rounded-md" />
                  ))}
                </div>
              ) : (
                <SessionsTable
                  sessions={sessions}
                  selectedId={effectiveSelectedId}
                  onSelect={setSelectedSessionId}
                />
              )}
            </TabsContent>
            <TabsContent value="llm-calls" className="mt-3 rounded-lg border">
              {llmCallsQuery.isLoading ? (
                <div className="space-y-2 p-3">
                  {Array.from({ length: 5 }).map((_, index) => (
                    <Skeleton key={index} className="h-12 rounded-md" />
                  ))}
                </div>
              ) : (
                <LLMCallsTable
                  calls={llmCallsQuery.data?.calls ?? []}
                  onSelectSession={setSelectedSessionId}
                />
              )}
            </TabsContent>
            <TabsContent value="governance" className="mt-3">
              <GatewayGovernanceInsights canManage={canManage} wsId={wsId} />
            </TabsContent>
            <TabsContent value="setup" className="mt-3">
              <div className="space-y-4">
                <GatewayHealthPanel canManage={canManage} wsId={wsId} />
                <GatewayConfigurationSetup
                  canManage={canManage}
                  memberRole={memberRole}
                  permissionsLoading={permissionsLoading}
                  wsId={wsId}
                />
                <GatewayGovernanceSetup canManage={canManage} wsId={wsId} />
                <GatewayGovernancePolicies canManage={canManage} wsId={wsId} />
                <GatewayComplianceControls canManage={canManage} wsId={wsId} />
                <GatewayIncidents canManage={canManage} wsId={wsId} />
                <GatewayPolicyExceptions canManage={canManage} wsId={wsId} />
                <GatewayPolicyDecisions canManage={canManage} wsId={wsId} />
                <GatewayEvidence canManage={canManage} wsId={wsId} />
                <GatewayAuditHistory canManage={canManage} wsId={wsId} />
                {canManage ? <IngestKeySetup wsId={wsId} /> : null}
              </div>
            </TabsContent>
          </Tabs>
        </div>
      </main>

      <div className="hidden w-[390px] shrink-0 lg:block">
        <SessionDrilldown
          selectedSession={selectedSession}
          detail={detailQuery.data ?? undefined}
          spans={spansQuery.data?.spans ?? []}
          loading={detailQuery.isLoading || spansQuery.isLoading}
        />
      </div>
    </div>
  );
}
