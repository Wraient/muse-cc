import { useEffect, useState } from "react";
import { api, PanelConfig, TestResult } from "../lib/api";
import { PageHeader } from "../App";
import { Badge, Card, Spinner } from "../components/ui";
import { fmtMs } from "../lib/format";

interface Mapping {
  match: string;
  target: string;
}

// Live Meta models (GET /v1/models, verified), offered as quick targets.
// The text field stays free-form.
const MUSE_SUGGESTIONS = [
  "muse-spark-1.3-contributor",
  "muse-spark-1.3",
  "muse-spark-1.2-contributor",
  "muse-spark-1.2",
  "muse-spark-1.1",
];

export default function Models() {
  const [cfg, setCfg] = useState<PanelConfig | null>(null);
  const [mappings, setMappings] = useState<Mapping[]>([]);
  const [defaultModel, setDefaultModel] = useState("muse-spark-1.3-contributor");
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [savedAt, setSavedAt] = useState(0);

  useEffect(() => {
    api.getConfig().then((c) => {
      setCfg(c);
      setMappings(c.model_mappings?.length ? c.model_mappings : [{ match: "*", target: c.default_model }]);
      setDefaultModel(c.default_model || "muse-spark-1.3-contributor");
    });
  }, []);

  function update(i: number, key: keyof Mapping, val: string) {
    setMappings((prev) => {
      const next = prev.map((m, idx) => (idx === i ? { ...m, [key]: val } : m));
      setDirty(true);
      return next;
    });
  }
  function addRow() {
    setMappings((prev) => [...prev, { match: "", target: "" }]);
    setDirty(true);
  }
  function removeRow(i: number) {
    setMappings((prev) => prev.filter((_, idx) => idx !== i));
    setDirty(true);
  }

  async function save() {
    setSaving(true);
    try {
      const cleaned = mappings.filter((m) => m.match || m.target);
      if (!cleaned.some((m) => m.match === "*")) {
        cleaned.push({ match: "*", target: defaultModel });
      }
      const updated = await api.putConfig({
        default_model: defaultModel,
        model_mappings: cleaned,
      });
      setCfg(updated);
      setMappings(updated.model_mappings);
      setDirty(false);
      setSavedAt(Date.now());
    } finally {
      setSaving(false);
    }
  }

  if (!cfg) {
    return (
      <div className="flex items-center justify-center py-24 text-slate-500 gap-2">
        <Spinner /> Loading…
      </div>
    );
  }

  return (
    <div className="animate-fade-in max-w-4xl">
      <PageHeader
        title="Model Routes"
        desc="Map incoming Anthropic model names to Meta target models. Rules match in order, first hit wins; * is the fallback rule."
        actions={
          <button onClick={save} disabled={!dirty || saving} className="btn-primary">
            {saving ? <Spinner /> : <SaveIcon />}
            {dirty ? "Save" : "Saved"}
          </button>
        }
      />

      <Card className="mb-4">
        <label className="label">Default model (fallback target)</label>
        <div className="flex gap-2 flex-wrap items-center">
          <input
            className="input max-w-xs"
            value={defaultModel}
            onChange={(e) => {
              setDefaultModel(e.target.value);
              setDirty(true);
            }}
            list="muse-suggestions"
          />
          <datalist id="muse-suggestions">
            {MUSE_SUGGESTIONS.map((m) => (
              <option key={m} value={m} />
            ))}
          </datalist>
          <ConnectButton model={defaultModel} />
        </div>
        <p className="text-xs text-slate-500 mt-2">
          Used when no rule matches, and by the * fallback row. Free-form — anything your Meta key can access.
        </p>
      </Card>

      <Card>
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-sm font-semibold text-slate-200">Mapping rules</h3>
          <button onClick={addRow} className="btn-ghost !py-1.5 !text-xs">
            + Add rule
          </button>
        </div>

        <div className="space-y-2">
          <div className="grid grid-cols-[1fr_1fr_auto] gap-3 px-1 pb-1 text-[10px] uppercase tracking-wider text-slate-500">
            <div>Match (prefix or *)</div>
            <div>Target Meta model</div>
            <div />
          </div>
          {mappings.map((m, i) => (
            <div key={i} className="grid grid-cols-[1fr_1fr_auto] gap-3 items-center">
              <input
                className="input font-mono"
                value={m.match}
                placeholder="claude-3-5-sonnet"
                onChange={(e) => update(i, "match", e.target.value)}
              />
              <input
                className="input font-mono"
                value={m.target}
                placeholder={defaultModel}
                list="muse-suggestions"
                onChange={(e) => update(i, "target", e.target.value)}
              />
              <button
                onClick={() => removeRow(i)}
                className="btn-ghost !px-2.5 !py-2 text-slate-500 hover:text-accent-red"
                title="Delete"
              >
                <TrashIcon />
              </button>
            </div>
          ))}
        </div>

        <div className="mt-5 pt-4 border-t border-white/[0.05]">
          <div className="text-xs font-medium text-slate-400 mb-2">Quick ref · common Meta models</div>
          <div className="flex flex-wrap gap-2">
            {MUSE_SUGGESTIONS.map((m) => (
              <button
                key={m}
                onClick={() => {
                  navigator.clipboard?.writeText(m);
                }}
                className="chip font-mono hover:border-accent/40 hover:text-accent-glow transition cursor-pointer"
                title="Click to copy"
              >
                {m}
              </button>
            ))}
          </div>
        </div>
      </Card>
    </div>
  );
}

function ConnectButton({ model }: { model: string }) {
  const [res, setRes] = useState<TestResult | null>(null);
  const [busy, setBusy] = useState(false);

  async function run() {
    setBusy(true);
    setRes(null);
    try {
      setRes(await api.test(model));
    } catch (e) {
      setRes({ ok: false, model, elapsed_ms: 0, error: (e as Error).message });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex items-center gap-2">
      <button onClick={run} disabled={busy} className="btn-ghost !py-2">
        {busy ? <Spinner /> : <BoltIcon />}
        Test connection
      </button>
      {res && (
        <div className="flex items-center gap-2">
          {res.ok ? (
            <Badge tone="green">
              OK · {fmtMs(res.elapsed_ms ?? res.upstreams?.[0]?.elapsed_ms)}
              {(res.preview || res.upstreams?.[0]?.preview) ? ` · "${(res.preview || res.upstreams?.[0]?.preview || "").slice(0, 24)}"` : ""}
            </Badge>
          ) : (
            <Badge tone="red">
              Failed · {res.error?.slice(0, 60)}
            </Badge>
          )}
        </div>
      )}
    </div>
  );
}

function SaveIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" strokeLinejoin="round" />
      <path d="M17 21v-8H7v8M7 3v5h8" strokeLinejoin="round" />
    </svg>
  );
}
function TrashIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
function BoltIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
      <path d="M13 2L3 14h7v8l10-12h-7V2z" />
    </svg>
  );
}
