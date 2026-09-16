import { useEffect, useState } from "react";
import {
  api,
  HourPoint,
  Latency,
  ModelUsagePoint,
  Summary,
} from "../lib/api";
import { fmtMs, fmtNum } from "../lib/format";
import { PageHeader } from "../App";
import { Card, EmptyState, Spinner, StatCard, Badge } from "../components/ui";
import { RequestsAreaChart, TokensAreaChart } from "../components/charts";

export default function Dashboard() {
  const [summary, setSummary] = useState<Summary | null>(null);
  const [hourly, setHourly] = useState<HourPoint[]>([]);
  const [latency, setLatency] = useState<Latency | null>(null);
  const [models, setModels] = useState<ModelUsagePoint[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string>("");

  async function refreshStats() {
    try {
      const [s, h, l, m] = await Promise.all([
        api.summary(),
        api.hourly(24),
        api.latency(),
        api.models(24),
      ]);
      setSummary(s);
      setHourly(h);
      setLatency(l);
      setModels(m);
      setErr("");
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setLoading(false);
    }
  }

  async function refresh() {
    refreshStats();
  }

  useEffect(() => {
    refreshStats();
    const statsID = setInterval(refreshStats, 8000);
    return () => {
      clearInterval(statsID);
    };
  }, []);

  if (loading && !summary) {
    return (
      <div className="flex items-center justify-center py-24 text-slate-500 gap-2">
        <Spinner /> loading…
      </div>
    );
  }

  const errorRate = summary?.requests_last_24h
    ? (summary.errors_last_24h / summary.requests_last_24h) * 100
    : 0;
  const inputTokens = summary?.total_input_tokens ?? 0;
  const cachedInputTokens = summary?.total_cached_input_tokens ?? 0;
  const cacheCreationTokens = summary?.total_cache_creation_input_tokens ?? 0;
  const cacheHitRate = inputTokens ? (cachedInputTokens / inputTokens) * 100 : 0;

  const maxModelReq = Math.max(1, ...models.map((m) => m.requests));

  return (
    <div className="animate-fade-in">
      <PageHeader
        title="Dashboard"
        desc="Live proxy traffic, token usage, and upstream latency."
        actions={
          <button onClick={refresh} className="btn-ghost">
            <RefreshIcon /> Refresh
          </button>
        }
      />

      {err && (
        <div className="mb-5 rounded-xl border border-accent-red/20 bg-accent-red/10 px-4 py-3 text-sm text-accent-red">
          Cannot reach the panel API: {err}
        </div>
      )}

      {/* Stat row */}
      <div className="grid grid-cols-2 lg:grid-cols-5 gap-4 mb-6">
        <StatCard
          label="Requests · 24h"
          value={fmtNum(summary?.requests_last_24h ?? 0)}
          sub={`${fmtNum(summary?.total_requests ?? 0)} total`}
          accent="text-white"
        />
        <StatCard
          label="Error rate · 24h"
          value={`${errorRate.toFixed(1)}%`}
          sub={`${fmtNum(summary?.errors_last_24h ?? 0)} errors`}
          accent={errorRate > 5 ? "text-accent-red" : "text-accent-green"}
        />
        <StatCard
          label="Tokens · 24h"
          value={fmtNum(
            (summary?.total_input_tokens ?? 0) + (summary?.total_output_tokens ?? 0)
          )}
          sub={`In ${fmtNum(summary?.total_input_tokens ?? 0)} · Out ${fmtNum(
            summary?.total_output_tokens ?? 0
          )}`}
          accent="text-accent-cyan"
        />
        <StatCard
          label="Cache hits"
          value={`${cacheHitRate.toFixed(1)}%`}
          sub={`${fmtNum(cachedInputTokens)} hits · ${fmtNum(cacheCreationTokens)} written`}
          accent={cacheHitRate > 0 ? "text-accent-green" : "text-slate-400"}
        />
        <StatCard
          label="Latency p95"
          value={latency ? fmtMs(latency.p95) : "—"}
          sub={
            latency
              ? `p50 ${fmtMs(latency.p50)} · p99 ${fmtMs(latency.p99)}`
              : "No data"
          }
          accent="text-accent-glow"
        />
      </div>

      {/* Charts */}
      <div className="grid grid-cols-1 xl:grid-cols-2 gap-4 mb-6">
        <Card>
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-sm font-semibold text-slate-200">Hourly requests</h3>
            <div className="flex gap-2">
              <Badge tone="violet">Requests</Badge>
              <Badge tone="red">Errors</Badge>
            </div>
          </div>
          {hourly.length > 0 ? (
            <RequestsAreaChart data={hourly} />
          ) : (
            <EmptyState title="No traffic yet" hint="Send a request through the proxy to fill this chart." />
          )}
        </Card>
        <Card>
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-sm font-semibold text-slate-200">Hourly tokens</h3>
            <div className="flex gap-2">
              <Badge tone="cyan">Input</Badge>
              <Badge tone="green">Output</Badge>
            </div>
          </div>
          {hourly.length > 0 ? (
            <TokensAreaChart data={hourly} />
          ) : (
            <EmptyState title="No tokens yet" hint="Token usage shows here once requests complete." />
          )}
        </Card>
      </div>

      {/* Model breakdown */}
      <Card>
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-sm font-semibold text-slate-200">Model usage · 24h</h3>
          <Badge>{models.length} models</Badge>
        </div>
        {models.length === 0 ? (
          <EmptyState title="No model usage yet" hint="Every resolved target model is counted here." />
        ) : (
          <div className="space-y-3">
            {models.map((m) => (
              <div key={m.model} className="flex items-center gap-4">
                <div className="w-40 shrink-0 truncate font-mono text-sm text-slate-300">
                  {m.model}
                </div>
                <div className="flex-1 h-7 rounded-lg bg-white/[0.03] overflow-hidden">
                  <div
                    className="h-full bg-gradient-to-r from-accent/70 to-accent-cyan/60 transition-all"
                    style={{ width: `${(m.requests / maxModelReq) * 100}%` }}
                  />
                </div>
                <div className="w-24 text-right text-sm text-slate-400 tabular-nums">
                  {fmtNum(m.requests)} reqs
                </div>
                <div className="w-24 text-right text-sm text-slate-500 tabular-nums">
                  {fmtNum(m.tokens)} tok
                </div>
                <div className="w-24 text-right text-xs text-accent-green/80 tabular-nums">
                  {m.cached_input_tokens ? `${fmtNum(m.cached_input_tokens)} cached` : "—"}
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}

function RefreshIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M3 12a9 9 0 0 1 15-6.7L21 8M21 3v5h-5M21 12a9 9 0 0 1-15 6.7L3 16M3 21v-5h5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
