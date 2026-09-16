import { useEffect, useState } from "react";
import { api, APIKey, KeyBody } from "../lib/api";
import { fmtNum } from "../lib/format";
import { PageHeader } from "../App";
import { Badge, Card, EmptyState, Spinner } from "../components/ui";

const EMPTY: KeyBody = {
  name: "",
  enabled: true,
  token_quota: 0,
  request_quota: 0,
  daily_token_limit: 0,
  daily_request_limit: 0,
  allowed_ips: "",
  expires_at: 0,
};

export default function Keys() {
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<APIKey | null>(null);
  const [creating, setCreating] = useState(false);
  const [createdPlain, setCreatedPlain] = useState<string | null>(null);
  const [flash, setFlash] = useState("");

  async function refresh() {
    try {
      setKeys(await api.keys());
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    refresh();
  }, []);

  function notify(msg: string) {
    setFlash(msg);
    setTimeout(() => setFlash(""), 2500);
  }

  async function handleCreate(body: KeyBody) {
    const created = await api.createKey(body);
    setCreatedPlain(created.plain_key);
    setCreating(false);
    await refresh();
    notify("Key created");
  }
  async function handleUpdate(id: number, body: KeyBody) {
    await api.updateKey(id, body);
    setEditing(null);
    await refresh();
    notify("Saved");
  }
  async function handleDelete(id: number) {
    if (!confirm("Delete this key? This cannot be undone.")) return;
    await api.deleteKey(id);
    await refresh();
    notify("Deleted");
  }
  async function handleReset(id: number) {
    await api.resetKey(id);
    await refresh();
    notify("Usage reset");
  }

  return (
    <div className="animate-fade-in">
      <PageHeader
        title="API Keys"
        desc="Issue keys to clients, with quotas and IP allowlists."
        actions={
          <div className="flex items-center gap-3">
            {flash && <span className="text-xs text-accent-green">{flash}</span>}
            <button onClick={() => setCreating(true)} className="btn-primary">
              <PlusIcon /> New key
            </button>
          </div>
        }
      />

      {/* How-to hint */}
      <Card className="mb-4">
        <div className="flex items-start gap-3">
          <div className="mt-0.5 text-accent-glow">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <circle cx="12" cy="12" r="10" />
              <path d="M12 16v-4M12 8h.01" strokeLinecap="round" />
            </svg>
          </div>
          <div className="text-sm text-slate-400">
            Clients send the key as <code className="text-accent-glow font-mono">Authorization: Bearer &lt;key&gt;</code>.
            In Claude Code: <code className="text-slate-300 font-mono">set ANTHROPIC_AUTH_TOKEN=sk-xxxx</code>.
            To enforce auth, enable "Require API key" on the Settings page.
          </div>
        </div>
      </Card>

      {loading ? (
        <div className="flex items-center justify-center py-16 text-slate-500 gap-2">
          <Spinner /> Loading…
        </div>
      ) : keys.length === 0 ? (
        <Card>
          <EmptyState
            title="No API keys yet"
            hint="Create a key for clients to reach the proxy; create at least one before enabling auth."
          />
        </Card>
      ) : (
        <div className="space-y-3">
          {keys.map((k) => (
            <KeyRow
              key={k.id}
              k={k}
              onEdit={() => setEditing(k)}
              onDelete={() => handleDelete(k.id)}
              onReset={() => handleReset(k.id)}
            />
          ))}
        </div>
      )}

      {(creating || editing) && (
        <KeyModal
          initial={editing}
          onClose={() => {
            setCreating(false);
            setEditing(null);
          }}
          onSubmit={(body) => (editing ? handleUpdate(editing.id, body) : handleCreate(body))}
        />
      )}

      {createdPlain && (
        <PlainKeyModal plain={createdPlain} onClose={() => setCreatedPlain(null)} />
      )}
    </div>
  );
}

