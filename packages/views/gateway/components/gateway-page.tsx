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
  KeyRound,
  MessageSquareText,
  RefreshCw,
  Route,
  Server,
  ShieldCheck,
  TerminalSquare,
  Trash2,
  Wrench,
} from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  GatewayIngestKeyListItem,
  GatewayIngestKeyResponse,
  GatewayBackendUsage,
  GatewayOverviewResponse,
  GatewayLLMCallListItem,
  GatewayModelCallObservation,
  GatewayModelUsage,
  GatewayOverviewBucket,
  GatewaySessionDetail,
  GatewaySessionListItem,
  GatewaySpanObservation,
} from "@multica/core/types";
import { useWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";
import {
  gatewayKeys,
  gatewayIngestKeysOptions,
  gatewayLLMCallsOptions,
  gatewayOverviewOptions,
  gatewaySessionDetailOptions,
  gatewaySessionSpansOptions,
  gatewaySessionsOptions,
} from "@multica/core/gateway/queries";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
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

function ingestKeyName(key: GatewayIngestKeyListItem): string {
  return key.display_name || key.app_id || key.key_prefix;
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
  const [timeWindow, setTimeWindow] = useState("24h");
  const [selectedSessionId, setSelectedSessionId] = useState("");
  const filters = useMemo(() => ({ since: timeWindow, limit: 50 }), [timeWindow]);

  const overviewQuery = useQuery(gatewayOverviewOptions(wsId, filters));
  const sessionsQuery = useQuery(gatewaySessionsOptions(wsId, filters));
  const llmCallsQuery = useQuery(gatewayLLMCallsOptions(wsId, filters));

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
              <IngestKeySetup wsId={wsId} />
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
