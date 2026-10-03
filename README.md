# hs-pi-dashboard

[![CI](https://github.com/HemSoft/hs-pi-dashboard/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/HemSoft/hs-pi-dashboard/actions/workflows/ci.yml)

A gold-on-black, fleet-wide dashboard for [pi](https://github.com/badlogic/pi-mono)
coding-agent sessions across the Tailscale network, including Claude Code.
One Go binary, two modes:

```text
┌─ laptop ─┐   ┌─ home ──┐   ┌─ air / grokbot ────┐
│ agent    │   │ agent   │   │ agent (later)      │  watches ~/.pi/agent/sessions
│ :8787    │   │ :8787   │   │ :8787              │  GET /sessions → JSON summaries
└────┬─────┘   └────┬────┘   └─────────┬──────────┘
     └──────────────┼──────────────────┘
        polled every 5s over Tailscale/MagicDNS
          ┌──────────▼──────────────────┐
          │ hs-pi-dashboard serve :8788 │  on mini (always-on)
          └──────────▼──────────────────┘
        http://mini:8788 — machine → project → session cards
```

## What it shows

The live Herdr monitor shows agent states across machines and named Herdr
sessions, with machine and state filters. Collection is read-only and does not
send input, change focus, start a Herdr server, or read terminal transcripts.
Unavailable or stale collectors do not count as live. Agents running only in
tmux are not included in this monitor; session history and Pulse remain separate.

The session table shows each machine, project, provider, model, thinking level,
message count, cost, start time, and duration. Active sessions remain visible
before they produce assistant text and flash when their state changes;
completed sessions without output stay hidden. Rebooted machines keep their
last known sessions grayed out.

Above the session grid sits an expandable/collapsible **Usage & Balances**
section with speedometer cards per provider: Codex (weekly window),
OpenCode Go (5h / weekly / monthly), and Antigravity (Gemini and 3P pools,
5h + weekly) show usage *left* on the plan as gold gauges with reset
countdowns; OpenCode Zen and Moonshot AI show prepaid dollar balances. The
agent refreshes usage
every 60s (endpoint `GET /usage`), the server folds it into `/api/fleet`, and
the gauges read `100 - used_percent` so full-bleed windows read zero.

The expandable **Pulse** monitor counts recently active Pi, Claude Code, and Hermes sessions
across the fleet. Pulse and the table use one fleet snapshot, and every counted
active session remains in the row payload even beyond the inactive-history cap.
Its green trace moves continuously to a live cursor at 75% of the monitor
width, resting at the current active-session level while zero stays on the
bottom baseline. The dot tracks the trace's rendered value and scale. Each
horizontal line is an integer session level from zero through five by default;
the grid adds denser integer levels when a count or beat needs more headroom.
When an active session row receives a meaningful update, the row flash and
cursor fire together with a short beat above that resting level.
Cached sessions from offline machines and stale Hermes rows without an end
timestamp do not count. The browser pauses the sweep
when Pulse is collapsed or the tab is hidden, and reduced-motion mode renders a
static trace.

The agent reads Pi and Claude Code session files without talking to either
running process. Claude Code sessions appear in the same table and Pulse count,
with `claude-code` in the provider column. No Claude hooks or plugin are needed.

Claude transcripts default to `~/.claude/projects`, or the `projects` directory
under `CLAUDE_CONFIG_DIR` when set. Override the path with `agent -claude-dir`.
The scanner keeps recent assistant text, model, effort when recorded, message
counts, and tokens including cache reads and writes. Repeated assistant content
blocks count as one API message, not separate responses. Subagent directories,
sidechain records, thinking, and tool payloads are excluded from displayed output.

Activity uses the same recent-write window as Pi, not process liveness. A quiet
session stops counting after two minutes by default. Claude transcript records
do not provide dollar costs, so their price cells stay blank. No subscription
balance is inferred. Future Pi streaming support would use an extension.

## Build

```powershell
go build ./...
go test ./...
```

Cross-compile for the fleet with explicit OS and architecture targets. The
builder checks each binary's format and architecture before replacing an
existing artifact. It also keeps Windows build processes hidden.

```powershell
python deploy/build_fleet.py
# Build one target:
python deploy/build_fleet.py --target linux-amd64
# Test the format checks, including a Windows binary mislabeled as Linux:
python -m unittest discover -s deploy -p test_build_fleet.py
```

## Run

### Agent (every machine with pi)

```powershell
# Windows (home, laptop) — bind the Tailscale IP so the agent stays off the LAN
.\hs-pi-dashboard.exe agent -addr 100.101.122.39:8787

# macOS/Linux
./hs-pi-dashboard agent -addr <tailscale-ip>:8787
```

Endpoints: `GET /sessions` (snapshot JSON), `GET /usage` (provider plan and
balance cards), `GET /herdr` (cached live agent states), `GET /health`.
Herdr is discovered from installed CLI locations, or can be specified with
`-herdr-bin`. Machines without Herdr report unavailable telemetry rather than
an empty healthy monitor.

### Server (mini)

```bash
./hs-pi-dashboard serve -addr 100.97.164.73:8788
```

Endpoints: `/` (dashboard), `/api/fleet` (aggregated JSON), `/healthz`.

Agents are configured with `-fleet name|url` pairs; the default covers all
five computers from the fleet map. An optional third field,
`name|url|https-terminal-url`, adds a link to that machine's browser terminal.
Terminal links require HTTPS and cannot embed credentials. Machines that don't
answer are shown offline with their last known sessions grayed out.

## Install

### home / laptop — Windows scheduled task (agent)

Run as the regular user (no admin needed for the task itself):

```powershell
schtasks /create /tn "hs-pi-dashboard-agent" /sc onlogon /rl limited /f `
  /tr '"C:\path\to\hs-pi-dashboard.exe" agent -addr 100.101.122.39:8787'
schtasks /run /tn "hs-pi-dashboard-agent"
```

If other machines can't reach port 8787, allow it inbound once (admin):

```powershell
netsh advfirewall firewall add rule name="hs-pi-dashboard agent" dir=in action=allow protocol=TCP localport=8787
```

### air — macOS LaunchAgent (agent)

Ship the arm64 binary via Taildrop from any tailnet machine, then set it up on
the Mac (the file arrives in ~/Downloads):

```powershell
tailscale file cp dist/hs-pi-dashboard-darwin-arm64 franzs-macbook-air:
```

```bash
mkdir -p ~/bin ~/logs
mv ~/Downloads/hs-pi-dashboard-darwin-arm64 ~/bin/hs-pi-dashboard
chmod +x ~/bin/hs-pi-dashboard
mkdir -p ~/Library/LaunchAgents
cp deploy/com.hemsoft.hs-pi-dashboard.agent.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.hemsoft.hs-pi-dashboard.agent.plist
```

RunAtLoad + KeepAlive start the agent at login and restart it after crashes or
a failed Tailscale bind, so reboots no longer leave the machine dark.

### mini — always-on server

Ship the Linux binary via Taildrop, then run it at boot:

```bash
mkdir -p ~/bin
mv ~/Downloads/from-home--hs-pi-dashboard-linux-amd64 ~/bin/hs-pi-dashboard
chmod +x ~/bin/hs-pi-dashboard
(crontab -l 2>/dev/null; echo '@reboot /home/franz/bin/hs-pi-dashboard serve -addr 100.97.164.73:8788 >> /home/franz/logs/hs-pi-dashboard.log 2>&1') | crontab -
# start it now, no reboot needed:
mkdir -p ~/logs && nohup ~/bin/hs-pi-dashboard serve -addr 100.97.164.73:8788 >> ~/logs/hs-pi-dashboard.log 2>&1 &
```

## Security model (v1)

No authentication by design: agents and the server bind to Tailscale
interface addresses, so only tailnet devices (including iphone/ipad) can
reach them. Add a shared token in v2 before any broader exposure.

## Roadmap

- [ ] v2: pi extension reporting live turn/tool events from running sessions
- [x] v1: usage speedometers (Codex, OpenCode Go, Antigravity, Zen, Moonshot)
- [ ] v2: xAI usage once a source exists (card removed for now — Grok
      exposes no usage API)
- [ ] v2: optional bearer token between server and agents
- [ ] v2: systemd user unit for mini (with lingering) instead of cron
- [ ] v3: click-through session transcript, cost rollups per project
