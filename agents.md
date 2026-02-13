# Agent Guidelines for Tailhopper

This document contains coding guidelines and conventions for AI agents working on this project.

## Logging

**ALWAYS use `slog.Attr` functions, never key-value pairs.**

The application outputs structured logs in logfmt format. For colored/formatted viewing, pipe the output through [hl](https://github.com/pamburus/hl):
```bash
./tailhopper 2>&1 | hl
```

The source location is output as a `caller` field with the path relative to the project root (e.g., `internal/web/middleware.go:40`). [hl](https://github.com/pamburus/hl) will automatically format this with a `->` arrow at the end of each log line.

### Correct Usage

```go
slog.InfoContext(ctx, "message",
    slog.String("key", "value"),
    slog.Int("count", 42),
    slog.Duration("elapsed", duration),
    slog.Any("error", err))
```

### Incorrect Usage (DO NOT USE)

```go
// ❌ Don't use key-value pairs
slog.InfoContext(ctx, "message", "key", "value", "count", 42)

// ❌ Don't use slog.String for errors
slog.ErrorContext(ctx, "message", slog.String("error", err.Error()))
```

### Available slog.Attr Functions

- `slog.String(key, value)`
- `slog.Int(key, value)` / `slog.Int64(key, value)`
- `slog.Uint64(key, value)`
- `slog.Float64(key, value)`
- `slog.Bool(key, value)`
- `slog.Duration(key, value)`
- `slog.Time(key, value)`
- `slog.Group(key, attrs...)`
- `slog.Any(key, value)` - **USE THIS FOR ERRORS ONLY**

**For errors, always use: `slog.Any("error", err)`**

## Component Tags

When logging, always include a component identifier:

```go
slog.String("component", "socksserver")
```

Common component names:
- `socksserver` - SOCKS5 proxy
- `webserver` - HTTP/UI server
- `tailnet` - Tailscale connection management
- `registry` - Suffix registry
- `pac` - PAC file generation
