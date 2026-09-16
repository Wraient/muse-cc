# muse-cc — deploy runbook

Local Meta proxy on `http://127.0.0.1:8788`: Claude Code and OpenAI/Codex
clients talk to `muse-spark` models on the Muse Code subscription.
Upstream `https://api.meta.ai`, honest `muse-cc/1.x` User-Agent, Bearer auth.

## File map

| What | Where |
|---|---|
| Binary | `~/.local/bin/muse-cc` |
| Source | `~/Projects/muse-cc` |
| systemd user unit | `~/.config/systemd/user/muse-cc.service` |
| API key env file | `~/.config/muse-cc/env` (`MUSE_API_KEY=LLM|...`, chmod 600; optional while `muse login` sync is active) |
| Data dir | `~/.local/share/muse-cc` (`muse-cc.db`, `reasoning-levels.json`, optional `config.json`) |
| Subscription login | `~/.config/muse/auth.json` (read-only source for auto-refresh) |

## Service management

```bash
systemctl --user daemon-reload
systemctl --user enable --now muse-cc.service
systemctl --user is-active muse-cc.service
systemctl --user restart muse-cc.service
journalctl --user -u muse-cc -f
loginctl enable-linger $USER
```

## Staging → live (every deploy)

```bash
cd ~/Projects/muse-cc
go build ./... && go test ./...
go build -o /tmp/muse-cc-stage .
export $(grep -E '^(MUSE_API_KEY|META_API_KEY|MODEL_API_KEY|MUSE_CC_)' ~/.config/muse-cc/env | xargs)
MUSE_CC_LISTEN='127.0.0.1:18788' nohup /tmp/muse-cc-stage -data /tmp/mc-stage-data >/tmp/mc-stage.log 2>&1 &
# E2E on :18788 — chat, stream, TTFB, web search, /v1/models, isolated claude:
export ANTHROPIC_BASE_URL=http://127.0.0.1:18788 ANTHROPIC_AUTH_TOKEN=anything
HOME=/tmp/cc-iso-home claude -p "reply with exactly: hi" --max-tokens 64
# when green:
systemctl --user stop muse-cc
cp /tmp/muse-cc-stage ~/.local/bin/muse-cc
systemctl --user start muse-cc
curl -s :8788/api/health
pkill -f '[m]use-cc-stage'; rm -rf /tmp/mc-stage-data
```

## Rebuild (panel changes)

```bash
cd ~/Projects/muse-cc
(cd web && npx vite build)
rm -rf internal/assets/dist/assets internal/assets/dist/index.html
cp -r web/dist/. internal/assets/dist/
go build -o /tmp/muse-cc-stage .   # then stage → live as above
```

## Backups

Before overwriting `~/.local/bin/muse-cc`,
`~/.config/systemd/user/muse-cc.service`, or `~/.config/muse-cc/env`,
copy the old file to `<name>.bak-muse-YYYYMMDD` first.
