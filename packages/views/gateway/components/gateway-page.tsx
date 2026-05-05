"use client";

import { useEffect, useMemo, useState } from "react";
import type React from "react";
import {
  Activity,
  AlertTriangle,
  Bot,
  Boxes,
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
} from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  CreateGatewayBackendRequest,
  GatewayAuditLogItem,
  GatewayBackend,
  GatewayCapturePolicy,
  GatewayIngestKeyListItem,
  GatewayIngestKeyResponse,
  GatewayStatusResponse,
  GatewayUserKeyResponse,
  GatewayBackendUsage,
  GatewayOverviewResponse,
  GatewayLLMCallListItem,
  GatewayModelCallObservation,
  GatewayModelUsage,
  GatewayOverviewBucket,
  GatewaySessionDetail,
  GatewaySessionListItem,
  GatewaySpanObservation,
  GatewayProviderRisk,
  UpsertGatewayProviderRiskRequest,
  UpdateGatewayBackendRequest,
} from "@multica/core/types";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";
import {
  gatewayKeys,
  gatewayAuditOptions,
  gatewayBackendsOptions,
  gatewayIngestKeysOptions,
  gatewayLLMCallsOptions,
  gatewayOverviewOptions,
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
}) {
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

          if (isEditing) {
            return (
              <TableRow key={backend.id}>
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
            );
          }

          return (
            <TableRow key={backend.id}>
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

      <div className="grid gap-3 xl:grid-cols-3">
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
                  <Badge variant={statusVariant(tool.status)}>{tool.status}</Badge>
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
            <TabsContent value="setup" className="mt-3">
              <div className="space-y-4">
                <GatewayConfigurationSetup
                  canManage={canManage}
                  memberRole={memberRole}
                  permissionsLoading={permissionsLoading}
                  wsId={wsId}
                />
                <GatewayGovernanceSetup canManage={canManage} wsId={wsId} />
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
