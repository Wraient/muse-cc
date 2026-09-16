import { useEffect, useState } from "react";
import { api, PanelConfig, UpstreamView } from "../lib/api";
import { PageHeader } from "../App";
import { Badge, Card, Spinner } from "../components/ui";

// One editable upstream row. api_key is the local edit buffer (empty = keep
// existing, matches the backend "empty = don't change" sentinel).
interface UpstreamRow {
  base_url: string;
  api_key: string;
  name: string;
  enabled: boolean;
  api_key_set: boolean;
  api_key_masked: string;
}

const META_BASES = [
  "https://api.meta.ai",
];

export default function Settings() {
  const [cfg, setCfg] = useState<PanelConfig | null>(null);
  const [upstreams, setUpstreams] = useState<UpstreamRow[]>([]);
  const [nativeAnthropic, setNativeAnthropic] = useState(false);
  const [syncMuseAuth, setSyncMuseAuth] = useState(true);
  const [logReqs, setLogReqs] = useState(true);
  const [maxBody, setMaxBody] = useState(16384);
  const [timeout, setTimeoutSecs] = useState(0);
  const [requireKey, setRequireKey] = useState(false);
  const [webSearchModel, setWebSearchModel] = useState("");
  const [webSearchMode, setWebSearchMode] = useState("auto");
  const [webSearchBaseURL, setWebSearchBaseURL] = useState("");
  const [webSearchAPIKey, setWebSearchAPIKey] = useState("");
  const [promptCache, setPromptCache] = useState(true);
  const [promptCacheKeyPrefix, setPromptCacheKeyPrefix] = useState("muse-cc");
  const [promptCacheAnthropicControl, setPromptCacheAnthropicControl] = useState(true);
  const [promptCacheNormalize, setPromptCacheNormalize] = useState(true);
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [flash, setFlash] = useState("");

  // Panel password state
  const [newPanelToken, setNewPanelToken] = useState("");
  const [confirmPanelToken, setConfirmPanelToken] = useState("");
  const [panelTokenError, setPanelTokenError] = useState("");
  const [panelTokenFlash, setPanelTokenFlash] = useState("");

  useEffect(() => {
    api.getConfig().then((c) => {
      setCfg(c);
      setNativeAnthropic(c.native_anthropic);
      setSyncMuseAuth(c.sync_muse_auth);
      setLogReqs(c.log_requests);
      setMaxBody(c.max_body_log_bytes);
      setTimeoutSecs(c.request_timeout_seconds);
      setRequireKey(c.require_api_key);
      setWebSearchModel(c.web_search_model || "");
      setWebSearchMode(c.web_search_mode || "auto");
      setWebSearchBaseURL(c.web_search_base_url || "");
      setWebSearchAPIKey("");
      setPromptCache(c.prompt_cache_enabled);
      setPromptCacheKeyPrefix(c.prompt_cache_key_prefix || "muse-cc");
      setPromptCacheAnthropicControl(c.prompt_cache_anthropic_control);
      setPromptCacheNormalize(c.prompt_cache_normalize);
      // Seed the upstreams editor from the server view.
      const rows: UpstreamRow[] = (c.upstreams && c.upstreams.length ? c.upstreams : []).map((u) => ({
        base_url: u.base_url,
        api_key: "",
        name: u.name,
        enabled: u.enabled,
        api_key_set: u.api_key_set,
        api_key_masked: u.api_key_masked,
      }));
      setUpstreams(rows);
    });
  }, []);

  async function savePanelToken() {
    setPanelTokenError("");
    if (newPanelToken !== confirmPanelToken) {
      setPanelTokenError("Passwords do not match");
      return;
    }
    setSaving(true);
    try {
      const updated = await api.putConfig({ panel_token: newPanelToken });
      setCfg(updated);
      setNewPanelToken("");
      setConfirmPanelToken("");
      setPanelTokenFlash(newPanelToken === "" ? "Panel password cleared." : "Panel password updated.");
      setTimeout(() => setPanelTokenFlash(""), 2500);
      // If a password was just set, reload so AuthGuard picks up the new state.
      if (newPanelToken !== "") setTimeout(() => window.location.reload(), 1000);
    } finally {
      setSaving(false);
    }
  }

  async function save() {
    setSaving(true);
    try {
      const body: Record<string, unknown> = {
        native_anthropic: nativeAnthropic,
        sync_muse_auth: syncMuseAuth,
        log_requests: logReqs,
        max_body_log_bytes: maxBody,
        request_timeout_seconds: timeout,
        require_api_key: requireKey,
        web_search_model: webSearchModel.trim(),
        web_search_mode: webSearchMode,
        web_search_base_url: webSearchBaseURL.trim(),
        web_search_api_key: webSearchAPIKey.trim(),
        prompt_cache_enabled: promptCache,
        prompt_cache_key_prefix: promptCacheKeyPrefix,
        prompt_cache_anthropic_control: promptCacheAnthropicControl,
        prompt_cache_normalize: promptCacheNormalize,
        upstreams: upstreams.map((u) => ({
          base_url: u.base_url,
          // empty api_key = keep existing (backend sentinel); only send typed value
          api_key: u.api_key.trim(),
          name: u.name,
          enabled: u.enabled,
        })),
      };
      const updated = await api.putConfig(body);
      setCfg(updated);
      setWebSearchBaseURL(updated.web_search_base_url || "");
      setWebSearchAPIKey("");
      // Refresh masked key views from the server response.
      if (updated.upstreams) {
        setUpstreams(
          updated.upstreams.map((u) => ({
            base_url: u.base_url,
            api_key: "",
            name: u.name,
            enabled: u.enabled,
            api_key_set: u.api_key_set,
            api_key_masked: u.api_key_masked,
          }))
        );
      }
      setDirty(false);
      setFlash("Saved.");
      setTimeout(() => setFlash(""), 2500);
    } finally {
      setSaving(false);
    }
  }

  // Upstream editor helpers
  function updateUpstream(i: number, patch: Partial<UpstreamRow>) {
    setUpstreams((prev) => prev.map((u, idx) => (idx === i ? { ...u, ...patch } : u)));
    setDirty(true);
  }
  function addUpstream() {
    setUpstreams((prev) => [
      ...prev,
      {
        base_url: META_BASES[0],
        api_key: "",
        name: "",
        enabled: true,
        api_key_set: false,
        api_key_masked: "",
      },
    ]);
    setDirty(true);
  }
  function removeUpstream(i: number) {
    setUpstreams((prev) => prev.filter((_, idx) => idx !== i));
    setDirty(true);
  }

  if (!cfg) {
    return (
      <div className="flex items-center justify-center py-24 text-slate-500 gap-2">
        <Spinner /> Loading…
      </div>
    );
  }

  return (
    <div className="animate-fade-in max-w-3xl">
      <PageHeader
        title="Settings"
        desc="Upstream credentials and proxy behavior."
        actions={
          <div className="flex items-center gap-3">
            {flash && <span className="text-xs text-accent-green">{flash}</span>}
            <button onClick={save} disabled={saving || !dirty} className="btn-primary">
              {saving ? <Spinner /> : <SaveIcon />}
              Save
            </button>
          </div>
        }
      />

      <Card className="mb-4">
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center gap-2">
            <KeyIcon />
            <h3 className="text-sm font-semibold text-slate-200">Upstream credentials (round-robin)</h3>
            {upstreams.filter((u) => u.enabled && (u.api_key_set || u.api_key.trim())).length > 0 ? (
              <Badge tone="green">{upstreams.filter((u) => u.enabled && (u.api_key_set || u.api_key.trim())).length} available</Badge>
            ) : (
              <Badge tone="amber">Not configured</Badge>
            )}
          </div>
          <button onClick={addUpstream} className="btn-ghost !py-1.5 !text-xs">
            + Add upstream
          </button>
        </div>

        <p className="text-xs text-slate-500 mb-4">
          Multiple upstream API keys rotate per request. The preset Base URL is the Meta Model API
          (<span className="font-mono text-slate-400">https://api.meta.ai</span>); choose "Custom" for any
          OpenAI-compatible endpoint. Keys are stored locally only and never sent anywhere except upstream.
        </p>

        {upstreams.length === 0 ? (
          <div className="text-sm text-slate-500 py-4 text-center">
            No upstreams yet. Click "+ Add upstream" to get started.
          </div>
        ) : (
          <div className="space-y-3">
            {upstreams.map((u, i) => (
              <div key={i} className="rounded-xl bg-white/[0.03] border border-white/[0.05] p-3">
                <div className="flex items-start gap-3">
                  <div className="flex-1 grid grid-cols-1 sm:grid-cols-2 gap-2">
                    <div>
                      <label className="label">Base URL</label>
                      <select
                        className="input font-mono mb-2"
                        value={META_BASES.includes(u.base_url) ? u.base_url : "__custom__"}
                        onChange={(e) => {
                          if (e.target.value === "__custom__") {
                            // Switch to custom mode with a blank URL if it was a preset.
                            if (META_BASES.includes(u.base_url)) {
                              updateUpstream(i, { base_url: "" });
                            }
                            return;
                          }
                          updateUpstream(i, { base_url: e.target.value });
                        }}
                      >
                        {META_BASES.map((b) => (
                          <option key={b} value={b}>{b}</option>
                        ))}
                        <option value="__custom__">
                          {META_BASES.includes(u.base_url) ? "Custom…" : "Custom (edit below)"}
                        </option>
                      </select>
                      {!META_BASES.includes(u.base_url) && (
                        <input
                          className="input font-mono"
                          placeholder="https://your-custom-host/v1"
                          value={u.base_url}
                          onChange={(e) => updateUpstream(i, { base_url: e.target.value.trim() })}
                        />
                      )}
                    </div>
                    <div>
                      <label className="label">Name</label>
                      <input
                        className="input"
                        placeholder="e.g. subscription"
                        value={u.name}
                        onChange={(e) => updateUpstream(i, { name: e.target.value })}
                      />
                    </div>
                  </div>
                  <button
                    onClick={() => removeUpstream(i)}
                    className="btn-ghost !px-2.5 !py-2 text-slate-500 hover:text-accent-red shrink-0 mt-5"
                    title="Delete"
                  >
                    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                      <path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" strokeLinecap="round" strokeLinejoin="round" />
                    </svg>
                  </button>
                </div>
                <div className="mt-2 flex items-center gap-2">
                  <div className="flex-1">
                    <label className="label">API Key {u.api_key_set ? `(current: ${u.api_key_masked})` : ""}</label>
                    <input
                      type="password"
                      className="input font-mono"
                      placeholder={u.api_key_set ? "Leave empty to keep, or enter a new key" : "Paste your Muse API key (LLM|...)"}
                      value={u.api_key}
                      onChange={(e) => updateUpstream(i, { api_key: e.target.value })}
                    />
                  </div>
                  <label className="flex items-center gap-2 cursor-pointer mt-5 shrink-0">
                    <button
                      type="button"
                      onClick={() => updateUpstream(i, { enabled: !u.enabled })}
                      className={`relative w-11 h-6 rounded-full transition-colors ${u.enabled ? "bg-accent" : "bg-ink-600"}`}
                    >
                      <span className={`absolute top-0.5 left-0.5 w-5 h-5 rounded-full bg-white transition-transform ${u.enabled ? "translate-x-5" : ""}`} />
                    </button>
                    <span className="text-xs text-slate-400">Enabled</span>
                  </label>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      <Card className="mb-4">
        <div className="flex items-center gap-2 mb-4">
          <SlidersIcon />
          <h3 className="text-sm font-semibold text-slate-200">Behavior</h3>
        </div>

        <Toggle
          label="Sync key from Muse login"
          desc="Auto-refresh the upstream key from ~/.config/muse/auth.json when it changes, so a fresh muse login takes effect without a restart. Custom multi-entry upstream pools are never overwritten."
          checked={syncMuseAuth}
          onChange={(v) => {
            setSyncMuseAuth(v);
            setDirty(true);
          }}
        />

        <div className="mt-4">
          <Toggle
            label="Smart native Anthropic routing"
          desc="When on, muse-* target models talk to upstream /v1/messages directly (thinking and tool_use preserved exactly); other targets keep using translation mode."
          checked={nativeAnthropic}
          onChange={(v) => {
            setNativeAnthropic(v);
            setDirty(true);
          }}
        />
        </div>

        <div className="mt-4">
          <Toggle
            label="Log requests"
            desc="Record every proxied request and its translated response for the panel."
            checked={logReqs}
            onChange={(v) => {
              setLogReqs(v);
              setDirty(true);
            }}
          />
        </div>

        <div className="mt-4">
          <Toggle
            label="Require API key"
            desc="When on, /v1/* proxy endpoints require a valid client key (see the API Keys page). Create a key before enabling this."
            checked={requireKey}
            onChange={(v) => {
              setRequireKey(v);
              setDirty(true);
            }}
          />
        </div>

        {nativeAnthropic && (
          <p className="mt-3 rounded-xl border border-amber-400/20 bg-amber-400/10 px-3 py-2 text-xs text-amber-100">
            Make sure the current upstream Base URL supports <span className="font-mono">/v1/messages</span>.
            Non-native Anthropic targets never use this direct path.
          </p>
        )}

        <div className="grid grid-cols-2 gap-4 mt-4">
          <div>
            <label className="label">Max logged body size (bytes)</label>
            <input
              type="number"
              className="input font-mono"
              value={maxBody}
              min={0}
              onChange={(e) => {
                setMaxBody(Number(e.target.value));
                setDirty(true);
              }}
            />
          </div>
          <div>
            <label className="label">Upstream timeout (seconds, 0 = no limit)</label>
            <input
              type="number"
              className="input font-mono"
              value={timeout}
              min={0}
              onChange={(e) => {
                setTimeoutSecs(Number(e.target.value));
                setDirty(true);
              }}
            />
          </div>
        </div>

        <div className="grid grid-cols-2 gap-4 mt-4">
          <div>
            <label className="label">Web search mode</label>
            <select
              className="input"
              value={webSearchMode}
              onChange={(e) => {
                setWebSearchMode(e.target.value);
                setDirty(true);
              }}
            >
              <option value="auto">Auto</option>
              <option value="native">Native</option>
              <option value="translate">Translate</option>
            </select>
            <p className="text-xs text-slate-500 mt-2">
              Native can use a dedicated Anthropic upstream; Translate searches via the proxy and compiles results.
            </p>
          </div>
          <div>
            <label className="label">Web search model (empty = follow main model)</label>
            <input
              className="input font-mono"
              placeholder="e.g. muse-spark-1.3"
              value={webSearchModel}
              onChange={(e) => {
                setWebSearchModel(e.target.value);
                setDirty(true);
              }}
            />
            <p className="text-xs text-slate-500 mt-2">
              Used by both Native and Translate; empty follows the main model.
            </p>
          </div>
        </div>

        <div className="grid grid-cols-2 gap-4 mt-4">
          <div>
            <label className="label">Web search native upstream</label>
            <input
              className="input font-mono"
              placeholder="https://api.meta.ai"
              value={webSearchBaseURL}
              onChange={(e) => {
                setWebSearchBaseURL(e.target.value);
                setDirty(true);
              }}
            />
            <p className="text-xs text-slate-500 mt-2">
              Native mode only; empty reuses the main upstream.
            </p>
          </div>
          <div>
            <label className="label">
              Web Search API Key
              {cfg?.web_search_api_key_set ? (
                <span className="ml-2 text-xs text-slate-500">Set {cfg.web_search_api_key_masked}</span>
              ) : null}
            </label>
            <input
              className="input font-mono"
              placeholder={cfg?.web_search_api_key_set ? "Empty = keep current key" : "Meta API key (empty = reuse main)"}
              type="password"
              value={webSearchAPIKey}
              onChange={(e) => {
                setWebSearchAPIKey(e.target.value);
                setDirty(true);
              }}
            />
            <p className="text-xs text-slate-500 mt-2">
              Leaving it empty will not overwrite the saved search key.
            </p>
          </div>
        </div>
      </Card>

      <Card className="mb-4">
        <div className="flex items-center gap-2 mb-4">
          <CacheIcon />
          <h3 className="text-sm font-semibold text-slate-200">Prompt cache tuning</h3>
          {promptCache ? <Badge tone="green">Enabled</Badge> : <Badge tone="amber">Disabled</Badge>}
        </div>

        <Toggle
          label="Enable cache friendliness"
          desc="Auto-add prompt_cache_key and stabilize tool, system/developer prefix, and adjacent file-context ordering."
          checked={promptCache}
          onChange={(v) => {
            setPromptCache(v);
            setDirty(true);
          }}
        />

        <div className="mt-4">
          <label className="label">prompt_cache_key prefix</label>
          <input
            className="input font-mono"
            value={promptCacheKeyPrefix}
            onChange={(e) => {
              setPromptCacheKeyPrefix(e.target.value);
              setDirty(true);
            }}
          />
          <p className="text-xs text-slate-500 mt-2">
            The proxy derives keys from model, tool set, and stable system prefix; this prefix distinguishes proxy instances.
          </p>
        </div>

        <div className="mt-4">
          <Toggle
            label="Automatic Anthropic cache_control"
            desc="For native Anthropic upstream requests with a stable system/tool prefix but no cache_control, auto-add an ephemeral cache breakpoint."
            checked={promptCacheAnthropicControl}
            onChange={(v) => {
              setPromptCacheAnthropicControl(v);
              setDirty(true);
            }}
          />
        </div>

        <div className="mt-4">
          <Toggle
            label="Normalize cache prefix"
            desc="Strip non-prompt noise fields like request_id/timestamp and pin tool, system/developer, and file-context ordering."
            checked={promptCacheNormalize}
            onChange={(v) => {
              setPromptCacheNormalize(v);
              setDirty(true);
            }}
          />
        </div>
      </Card>

      <Card className="mb-4">
        <div className="flex items-center gap-2 mb-4">
          <LockIcon />
          <h3 className="text-sm font-semibold text-slate-200">Panel access password</h3>
          {cfg.panel_token_set ? <Badge tone="green">Set</Badge> : <Badge tone="amber">Not set (open access)</Badge>}
        </div>
        <p className="text-xs text-slate-500 mb-4">
          Once set, the control panel requires login-page authentication. Save an empty password to clear it and restore open access.
        </p>

        <label className="label">New password</label>
        <input
          type="password"
          className="input mb-3"
          placeholder="Enter a new password, empty clears"
          value={newPanelToken}
          autoComplete="new-password"
          onChange={(e) => setNewPanelToken(e.target.value)}
        />

        <label className="label">Confirm new password</label>
        <input
          type="password"
          className="input mb-3"
          placeholder="Enter the new password again"
          value={confirmPanelToken}
          autoComplete="new-password"
          onChange={(e) => setConfirmPanelToken(e.target.value)}
        />

        {panelTokenError && (
          <p className="text-xs text-accent-red mb-3">{panelTokenError}</p>
        )}

        <div className="flex items-center gap-3">
          <button
            onClick={savePanelToken}
            disabled={saving}
            className="btn-primary"
          >
            {saving ? <Spinner /> : <SaveIcon />}
            {newPanelToken === "" ? "Clear password" : "Set password"}
          </button>
          {panelTokenFlash && (
            <span className="text-xs text-accent-green">{panelTokenFlash}</span>
          )}
        </div>
      </Card>

      <Card>
        <div className="flex items-center gap-2 mb-4">
          <TerminalIcon />
          <h3 className="text-sm font-semibold text-slate-200">Connect Claude Code</h3>
        </div>
        <p className="text-sm text-slate-400 mb-3">
          Point Claude Code at this proxy with two environment variables:
        </p>
        <pre className="rounded-xl bg-ink-950/80 border border-white/[0.05] p-4 text-xs font-mono text-slate-300 overflow-x-auto">
{`# Bash / zsh
export ANTHROPIC_BASE_URL=http://localhost:${cfg.listen_addr.split(":").pop() || "8788"}
export ANTHROPIC_AUTH_TOKEN=${requireKey ? "sk-your-client-key" : "anything"}
claude

# PowerShell
$env:ANTHROPIC_BASE_URL="http://localhost:${cfg.listen_addr.split(":").pop() || "8788"}"
$env:ANTHROPIC_AUTH_TOKEN="${requireKey ? "sk-your-client-key" : "anything"}"
claude`}
        </pre>
        <p className="text-xs text-slate-500 mt-3">
          {requireKey
            ? "Client auth is enabled — use a key from the API Keys page as AUTH_TOKEN."
            : "With client auth off, the AUTH_TOKEN value does not matter; the proxy still authenticates upstream with your Meta key."}
        </p>
      </Card>
    </div>
  );
}

function Toggle({
  label,
  desc,
  checked,
  onChange,
}: {
  label: string;
  desc: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="flex items-start justify-between gap-4 cursor-pointer">
      <div>
        <div className="text-sm text-slate-200">{label}</div>
        <div className="text-xs text-slate-500">{desc}</div>
      </div>
      <button
        type="button"
        onClick={() => onChange(!checked)}
        className={`relative w-11 h-6 rounded-full transition-colors shrink-0 mt-0.5 ${
          checked ? "bg-accent" : "bg-ink-600"
        }`}
      >
        <span
          className={`absolute top-0.5 left-0.5 w-5 h-5 rounded-full bg-white transition-transform ${
            checked ? "translate-x-5" : ""
          }`}
        />
      </button>
    </label>
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
function KeyIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-slate-500">
      <path d="M21 2l-2 2m-7.6 7.6a5 5 0 1 1-7.07 7.07 5 5 0 0 1 7.07-7.07zm0 0L15 8m0 0l3 3 3-3-3-3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
function SlidersIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-slate-500">
      <path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6" strokeLinecap="round" />
    </svg>
  );
}
function TerminalIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-slate-500">
      <path d="M4 17l6-6-6-6M12 19h8" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
function LockIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-slate-500">
      <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
      <path d="M7 11V7a5 5 0 0 1 10 0v4" strokeLinecap="round" />
    </svg>
  );
}

function CacheIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-slate-500">
      <ellipse cx="12" cy="5" rx="8" ry="3" />
      <path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5" />
      <path d="M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6" />
    </svg>
  );
}
