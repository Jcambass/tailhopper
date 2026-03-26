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
brew tap jcambass/tailhopper
brew install tailhopper
brew services start tailhopper
```

Stop or uninstall:

```bash
brew services stop tailhopper
brew uninstall tailhopper
```

### Linux

Download and install the binary:

```bash
VERSION=v0.1.0  # or use 'latest'
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
ARCHIVE="https://github.com/jcambass/tailhopper/releases/download/${VERSION}/tailhopper_linux_${ARCH}.tar.gz"

mkdir -p ~/.local/bin ~/.local/share/tailhopper
curl -fsSL "$ARCHIVE" | tar xz -C ~/.local/bin --strip-components 2 tailhopper_linux_${ARCH}/tailhopper

# Install systemd user service
mkdir -p ~/.config/systemd/user
curl -fsSL "https://github.com/jcambass/tailhopper/releases/download/${VERSION}/tailhopper_linux_${ARCH}.tar.gz" | tar xz -C /tmp
cp /tmp/tailhopper_linux_${ARCH}/tailhopper.service ~/.config/systemd/user/

systemctl --user daemon-reload
systemctl --user enable --now tailhopper
```

View logs:

```bash
journalctl --user -fu tailhopper
```

Stop or uninstall:

```bash
systemctl --user disable --now tailhopper
rm ~/.local/bin/tailhopper ~/.config/systemd/user/tailhopper.service
systemctl --user daemon-reload
```

### Windows

Download the binary from [Releases](https://github.com/jcambass/tailhopper/releases) and run it. To run as a background service, use your preferred Go service wrapper or task scheduler.

### Manual / other platforms

```bash
git clone https://github.com/jcambass/tailhopper.git
cd tailhopper
go build -o tailhopper ./cmd/tailhopper
./tailhopper
```

Tailhopper writes its state file (`tailhopper.json`) to the working directory it is started from.

## Release process

Releases are automated via [GoReleaser](https://goreleaser.com). When you push a `v*` tag, a GitHub Action automatically:

1. Builds cross-platform binaries (macOS, Linux, Windows)
2. Creates a GitHub Release with all artifacts
3. Generates the Homebrew cask and publishes it (adding to the top level `Casks` directory in this repo and committing the changes)

### To release:
cat dist/homebrew/Casks/tailhopper.rb
```bash
git tag v0.1.0
git push origin v0.1.0
```

The action uses the standard `GITHUB_TOKEN`, so no additional secrets are needed.

### Testing the release process locally

To validate the entire release pipeline (build, archive, checksum, Homebrew cask generation) without publishing:

```bash
go run github.com/goreleaser/goreleaser/v2@latest release --clean --snapshot --skip=publish
```

This runs the full release process in the `dist/` directory with all platform-specific binaries, archives, checksums, and Homebrew cask files. The snapshot version will be `<commit>-SNAPSHOT`. This matches exactly what runs when you push a real tag.

To validate the configuration syntax only:

```bash
go run github.com/goreleaser/goreleaser/v2@latest check
```

### Installation via Homebrew

Users can install via:

```bash
brew tap jcambass/tailhopper
brew install tailhopper
```



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
