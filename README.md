# muse-cc — Muse Code subscription proxy

Local reverse proxy that lets Claude Code (and any OpenAI/Codex-compatible
client) use Meta's `muse-spark` models through a **Muse Code subscription**.
No `ZEN_API_KEY`, no Zen billing — the only upstream is `https://api.meta.ai`,
authenticated with the `muse login` credential.

| What | Where |
|---|---|
| Port | `:8788` (never `:8787` — that is opencode-cc) |
| Binary | `~/.local/bin/muse-cc` |
| systemd unit | `~/.config/systemd/user/muse-cc.service` |
| Env file | `~/.config/muse-cc/env` (mode 600) |
| Data dir | `~/.local/share/muse-cc` |
| Go proxy + embedded dashboard | this repo (`main.go`, `internal/`, `web/`) |

## 1. Overview

`muse-cc` accepts the same three API surfaces clients already speak and serves
them from Meta, natively on every surface (all verified live with `200`s):

- `POST /v1/messages` — Anthropic Messages API → Meta `/v1/messages`
  passthrough (content blocks, `thinking`, `tool_use`/`tool_result`,
  `stop_reason` preserved byte-for-byte).
- `POST /v1/chat/completions` — OpenAI Chat Completions passthrough.
- `POST /v1/responses` — Codex Responses API passthrough (Meta's recommended
  full-featured surface: reasoning replay, tool loops, search grounding).
- `GET /v1/models` — live upstream list, 60 s TTL cache, static fallback.
- `GET /api/*` + `/` — JSON control API and the embedded React panel.

Non-Meta targets configured via the upstream pool still use the translation
paths (Messages↔Chat, Responses↔Chat, Responses↔Messages bridge); the
Anthropic↔Responses bridge also remains as the fallback when native routing
is disabled.

## 2. Quick start

```bash
# 1. Log the Muse CLI in (one time; creates ~/.config/muse/auth.json).
muse login

# 2. Run the proxy. No key export needed: it auto-syncs the login credential.
muse-cc -data ~/.local/share/muse-cc   # listens on :8788

# 3. Point Claude Code at it.
export ANTHROPIC_BASE_URL=http://localhost:8788
export ANTHROPIC_AUTH_TOKEN=anything
claude
```

Claude Code's default `claude-*` model ids are mapped to
`muse-spark-1.3-contributor` automatically; explicit `muse-*` ids pass
through verbatim. Prefer an explicit model? `export ANTHROPIC_MODEL=muse-spark-1.3`.

Build from source: `./build.sh` (panel + embedded binary) or
`go build -o /tmp/muse-cc-stage .` for a backend-only binary.

## 3. Endpoints

| Method + path | Auth | Behavior |
|---|---|---|
| `POST /v1/messages` | client key¹ | Native Meta passthrough (default); bridge via `/v1/responses` when `native_anthropic=false`; Chat translation for non-native targets |
| `POST /v1/messages/count_tokens` | client key¹ | Local token estimate |
| `POST /v1/chat/completions` | client key¹ | Model rewrite + passthrough; `stream_options.include_usage` defaulted on streams |
| `POST /v1/responses` | client key¹ | Native passthrough for `muse-spark*` (input dedupe, `tool_choice` coercion, effort clamp); translation for other targets |
| `GET /v1/models` | client key¹ | Live upstream ids, 60 s cache, contributor fallback |
| `GET/PUT /api/config` + `/api/stats/*` `/api/logs` `/api/keys` `/api/test` | panel token² | Control API (keys always masked) |
| `GET /` | panel token² | Embedded dashboard (or dev placeholder without assets) |

¹ Gated only when `require_api_key=true` (default off for single-user use).
² Open when `panel_token` is empty; set one before exposing the port.

## 4. Subscription auth (how it was discovered — no guessing)

Sources, in the order they were consulted:

