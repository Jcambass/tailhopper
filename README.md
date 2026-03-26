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

### macOS

Install with Homebrew:

```bash
brew install jcambass/homebrew-tap/tailhopper
brew services start tailhopper
```

Stop:

```bash
brew services stop tailhopper
```

Uninstall:

```bash
brew uninstall tailhopper
brew services cleanup
```

Use a custom dashboard port:

```bash
TAILHOPPER_HTTP_PORT=9999 brew services restart tailhopper
```

To also remove the state and log files:

```bash
rm -rf "$(brew --prefix)/var/tailhopper" "$(brew --prefix)/var/log/tailhopper.log"
```

### Linux

Run the install script (single command):

```bash
curl -fsSL https://raw.githubusercontent.com/jcambass/tailhopper/main/linux/install.sh | bash
```

Install a specific version:

```bash
VERSION=v0.1.0 curl -fsSL https://raw.githubusercontent.com/jcambass/tailhopper/main/linux/install.sh | bash
```

Use a custom dashboard port:

```bash
HTTP_PORT=9999 curl -fsSL https://raw.githubusercontent.com/jcambass/tailhopper/main/linux/install.sh | bash
```

View logs:

```bash
journalctl --user -fu tailhopper
```

Stop:

```bash
systemctl --user disable --now tailhopper
```

Uninstall:

```bash
systemctl --user disable --now tailhopper
rm ~/.local/bin/tailhopper ~/.config/systemd/user/tailhopper.service
systemctl --user daemon-reload
```

To also remove state files:

```bash
rm -rf ~/.local/share/tailhopper
```

### Windows

Download the binary from [Releases](https://github.com/jcambass/tailhopper/releases) and run it. To run as a background service, use your preferred Go service wrapper or task scheduler.

### Build from source

Requires Go 1.22+.

```bash
git clone https://github.com/jcambass/tailhopper.git
cd tailhopper
go build -o tailhopper ./cmd/tailhopper
./tailhopper
```

## Configuration & State

Tailhopper is configured via environment variables:

| Variable | Default | Description |
|---|---|---|
| `HTTP_PORT` | `8888` | Dashboard and PAC file port |
| `LOG_LEVEL` | `INFO` | Log verbosity (`DEBUG`, `INFO`, `WARN`, `ERROR`) |

Tailhopper stores state in its working directory:

- `tailhopper.json`
- `tailnets/` (per-tailnet runtime/state data)

Common working directories:

- Homebrew service (macOS): `$(brew --prefix)/var/tailhopper`
- Linux installer (systemd user service): `~/.local/share/tailhopper`
- Manual runs: current working directory

## Logs

Tailhopper outputs structured logs in [logfmt](https://brandur.org/logfmt) format to stderr. For colored output, pipe through [hl](https://github.com/pamburus/hl):

```bash
./tailhopper 2>&1 | hl
```

## Release process

This section is for maintainers.

Releases are automated via [GoReleaser](https://goreleaser.com). When you push a `v*` tag, GitHub Actions automatically:

1. Builds cross-platform binaries (macOS, Linux, Windows)
2. Creates a GitHub Release with artifacts
3. Updates `Formula/tailhopper.rb` in `jcambass/homebrew-tap` (`url` and `sha256`) via `mislav/bump-homebrew-formula-action`

### Publish a release

```bash
git tag v0.1.0
git push origin v0.1.0
```

The workflow requires a `TAP_COMMITER_TOKEN` secret with write access to `jcambass/homebrew-tap`.

### Validate release pipeline locally

Run a full local snapshot release (no publish):

```bash
go run github.com/goreleaser/goreleaser/v2@latest release --clean --snapshot --skip=publish
```

Validate config only:

```bash
go run github.com/goreleaser/goreleaser/v2@latest check
```

### Test Homebrew formula locally

Homebrew formula changes live in `jcambass/homebrew-tap`.
Make sure to update the `url` and `sha256` in `Formula/tailhopper.rb` to point to the new release artifact before testing.

```bash
git clone https://github.com/jcambass/homebrew-tap.git
cd homebrew-tap
brew install --build-from-source --verbose --debug ./Formula/tailhopper.rb
```