function KeyRow({
  k,
  onEdit,
  onDelete,
  onReset,
}: {
  k: APIKey;
  onEdit: () => void;
  onDelete: () => void;
  onReset: () => void;
}) {
  const tokenPct = k.token_quota > 0 ? Math.min(100, (k.used_tokens / k.token_quota) * 100) : 0;
  const reqPct = k.request_quota > 0 ? Math.min(100, (k.used_requests / k.request_quota) * 100) : 0;

  return (
    <Card className="!p-4">
      <div className="flex flex-wrap items-start justify-between gap-4">
        {/* Left: identity */}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="font-mono text-sm text-slate-200">{k.key_prefix}…</span>
            {k.enabled ? <Badge tone="green">Enabled</Badge> : <Badge tone="red">Disabled</Badge>}
            {(() => {
              const e = expiryBadge(k.expires_at);
              return e ? <Badge tone={e.tone}>{e.text}</Badge> : null;
            })()}
            {k.name && <span className="text-sm text-slate-400">· {k.name}</span>}
          </div>
          <div className="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-xs text-slate-500">
            <span>Total <span className="text-slate-300 font-mono">{fmtNum(k.used_tokens)}</span> tok · <span className="text-slate-300 font-mono">{fmtNum(k.used_requests)}</span> reqs</span>
            <span>Today <span className="text-accent-cyan font-mono">{fmtNum(k.daily_used_tokens)}</span> tok · <span className="text-accent-cyan font-mono">{fmtNum(k.daily_used_requests)}</span> reqs</span>
            {k.allowed_ips && <span>IP allowlist: <span className="font-mono text-slate-400">{k.allowed_ips}</span></span>}
          </div>
        </div>

        {/* Right: actions */}
        <div className="flex items-center gap-1.5 shrink-0">
          <button onClick={onReset} className="btn-ghost !py-1.5 !px-2.5 !text-xs" title="Reset usage">
            Reset
          </button>
          <button onClick={onEdit} className="btn-ghost !py-1.5 !px-2.5 !text-xs">Edit</button>
          <button onClick={onDelete} className="btn-ghost !py-1.5 !px-2.5 !text-xs text-accent-red">Delete</button>
        </div>
      </div>

      {/* Quota bars */}
      {(k.token_quota > 0 || k.request_quota > 0) && (
        <div className="mt-3 grid grid-cols-1 sm:grid-cols-2 gap-3">
          {k.token_quota > 0 && (
            <QuotaBar label="Total token quota" used={k.used_tokens} quota={k.token_quota} pct={tokenPct} />
          )}
          {k.request_quota > 0 && (
            <QuotaBar label="Request quota" used={k.used_requests} quota={k.request_quota} pct={reqPct} />
          )}
        </div>
      )}
      {(k.daily_token_limit > 0 || k.daily_request_limit > 0) && (
        <div className="mt-3 grid grid-cols-1 sm:grid-cols-2 gap-3">
          {k.daily_token_limit > 0 && (
            <QuotaBar label="Daily token limit" used={k.daily_used_tokens} quota={k.daily_token_limit} pct={Math.min(100, (k.daily_used_tokens / k.daily_token_limit) * 100)} />
          )}
          {k.daily_request_limit > 0 && (
            <QuotaBar label="Daily request limit" used={k.daily_used_requests} quota={k.daily_request_limit} pct={Math.min(100, (k.daily_used_requests / k.daily_request_limit) * 100)} />
          )}
        </div>
      )}
    </Card>
  );
}