1. `muse --help` / `muse login --help`: OAuth device-code login;
   "`META_API_KEY` always takes priority over the account login";
   `--base-url` overrides the provider base URL.
2. `~/.local/bin/muse` (launcher script): credential path resolution
   (`$MUSE_AUTH_PATH` → `$XDG_CONFIG_HOME/muse/auth.json` →
   `~/.config/muse/auth.json`), `https://auth.meta.com` device flow.
3. `~/.config/muse/auth.json` (this machine): `providers.meta.api_base_url`
   is `https://api.meta.ai/v1`; it carries **two** credentials —
   `access_token` (`dca:…`, OAuth) and `api_key` (`LLM|…`, 48 chars).
4. Live probes (no secret ever printed, only status codes):
   - `GET /v1/models` with `Bearer <access_token>` → **401**
     `invalid_api_key`; with `Bearer <api_key>` → **200** listing
     `muse-spark-1.3-contributor`, `muse-spark-1.3`, `muse-spark-1.2-*`,
     `muse-spark-1.1`, `muse-voice-transcribe-1.0`.
   - `POST /v1/messages` (Bearer + `anthropic-version`) → 200 native
     Anthropic shape; `/v1/chat/completions` → 200; `/v1/responses` →
     200; SSE streams on all three; unknown model → 404 `model_not_found`.
5. Published integrations: Meta Model API docs via mozilla-ai/any-llm#1250
   (Chat+Responses on `https://api.meta.ai/v1`, Messages on
   `https://api.meta.ai`, Bearer on **both** — never `x-api-key`;
   `stop_sequences`/`top_k` 400 on Messages), lidge-jun/opencodex#3321
   (`openai-responses` provider on `https://api.meta.ai/v1`),
   theopenco/llmgateway#2985 (Chat redacts `reasoning_content`; Responses
   is the full-featured surface).

Resulting behavior:

- Upstream auth is `Authorization: Bearer <providers.meta.api_key>`.
  Key precedence: `MUSE_API_KEY` → `META_API_KEY` → `MODEL_API_KEY` →
  synced login → panel `upstreams[]` → legacy `muse_api_key`.
- `sync_muse_auth` (default on) stats `~/.config/muse/auth.json` on every
  upstream selection and adopts a changed `api_key` immediately, so a fresh
  `muse login` or rotation takes effect with no restart. Custom
  multi-entry pools are never overwritten; disable with
  `MUSE_CC_SYNC_MUSE_AUTH=0` or the Settings toggle to pin a key.
- Keys are never printed: logs carry at most path + length, the panel API
  only serves masked views, and `~/.config/muse-cc/env` must stay mode 600.

## 5. Models

- `GET /v1/models` proxies the upstream list live (20 s fetch timeout) and
  caches it for 60 s. When Meta is unreachable it serves the last good list,
  else the static fallback `muse-spark-1.3-contributor` — the endpoint never
  errors.
- Default mapping: `muse-*` passes through verbatim;
  `*` → `muse-spark-1.3-contributor` (this is what makes stock Claude Code
  work with zero model flags). Edit on the Models page or via `PUT /api/config`.
- Reasoning effort: Meta accepts `minimal/low/medium/high/xhigh` on
  Responses (live-verified; `none`/`max`/unknown 400). Passthrough clamps
  client-supplied efforts into the target's known scale, learns corrections
  from upstream `400`s into `<datadir>/reasoning-levels.json`, and demotes
  rejected tops — all best-effort, never a failed request. The Anthropic
  bridge defaults to `minimal` effort for fast plain turns.
- Heads-up: Meta spends ~13 reasoning tokens before the first content token
  at default effort, so tiny `max_tokens` budgets (≤16) can return empty
  content with `finish_reason=length`. The panel test probe uses 64.

## 6. Web search

Three modes (`web_search_mode`, default `auto`):

- `auto` — native-capable targets (`muse-*`) pass `web_search` blocks
  straight to Meta `/v1/messages`; anything else uses the proxy shim.
