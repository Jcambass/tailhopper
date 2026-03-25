<p align="center">
  <img src="internal/ui/static/favicon.svg" alt="Tailhopper" width="96">
</p>

<h1 align="center">Tailhopper</h1>

<p align="center">
  A SOCKS5 proxy manager for personal <a href="https://tailscale.com">Tailscale</a> users.<br>
  Connect to multiple Tailnets simultaneously and route traffic automatically via a PAC file.
</p>

---

## Why Tailhopper?

Tailscale only lets you be connected to one Tailnet at a time. If you need to access resources on two different Tailnets simultaneously — for example, reaching web services on your personal Tailnet while your work machine is already connected to a work Tailnet — you'd normally have to disconnect from one and reconnect to the other every time.

Tailhopper lets you stay connected to multiple Tailnets at once, without touching your primary Tailscale connection.

## What it does

- Single web dashboard to manage your additional Tailnets
- Works alongside your existing Tailscale connection — your primary Tailnet is unaffected
- Automatic proxy routing via PAC file — no per-request configuration needed
- Manual per Tailnet SOCKS5 proxy option for apps that don't support PAC or for non-HTTP traffic
- Runs as a background service on macOS (auto-starts at login, auto-restarts on crash)

## How it works

Tailhopper connects to each Tailnet using [tsnet](https://pkg.go.dev/tailscale.com/tsnet) and starts a dedicated local SOCKS5 proxy for each one. Traffic can reach those proxies in two ways:

**PAC file (recommended for browsers)** — Tailhopper serves a [PAC file](https://en.wikipedia.org/wiki/Proxy_auto-config) that maps each Tailnet's MagicDNS suffix to the corresponding SOCKS5 proxy. Configure your browser or OS to use the PAC file once, and every request is automatically sent to the right proxy based on the destination hostname — with no manual switching needed, even when accessing multiple Tailnets in the same browser session.

**Direct SOCKS5 (per Tailnet)** — Each Tailnet's proxy can also be configured directly in any app that supports SOCKS5. This is useful for non-HTTP traffic (SSH, database clients, etc.) or apps that don't respect the systems PAC setting. Because each proxy represents exactly one Tailnet, an app configured to use a specific proxy can only reach that one Tailnet at a time — unlike the PAC approach, there is no automatic multi-Tailnet routing.

## Limitations

- **Requires proxy-aware apps.** Only applications that respect PAC or SOCKS5 proxy settings will route traffic through Tailhopper. Apps that bypass system proxy settings or manage their own networking will not.
- **PAC routing only works with MagicDNS names.** The PAC file routes based on MagicDNS hostnames only. Accessing Tailnet machines by IP address is not supported via PAC — use the manual SOCKS5 proxy for that Tailnet directly instead.
- **Tailnets without MagicDNS enabled are not supported.**

## Usage

Once running, open the dashboard at **http://localhost:8888**.

From there you can:

- Add Tailnets and authenticate them via the Tailscale
- Temporarily enable/disable individual Tailnets
- Configure your browser or OS to use the PAC file for automatic proxy routing — see the in-app **How to configure PAC** section for OS- and browser-specific instructions
- Alternatively, configure a SOCKS5 proxy manually per-app using the host and port shown on each connected Tailnet's card (useful for non-HTTP traffic or apps that don't respect system wide PAC settings)

## Installation

### macOS (recommended)

The install script builds the binary, registers it as a LaunchAgent (starts at login, restarts on crash), and creates helper commands:

```bash
git clone https://github.com/jcambass/tailhopper.git
cd tailhopper
./macos/install.sh
```

The script requires [Go](https://go.dev) to be installed. During installation it prompts for the dashboard/PAC port and defaults to `8888`, so the dashboard is typically available at `http://localhost:8888` unless you choose a different port.

**Useful commands installed alongside the binary:**

| Command | Description |
|---|---|
| `tailhopper-logs` | Tail the live log output |
| `tailhopper-uninstall` | Fully remove Tailhopper |

**Service management:**

```bash
# View service status
launchctl list | grep tailhopper

# Restart
launchctl unload ~/Library/LaunchAgents/com.tailhopper.plist
launchctl load ~/Library/LaunchAgents/com.tailhopper.plist

# Stop
launchctl unload ~/Library/LaunchAgents/com.tailhopper.plist
```

**File locations (macOS):**

| Path | Purpose |
|---|---|
| `~/.local/bin/tailhopper` | Binary |
| `~/Library/Application Support/Tailhopper/` | State & configuration |
| `~/Library/Logs/Tailhopper/tailhopper.log` | Log output |
| `~/Library/LaunchAgents/com.tailhopper.plist` | LaunchAgent definition |

### Manual / other platforms

```bash
git clone https://github.com/jcambass/tailhopper.git
cd tailhopper
go build -o tailhopper ./cmd/tailhopper
./tailhopper
```

Tailhopper writes its state file (`tailhopper.json`) to the working directory it is started from.

## Build

Requires Go 1.22+.

```bash
go build ./...
```

To produce the release binary:

```bash
go build -o tailhopper ./cmd/tailhopper
```

## Configuration

Tailhopper is configured via environment variables:

| Variable | Default | Description |
|---|---|---|
| `HTTP_PORT` | `8888` | Dashboard and PAC file port |
| `LOG_LEVEL` | `INFO` | Log verbosity (`DEBUG`, `INFO`, `WARN`, `ERROR`) |

## Logs

Tailhopper outputs structured logs in [logfmt](https://brandur.org/logfmt) format to stderr. For colored output, pipe through [hl](https://github.com/pamburus/hl):

```bash
./tailhopper 2>&1 | hl
```
