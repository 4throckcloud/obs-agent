<h1 align="center">4thRock OBS Agent</h1>

<p align="center">
  <strong>Remote OBS control, powered by 4thRock Cloud</strong><br>
  <em>Secure relay agent for Windows, macOS & Linux</em>
</p>

<p align="center">
  <b>Latest:</b> v2.25.0
</p>

---

## How It Works

```
┌─────────────────┐          ┌──────────────┐          ┌────────────────┐
│  4thRock Cloud   │◄── WSS ──►  OBS Agent   │◄── WS ──►  OBS Studio    │
│  (relay server)  │          │  (this app)  │          │  (local)       │
└─────────────────┘          └──────────────┘          └────────────────┘
```

The agent maintains a persistent WebSocket tunnel to the 4thRock relay server. All commands are authenticated with HMAC-SHA256 signed envelopes and replay protection.

## Download

| Platform | Download |
|----------|----------|
| Windows | [obs-agent-windows-amd64.zip](https://github.com/4throckcloud/obs-agent/releases/latest/download/obs-agent-windows-amd64.zip) |
| macOS Intel | [obs-agent-mac-intel.zip](https://github.com/4throckcloud/obs-agent/releases/latest/download/obs-agent-mac-intel.zip) |
| macOS Apple Silicon | [obs-agent-mac-apple.zip](https://github.com/4throckcloud/obs-agent/releases/latest/download/obs-agent-mac-apple.zip) |
| Linux | [obs-agent-linux-amd64.zip](https://github.com/4throckcloud/obs-agent/releases/latest/download/obs-agent-linux-amd64.zip) |

Checksums: [`manifest.json`](https://github.com/4throckcloud/obs-agent/releases/latest/download/manifest.json)

### macOS / Linux

```bash
curl -Lo obs-agent.zip https://github.com/4throckcloud/obs-agent/releases/latest/download/obs-agent-linux-amd64.zip
unzip obs-agent.zip
chmod +x obs-agent-linux-amd64
./obs-agent-linux-amd64
```

### Docker

```bash
docker run -d --name obs-agent \
  -e TOKEN=your-agent-token \
  -e OBS_PASS=your-obs-password \
  -e OBS_PORT=4455 \
  ghcr.io/4throckcloud/obs-agent:latest
```

## Quick Start

1. **Download** the binary for your platform
2. **Run it** — a setup wizard opens in your browser
3. **Authorize** — enter the code shown on your [4thRock dashboard](https://4throck.cloud)
4. The agent connects to OBS automatically

Config is stored encrypted (`obs-agent.dat`) next to the binary, locked to your machine.

## Usage

```
obs-agent [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `-token` | Agent authentication token | _(from config)_ |
| `-obs-port` | OBS WebSocket port | `4455` |
| `-obs-pass` | OBS WebSocket password | _(empty)_ |
| `-setup` | Re-run the setup wizard | |
| `-install` | Install as startup service | |
| `-uninstall` | Remove startup service | |
| `-verify` | Verify binary integrity | |
| `-status` | Show status of running agent | |
| `-version` | Print version | |

### Environment Variables

| Variable | Maps to |
|----------|---------|
| `OBS_AGENT_TOKEN` | `-token` |
| `OBS_PASSWORD` | `-obs-pass` |

## System Service

Install as a startup service so the agent runs automatically:

```bash
./obs-agent -install     # Install
./obs-agent -uninstall   # Uninstall
```

| Platform | Method |
|----------|--------|
| Windows | Windows Service API |
| macOS | launchd |
| Linux | systemd user service |

## Security

- **TLS 1.3** minimum for all relay connections
- **Signed envelopes** — HMAC-SHA256 with nonce and timestamp on every message
- **Replay protection** — nonce cache with 30-second timestamp window
- **Machine-locked config** — AES-256 encrypted via HKDF from hardware ID
- **No secrets in URLs** — token sent via headers only
- **Single instance lock** — prevents duplicate agents per directory

## Building from Source

Requires Go 1.24+.

```bash
# Single platform
CGO_ENABLED=0 go build -ldflags="-s -w" -o obs-agent ./cmd/agent

# All platforms (requires Docker)
docker compose run --rm obs-agent-builder make -C build build-all VERSION=v1.0.0
```

---

<p align="center">
  <sub>Copyright 2026 <a href="https://4throck.cloud">4thRock Cloud</a>. All rights reserved.</sub>
</p>
