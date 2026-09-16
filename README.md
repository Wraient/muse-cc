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

Heads-up: if your `~/.claude/settings.json` pins `env.ANTHROPIC_BASE_URL`
(e.g. to another proxy), that wins over shell exports — use the
`claude-muse` launcher (§10) instead, which overrides backend and model
via claude's own `--settings`/`--model` flags while keeping your normal
config (same MCP, skills, memory).

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
- Heads-up: Meta reasons heavily before the first content token (100+
  thinking tokens even on trivial prompts at default effort, 1600+ on big
  ones), so small `max_tokens` budgets return empty content with
  `length`/`max_tokens` stop — even 128 is not enough; use ≥1024 for
  probes. The panel test probe uses 64 and can legitimately come back
  empty for this reason.

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
  with no proxy-added delay. Measured header TTFB through the proxy:
  ~1–2.5 s (Messages SSE), ~1.2–2.8 s (Chat SSE). Time-to-first-*text*
  is longer on big tasks (~25 s for a ~1200-word guide) because upstream
  thinking is redacted and precedes all text — that gap is Meta reasoning,
  not proxy delay.
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
| Empty content, `length`/`max_tokens` stop | Reasoning ate the budget; raise `max_tokens` (≥1024 — even 128 returns empty on this model) or lower effort. |
| `400` on Messages with `stop_sequences`/`top_k` | Meta rejects those fields (documented); drop them client-side. |
| `400 reasoning_effort 'none' …` | Meta rejects `thinking: {"type": "disabled"}` (it maps to effort `none`). The proxy strips that block for `muse-*` targets automatically; `enabled` with `budget_tokens < max_tokens` passes through and works. |
| `400` with `--effort max` | Meta rejects effort `max` on every surface (verified: `xhigh` 200s, `max` 400s). The proxy clamps `max`→`xhigh` automatically — `reasoning.effort`, `output_config.effort`, and thinking-budget bridges — so `max` always means "most thinking this model allows". |
| `bind: address already in use` | A second copy on `:8788` (repo cwd, `go run`, stale staging). Kill it; the service owns the port. |
| Panel shows "Not configured" | No key anywhere: `muse login`, or set `MUSE_API_KEY`, or add an upstream row. |
| `Text file busy` on deploy | Stop the service before copying the binary over it. |
| Slow first byte | Normal: Meta TTFB is ~2 s; the proxy adds nothing measurable. Check `reasoning.effort` for hot models. |
| Model can't see a video / extracts frames itself | The clip never arrived: use an absolute `~/`-anchored `.mp4` path or `[[video …]]` in user text (relative paths are ignored by design). Native path now splices refs into video blocks — see §12. |

Logs: `journalctl --user -u muse-cc -f`. Request history lives in the
panel (Logs page) and `<datadir>/muse-cc.db`.

## 10. External launchers (t3code): `claude-muse`

`scripts/claude-muse` (installed as a symlink at
`~/.local/bin/claude-muse`) runs the Claude Code CLI against this proxy
while keeping your **normal `~/.claude` config** — same MCP servers,
skills, memory, permissions — with only backend and model switched via
claude's own `--settings`/`--model` flags (verified to override
`settings.json`). Point t3code's "another client" at it:

- **Binary:** `/home/wraient/.local/bin/claude-muse`
- **Launch args:** ` [--upstream URL] [claude args...]` —
  `--upstream` (or `=` form) sets the backend
  (default `http://127.0.0.1:8788`, `ANTHROPIC_BASE_URL` also works);
  `--model`/`--effort` are claude's own flags, passed straight through
  (defaults `muse-spark-1.3-contributor` / `max` are injected when
  omitted, and mirrored into the injected settings for settings-reading
  paths); everything else passes through untouched
  (e.g. `-p "prompt" --max-turns 5`)
- **Env overrides (optional):** `ANTHROPIC_AUTH_TOKEN` /
  `ANTHROPIC_API_KEY` (never clobbered when set),
  `CLAUDE_MUSE_NO_AUTOSTART=1`

Rules: an explicit caller `--settings` wins untouched (combining it with
`--upstream` is an error); the proxy is auto-started only when the
effective backend is this machine's `:8788`; `claude` is resolved even
under minimal-PATH GUI launchers; exit code propagates via `exec`.
Examples: `claude-muse -p "hi"` (defaults),
`claude-muse --model muse-spark-1.2-contributor -p "hi"`,
`claude-muse --upstream http://127.0.0.1:8787 --model <zen-id> -p "hi"`.

## 11. File map & agent notes

File map (repo + deployment):

