# hs-pi-dashboard

[![CI](https://github.com/HemSoft/hs-pi-dashboard/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/HemSoft/hs-pi-dashboard/actions/workflows/ci.yml)

A gold-on-black, fleet-wide dashboard for [pi](https://github.com/badlogic/pi-mono)
coding-agent sessions across the Tailscale network. One Go binary, two modes:

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

Per machine, per session card: project, first user prompt, model + thinking
level, message count, token/cost totals, started/last-activity times, and a
pulsing gold dot while the session is active (file written within the last 2
minutes). Rebooted machines keep their last known sessions grayed out.

The agent only reads pi's session files — it never talks to a running pi
process, so it works whether or not pi is currently open. v2 plans a real pi
extension for live streaming (tool calls, thinking) from running sessions.

## Build

```powershell
go build ./...
go test ./...
```

Cross-compile for the fleet:

```powershell
$env:GOOS="linux";   go build -o dist/hs-pi-dashboard-linux-amd64 .;  $env:GOOS=$null
$env:GOOS="darwin";  go build -o dist/hs-pi-dashboard-darwin-arm64 .; $env:GOOS=$null
```

## Run

### Agent (every machine with pi)

```powershell
# Windows (home, laptop) — bind the Tailscale IP so the agent stays off the LAN
.\hs-pi-dashboard.exe agent -addr 100.101.122.39:8787

# macOS/Linux
./hs-pi-dashboard agent -addr <tailscale-ip>:8787
```

Endpoints: `GET /sessions` (snapshot JSON), `GET /health`.

### Server (mini)

```bash
./hs-pi-dashboard serve -addr 100.97.164.73:8788
```

Endpoints: `/` (dashboard), `/api/fleet` (aggregated JSON), `/healthz`.

Agents are configured with `-fleet name|url` pairs; the default covers all
five computers from the fleet map. Machines that don't answer are shown
offline with their last known sessions grayed out.

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
- [ ] v2: optional bearer token between server and agents
- [ ] v2: systemd user unit for mini (with lingering) instead of cron
- [ ] v3: click-through session transcript, cost rollups per project
