# AGENTS.md — muse-cc

## Live proxy: DO NOT touch directly

- The production proxy is `muse-cc.service` (systemd user unit).
  Binary: `~/.local/bin/muse-cc`, port **:8788**, data dir
  `~/.local/share/muse-cc`, env `~/.config/muse-cc/env` (mode 600).
- **Never run a second copy on :8788** (repo cwd, manual `go run`, another
  binary). The service will crash-loop with `bind: address already in use`
  and every client loses its key. This has happened before — do not repeat it.
- `:8787` is **opencode-cc**, a different service with a different upstream.
  Do not stop, restart, or point muse-cc staging at it.

## Safe deploy procedure (mandatory)

1. `go build ./... && go test ./...` in the repo.
2. Build a staging binary: `go build -o /tmp/muse-cc-stage .`
3. Run staging on a **spare port with an isolated data dir**, key from env:
   ```
   export $(grep -E '^(MUSE_API_KEY|META_API_KEY|MODEL_API_KEY|MUSE_CC_)' ~/.config/muse-cc/env | xargs)
   MUSE_CC_LISTEN='127.0.0.1:18788' nohup /tmp/muse-cc-stage -data /tmp/mc-stage-data >/tmp/mc-stage.log 2>&1 &
   ```
   (When `muse login` is active, staging also works with no env at all via
   subscription sync — but test the env path too.)
4. E2E-test through staging on **:18788**: chat + stream + TTFB +
   web search + `/v1/models` + the feature under test, plus an isolated
   Claude Code (`HOME=/tmp/cc-iso-home`, staging as base URL).
5. Only when staging is green: `systemctl --user stop muse-cc`,
   copy the staging binary over `~/.local/bin/muse-cc`
   (cp fails with "Text file busy" while the service runs — stop first),
   then `systemctl --user start muse-cc`.
6. Smoke-test live: `curl :8788/api/health` + one real `/v1/messages` call.
7. Kill staging (`pkill` with a bracket pattern like `[m]use-cc-stage`
   so pkill doesn't match its own command line) and remove `/tmp/mc-stage-data`
   if it holds junk.

## Auth rules (non-negotiable)

- Upstream is the Muse Code subscription ONLY: `https://api.meta.ai` with
  `providers.meta.api_key` (`LLM|...`). Never configure a Zen key/URL here.
- The `dca:` OAuth credential 401s on the model API — sync adopts `api_key`
  only (`internal/config/museauth.go`). Do not "fix" that.
- Never print keys: logs carry path + length at most, the panel serves
  masked views only, and `~/.config/muse-cc/env` stays mode 600.

## Video ingress limits (learned Sep 2026 — do not regress)

- Plain local `.mp4` paths in user text are read off disk and sent upstream
  as inline base64 `input_video`. Code: `internal/proxy/video.go`.
- Upstream `POST /v1/responses` degrades hard with inline size (staging
  probes): 2MB→11s, 5MB→18s, 7MB→65s w/ retry, **10MB→never responds**
  (~110s of header-timeout retries, then 502). There is NO duration limit —
  only size matters (bitrate × seconds).
- A hung video request holds 1 of only 2 muse-spark upstream slots
  (`museSparkUpstreamSlots`), so **one big video wedges all other threads**.
- Current guards: files over 4MB are ffmpeg-transcoded toward ~4MB
  (480p/crf28 → 360p/crf32, full duration kept; ffmpeg best-effort, never a
  hard dep); anything still over 8MB (`maxVideoBytes`) fails FAST with a
  clear error instead of hanging. Resolved videos are cached by
  path+size+mtime (4 entries) so repeat turns don't re-transcode.
- `[[video ~/...]]` markers expand `~`; a `document` URL block only counts
  as video when its media type is empty or `video/*` (else upstream tries to
  download it as media → `media_url_origin_error` 404).
- When touching video: E2E-test staging with the REAL 10MB file
  (`/home/wraient/Videos/video_2026-08-07_00-50-06.mp4`) via an isolated
  Claude Code (`HOME=/tmp/cc-iso-home`, staging as base URL) — small-file
  tests alone hide this trap.

## Notes

- Before overwriting any existing muse-cc deployment artifact
  (`~/.local/bin/muse-cc`, `~/.config/systemd/user/muse-cc.service`,
  `~/.config/muse-cc/env`), back it up with a `.bak-muse-DATE` suffix
  (e.g. `muse-cc.service.bak-muse-20260916`). Same for `AGENTS.md` when
  replacing it in place.
- Live config is env-only (`~/.config/muse-cc/env` may not even need a key
  while subscription sync is on); there is normally no `config.json` in
  `~/.local/share/muse-cc`. Staging must source the same env file.
- Commit messages: prefix with `muse-cc:`.