- `native` — force pass-through to `/v1/messages` (optionally with a
  dedicated `web_search_base_url` + key).
- `translate` — the proxy shim: the model picks a query via a `web_search`
  function call, the proxy runs provider search, then a second turn compiles
  the answer with `server_tool_use` / `web_search_result` blocks and
  `web_search_requests` usage. Works streamed and non-streamed.

On the Responses surface, `tools:[{"type":"web_search"}]` is accepted
natively by Meta (verified 200) and passed through untouched.

## 7. Streaming & latency

- Streams relay bytes unbuffered with a flush on every write; response
  headers (`text/event-stream`, `no-cache`, `X-Accel-Buffering: no`) go out
  the moment the upstream connects, so `message_start` reaches the client
  with no proxy-added delay. Measured direct-to-Meta TTFB: ~1.8 s
  (Messages SSE), ~2.6 s (Chat SSE).
- The Responses relay strips only `event: ping` keepalive frames (some
  clients fail the turn on the unknown variant); SSE `:` comments pass
  through as keepalives.
- Upstream transport is HTTP/1.1-only with a 30 s response-header timeout:
  one stalled connection can never wedge every route (Sep 2026 forensics).
  Non-stream requests additionally get a 300 s cap (configurable).
- Prompt-cache forwarding stays on by default: `prompt_cache_key`
  (+`prompt_cache_retention`) on OpenAI-family requests, automatic
  `cache_control` breakpoints and prefix normalization on native Anthropic
  requests, and sticky upstream selection so caches stay warm.

## 8. Deploy

Systemd user service (see `README_DEPLOY.md` for the full runbook):

```bash
# env (mode 600; optional when `muse login` is active — sync covers it)
printf 'MUSE_API_KEY=LLM|...\n' > ~/.config/muse-cc/env
chmod 600 ~/.config/muse-cc/env

systemctl --user daemon-reload
systemctl --user enable --now muse-cc.service
systemctl --user is-active muse-cc.service
loginctl enable-linger $USER
```

Safe deploys always go through staging first: build
`go build -o /tmp/muse-cc-stage .`, run it on `127.0.0.1:18788` with
`-data /tmp/mc-stage-data`, E2E chat + stream + TTFB + web search + models
+ an isolated Claude Code (`HOME=/tmp/cc-iso-home`), then swap the live
binary (stop first — `cp` fails with "Text file busy" otherwise), smoke
test `:8788`, and kill staging. Never run two copies on `:8788`.

Config validation: `config.json` must parse as JSON (the loader merges it
over defaults — unknown fields are ignored, corrupt files abort startup
with an error); verify the unit with
`systemd-analyze verify ~/.config/systemd/user/muse-cc.service`.

## 9. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `401` from upstream | Key rejected: check `muse login` freshness, `MUSE_API_KEY` value, or pool entries. The `dca:` credential never works on the model API — sync only adopts `api_key`. |
| `404 model_not_found` | Non-`muse-*` target reached Meta verbatim; check Model Routes (the default catch-all prevents this). |
| Empty content, `length`/`max_tokens` stop | Reasoning ate the budget; raise `max_tokens` (≥64 for probes) or lower effort. |
| `400` on Messages with `stop_sequences`/`top_k` | Meta rejects those fields (documented); drop them client-side. |
| `bind: address already in use` | A second copy on `:8788` (repo cwd, `go run`, stale staging). Kill it; the service owns the port. |
| Panel shows "Not configured" | No key anywhere: `muse login`, or set `MUSE_API_KEY`, or add an upstream row. |
| `Text file busy` on deploy | Stop the service before copying the binary over it. |
| Slow first byte | Normal: Meta TTFB is ~2 s; the proxy adds nothing measurable. Check `reasoning.effort` for hot models. |

Logs: `journalctl --user -u muse-cc -f`. Request history lives in the
panel (Logs page) and `<datadir>/muse-cc.db`.
