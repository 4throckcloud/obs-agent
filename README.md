# OBS Agent

Secure relay agent that connects your local [OBS Studio](https://obsproject.com/) to [4thRock Cloud](https://4throck.cloud) for remote control. Runs as a lightweight background process on Windows, macOS, and Linux.

## How It Works

```
┌─────────────────┐          ┌──────────────┐          ┌────────────────┐
│   4thRock Cloud  │◄── WSS ──►  OBS Agent   │◄── WS ──►  OBS Studio    │
│   (relay server) │          │  (this app)  │          │  (local)       │
└─────────────────┘          └──────────────┘          └────────────────┘
```

The agent maintains a persistent WebSocket tunnel to the relay server with signed message envelopes. All commands are authenticated and verified with HMAC-SHA256 signatures and replay protection.

## Download

**Latest:** v2.21.0

| Platform | Download |
|----------|----------|
| Windows | [obs-agent-windows-amd64.zip](https://github.com/4throckcloud/obs-agent/releases/latest/download/obs-agent-windows-amd64.zip) |
| macOS Intel | [obs-agent-mac-intel.zip](https://github.com/4throckcloud/obs-agent/releases/latest/download/obs-agent-mac-intel.zip) |
| macOS Apple Silicon | [obs-agent-mac-apple.zip](https://github.com/4throckcloud/obs-agent/releases/latest/download/obs-agent-mac-apple.zip) |
| Linux | [obs-agent-linux-amd64.zip](https://github.com/4throckcloud/obs-agent/releases/latest/download/obs-agent-linux-amd64.zip) |

Latest version and checksums: [`manifest.json`](https://media.4throck.cloud/agent/manifest.json)

### macOS / Linux

```bash
# Download (replace URL with your platform)
curl -Lo obs-agent.zip https://github.com/4throckcloud/obs-agent/releases/latest/download/obs-agent-linux-amd64.zip
unzip obs-agent.zip
chmod +x obs-agent-linux-amd64
./obs-agent-linux-amd64
```

### Docker

```bash
docker run -d --name obs-agent \
  -e RELAY_URL=wss://4throck.cloud/ws/agent \
  -e TOKEN=your-agent-token \
  -e OBS_HOST=host.docker.internal \
  -e OBS_PORT=4455 \
  ghcr.io/4throckcloud/obs-agent:latest
```

## Quick Start

1. **Download** the binary for your platform
2. **Run it** — a setup wizard opens in your browser
3. **Authorize** — enter the code shown on your 4thRock dashboard
4. The agent connects to OBS automatically

The agent stores an encrypted config file (`obs-agent.dat`) next to the binary, locked to your machine.

## Usage

```
obs-agent [flags]
```

| Flag | Description | Default |
|------|-------------|---------|
| `-token` | Agent authentication token | _(from config)_ |
| `-obs-host` | OBS WebSocket host | `localhost` |
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
# Install
./obs-agent -install

# Uninstall
./obs-agent -uninstall
```

| Platform | Method |
|----------|--------|
| Windows | Windows Service API |
| macOS | launchd |
| Linux | systemd user service |

## Security

- **TLS 1.3** minimum for all relay connections
- **Signed envelopes** — every message carries an HMAC-SHA256 signature with nonce and timestamp
- **Replay protection** — nonce cache with 30-second timestamp window
- **Machine-locked config** — AES-256 encrypted, derived from hardware ID via HKDF
- **No secrets in URLs** — token sent via headers, never query parameters
- **Single instance lock** — prevents duplicate agents per directory

## Building from Source

Requires Go 1.22+.

```bash
# Single platform
CGO_ENABLED=0 go build -ldflags="-s -w" -o obs-agent ./cmd/agent

# All platforms (requires Docker)
docker compose run --rm obs-agent-builder make -C build build-all VERSION=v1.0.0
```

## License

Copyright 2026 4thRock Cloud. All rights reserved.