| Path | What |
|---|---|
| `main.go` | Entrypoint; `-data` flag, config+store init, route wiring |
| `internal/config/` | Config load/merge/env, `museauth.go` (login-key sync) |
| `internal/server/` | Route handlers: `proxy.go` (Messages native/translate), `openai_proxy.go`, `responses_proxy.go` + `responses_passthrough.go`, `web_search_proxy.go`, `reasoning_levels.go` (effort scale/clamp/learn), `thinking.go`, `auth.go` (client keys), `logging.go` (journal access log) |
| `internal/proxy/` | Translators + pure JSON ops: `anthropic*.go`, `openai.go`, `responses*.go`, `reasoning_levels.go` (`ClampEffort`, extract/set effort helpers), `video.go` guards, `models.go` (live list + TTL) |
| `internal/store/` | SQLite request log + client API keys |
| `internal/api/` `internal/assets/` `internal/metrics/` | Panel REST API, embedded SPA serving, counters |
| `web/` | React panel source (`npm run build` → `internal/assets/dist/`, git-ignored) |
| `scripts/claude-muse` | t3code/CLI launcher (symlinked to `~/.local/bin/claude-muse`) |
| `build.sh` / `Dockerfile` / `.github/workflows/` | Panel+Go build, container, CI (`go test ./...` + `go build`) |
| `~/.local/bin/muse-cc` | Live binary (systemd runs this, not the repo) |
| `~/.config/systemd/user/muse-cc.service` | User unit (listens on `:8788` — never `:8787`, that is opencode-cc) |
| `~/.config/muse-cc/env` (600) + `README.md` (symlink here) | Key file + co-located docs |
| `~/.local/share/muse-cc/` | Live data: `config.json`, `muse-cc.db*`, `reasoning-levels.json` |

Notes for the next agent:

- **Deploy ritual is mandatory for proxy changes**: `gofmt`/`go vet`/
  `go test ./...` → staging binary on `127.0.0.1:18788` with fresh
  `-data /tmp/mc-stage-data` → E2E (chat+stream+TTFB+websearch+models+
  isolated `claude -p`) → stop service → back up `~/.local/bin/muse-cc`
  to `.bak-muse-<YYYYMMDD>` → copy → start → `is-active` → smoke live
  `:8788` → kill staging. Live `:8787` (opencode-cc) is never touched.
- **Effort scale is clamp-based, not validated**: Meta rejects literal
  `max`/`none` but lists them in error enums; the proxy clamps to the
  per-model verified top (`xhigh` for `muse-spark`) on every path that
  forwards effort (`reasoning.effort`, `output_config.effort`, thinking
  bridges) and self-heals via invalid-effort learning + demote-retry.
  Never "fix" a 400 by passing effort through raw — re-probe upstream
  first with the sink-capture trick (`/tmp/cap-sink.py` pattern: dummy
  400 server on a spare port shows exactly what the client sent).
- **Thinking-budget rule**: `thinking.budget_tokens` must be `<`
  `max_tokens` or Meta 400s; `thinking.disabled` is stripped for
  `muse-*` for the same reason.
- **Settings precedence**: `~/.claude/settings.json` `env` beats shell
  env; only claude's `--settings`/`--model`/`--effort` CLI flags beat
  settings — that is why `claude-muse` injects flags instead of exports.
- **Model ids are backend-scoped**: `-free` ids 404 on Meta, `muse-*`
  ids pass verbatim, `claude-*` maps to the default contributor model.
- **Known benign warnings**: `[claude-code:unrecognized_model]` for
  `muse-*` ids (works anyway, 200k assumed window); occasional `502`
  on heavy non-stream turns (30 s upstream header timeout — stream).
- **Never print secrets**: logs/answers carry key paths + lengths only;
  request bodies in `<datadir>/muse-cc.db` may contain user prompts —
  query metadata columns, not bodies, unless debugging wire shape.

## 12. Video

Three ingress paths, all delivered natively (never frame-split):

1. Plain absolute/`~/` `.mp4` path in user text
   (`what happens in ~/clip.mp4?`) — read off disk, never touches the
   client's context. Unresolvable paths stay plain text; relative paths
   are ignored by design (the proxy owns no cwd).
2. `[[video <src>]]` markers — remote URLs, `data:` URIs, or forced
   paths. Unresolvable markers fail loudly (400), never silently drop.
3. Explicit `{"type":"video"}` blocks (base64 mp4 or URL source).

Only user-role text is scanned (assistant text and tool results are
untouched, so an `ls` of `*.mp4` never becomes an attachment). Upstream
takes mp4 only: files over 4 MB are ffmpeg-transcoded toward ~4 MB
(full duration, lower resolution) and anything still over the 8 MB
inline cap fails fast instead of hanging upstream. Per surface: the
bridge emits `input_video` parts, the Chat translation emits `video_url`
parts, and the native Messages path splices explicit video blocks
(`ResolveNativeVideoRefs` — Meta accepts them, verified live).