function QuotaBar({ label, used, quota, pct }: { label: string; used: number; quota: number; pct: number }) {
  const tone = pct > 90 ? "from-accent-red/70 to-accent-red" : pct > 70 ? "from-accent-amber/70 to-accent-amber" : "from-accent/70 to-accent-cyan/60";
  return (
    <div>
      <div className="flex justify-between text-[11px] text-slate-500 mb-1">
        <span>{label}</span>
        <span className="font-mono">{fmtNum(used)} / {fmtNum(quota)}</span>
      </div>
      <div className="h-2 rounded-full bg-white/[0.04] overflow-hidden">
        <div className={`h-full bg-gradient-to-r ${tone} transition-all`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

function KeyModal({
  initial,
  onClose,
  onSubmit,
}: {
  initial: APIKey | null;
  onClose: () => void;
  onSubmit: (body: KeyBody) => void;
}) {
  const [form, setForm] = useState<KeyBody>(
    initial
      ? {
          name: initial.name,
          enabled: initial.enabled,
          token_quota: initial.token_quota,
          request_quota: initial.request_quota,
          daily_token_limit: initial.daily_token_limit,
          daily_request_limit: initial.daily_request_limit,
          allowed_ips: initial.allowed_ips,
          expires_at: initial.expires_at,
        }
      : EMPTY
  );
  const [saving, setSaving] = useState(false);

  // Expiry editor state: derive a friendly mode from the epoch value.
  // modes: "never" | "days" | "date"
  const [expMode, setExpMode] = useState<"never" | "days" | "date">(() => {
    if (!form.expires_at) return "never";
    return "date";
  });
  const [expDays, setExpDays] = useState<number>(30);
  // datetime-local expects yyyy-MM-ddTHH:mm in LOCAL time.
  const [expDate, setExpDate] = useState<string>(() => {
    if (!form.expires_at) return "";
    const d = new Date(form.expires_at * 1000);
    const pad = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  });

  function set<K extends keyof KeyBody>(key: K, val: KeyBody[K]) {
    setForm((f) => ({ ...f, [key]: val }));
  }

  // Recompute expires_at from the chosen mode whenever the mode/inputs change.
  function commitExpiry(mode: "never" | "days" | "date", days = expDays, date = expDate) {
    setExpMode(mode);
    if (mode === "never") {
      set("expires_at", 0);
    } else if (mode === "days") {
      const ts = Math.floor(Date.now() / 1000) + Math.max(1, Math.floor(days)) * 86400;
      set("expires_at", ts);
    } else {
      const t = new Date(date).getTime();
      set("expires_at", isNaN(t) ? 0 : Math.floor(t / 1000));
    }
  }

  async function submit() {
    setSaving(true);
    try {
      await onSubmit(form);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg glass p-6 animate-fade-in max-h-[90vh] overflow-y-auto">
        <div className="flex items-center justify-between mb-5">
          <h3 className="text-base font-semibold text-white">{initial ? "Edit key" : "New API key"}</h3>
          <button onClick={onClose} className="btn-ghost !px-2.5 !py-2">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M18 6L6 18M6 6l12 12" strokeLinecap="round" /></svg>
          </button>
        </div>

        <div className="space-y-4">
          <div>
            <label className="label">Name</label>
            <input className="input" placeholder="e.g. my laptop / coworker" value={form.name} onChange={(e) => set("name", e.target.value)} />
          </div>

          <label className="flex items-center justify-between cursor-pointer">
            <span className="text-sm text-slate-200">Enable this key</span>
            <button type="button" onClick={() => set("enabled", !form.enabled)} className={`relative w-11 h-6 rounded-full transition-colors ${form.enabled ? "bg-accent" : "bg-ink-600"}`}>
              <span className={`absolute top-0.5 left-0.5 w-5 h-5 rounded-full bg-white transition-transform ${form.enabled ? "translate-x-5" : ""}`} />
            </button>
          </label>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="label">Total token quota (0=unlimited)</label>
              <input type="number" min={0} className="input font-mono" value={form.token_quota} onChange={(e) => set("token_quota", Number(e.target.value))} />
            </div>
            <div>
              <label className="label">Request quota (0=unlimited)</label>
              <input type="number" min={0} className="input font-mono" value={form.request_quota} onChange={(e) => set("request_quota", Number(e.target.value))} />
            </div>
            <div>
              <label className="label">Daily token limit (0=unlimited)</label>
              <input type="number" min={0} className="input font-mono" value={form.daily_token_limit} onChange={(e) => set("daily_token_limit", Number(e.target.value))} />
            </div>
            <div>
              <label className="label">Daily request limit (0=unlimited)</label>
              <input type="number" min={0} className="input font-mono" value={form.daily_request_limit} onChange={(e) => set("daily_request_limit", Number(e.target.value))} />
            </div>
          </div>

          <div>
            <label className="label">Expiry</label>
            <div className="flex gap-2 flex-wrap items-center">
              <select
                className="input !w-auto"
                value={expMode}
                onChange={(e) => {
                  const m = e.target.value as "never" | "days" | "date";
                  if (m === "never") commitExpiry("never");
                  else if (m === "days") commitExpiry("days", 30);
                  else commitExpiry("date", expDays, expDate || tomorrowLocal());
                }}
              >
                <option value="never">Never expires</option>
                <option value="days">Expires in N days</option>
                <option value="date">Specific date</option>
              </select>
              {expMode === "days" && (
                <div className="flex items-center gap-2">
                  <input
                    type="number"
                    min={1}
                    className="input font-mono !w-24"
                    value={expDays}
                    onChange={(e) => {
                      const n = Number(e.target.value);
                      setExpDays(n);
                      commitExpiry("days", n);
                    }}
                  />
                  <span className="text-xs text-slate-500">days</span>
                </div>
              )}
              {expMode === "date" && (
                <input
                  type="datetime-local"
                  className="input font-mono !w-auto"
                  value={expDate}
                  onChange={(e) => {
                    setExpDate(e.target.value);
                    commitExpiry("date", expDays, e.target.value);
                  }}
                />
              )}
            </div>
            <p className="text-xs text-slate-500 mt-1.5">
              {form.expires_at
                ? `Expires ${new Date(form.expires_at * 1000).toLocaleString()}`
                : "This key never expires."}
            </p>
          </div>

          <div>
            <label className="label">IP allowlist (comma-separated CIDR, empty=unlimited)</label>
            <input className="input font-mono" placeholder="e.g. 1.2.3.4, 10.0.0.0/8" value={form.allowed_ips} onChange={(e) => set("allowed_ips", e.target.value)} />
            <p className="text-xs text-slate-500 mt-1.5">Single IPs and CIDR ranges supported; X-Forwarded-For is only trusted from a local reverse proxy.</p>
          </div>
        </div>

        <div className="flex justify-end gap-2 mt-6">
          <button onClick={onClose} className="btn-ghost">Cancel</button>
          <button onClick={submit} disabled={saving} className="btn-primary">
            {saving ? <Spinner /> : null}
            {initial ? "Save" : "Create"}
          </button>
        </div>
      </div>
    </div>
  );
}

function PlainKeyModal({ plain, onClose }: { plain: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  function copy() {
    navigator.clipboard?.writeText(plain);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
      <div className="relative w-full max-w-lg glass p-6 animate-fade-in">
        <div className="flex items-center gap-2 mb-2">
          <div className="w-8 h-8 rounded-lg bg-accent-green/15 border border-accent-green/30 flex items-center justify-center">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#34d399" strokeWidth="2.5"><path d="M20 6L9 17l-5-5" strokeLinecap="round" strokeLinejoin="round" /></svg>
          </div>
          <h3 className="text-base font-semibold text-white">Key created</h3>
        </div>
        <p className="text-sm text-accent-amber mb-4">
          ⚠️ This is the full key in plaintext, <b>shown only once</b>. Copy and save it now — you cannot view it again.
        </p>
        <div className="flex gap-2">
          <code className="flex-1 rounded-xl bg-ink-950/80 border border-white/[0.06] px-3 py-2.5 font-mono text-sm text-accent-glow break-all">
            {plain}
          </code>
          <button onClick={copy} className="btn-primary shrink-0">
            {copied ? "Copied" : "Copy"}
          </button>
        </div>
        <div className="flex justify-end mt-5">
          <button onClick={onClose} className="btn-ghost">I've saved it</button>
        </div>
      </div>
    </div>
  );
}

function PlusIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
      <path d="M12 5v14M5 12h14" strokeLinecap="round" />
    </svg>
  );
}

// tomorrowLocal returns a yyyy-MM-ddTHH:mm string for ~24h from now, used as a
// sensible default when switching to the "specific date" expiry mode.
function tomorrowLocal(): string {
  const d = new Date(Date.now() + 24 * 3600 * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// expiryBadge returns a {text, tone} descriptor for a key's expiry state, or
// null when the key never expires.
function expiryBadge(expiresAt: number): { text: string; tone: "amber" | "red" | "cyan" } | null {
  if (!expiresAt) return null;
  const now = Date.now();
  const exp = expiresAt * 1000;
  const diff = exp - now;
  if (diff <= 0) return { text: "Expired", tone: "red" };
  if (diff < 24 * 3600 * 1000) return { text: `${Math.max(1, Math.floor(diff / 3600000))}h left`, tone: "red" };
  const days = Math.floor(diff / (24 * 3600 * 1000));
  if (days <= 7) return { text: `${days}d left`, tone: "amber" };
  const d = new Date(exp);
  return {
    text: `Expires ${d.getMonth() + 1}/${d.getDate()}`,
    tone: "cyan",
  };
}
