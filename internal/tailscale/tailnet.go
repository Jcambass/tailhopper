package tailscale

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/jcambass/tailhopper/internal/socks"
	tsnetpkg "github.com/jcambass/tailhopper/internal/tsnet"
	"tailscale.com/tailcfg"
)

type Tailnet struct {
	// Immutable after construction.
	id              int
	socksPort       int
	tsnetStateDir   string
	userSetHostname string
	observer        TailnetObserver
	newServer       tsnetpkg.TSNetServerFactory

	// Logger has its own lock to avoid blocking state reads.
	logMu  sync.RWMutex
	logger *slog.Logger

	mu sync.RWMutex

	// Only changed by commands (start, stop, logout).
	server     tsnetpkg.TSNetServer
	watcher    *watcher
	socksProxy *socks.Server

	// Also changed by IPN state changes.
	claimedMagicDNSSuffix string
	terminalError         string
	peers                 []tailcfg.NodeView
	loginURL              string
	currentState          State
	// selfNodeHostname is the hostname reported by Tailscale for our own node.
	// Populated once the NetMap is received; empty until then.
	selfNodeHostname string

	// userState tracks the user's desired on/off state, independent of the
	// internal connection state managed by Tailscale IPN events.
	userState UserState
}

type TailnetSnapshot struct {
	ID             int
	State          State
	UserState      UserState
	MagicDNSSuffix string
	Hostname       string
	LoginURL       string
	Peers          []tailcfg.NodeView
	TerminalError  string
}

func (s *TailnetSnapshot) String() string {
	return fmt.Sprintf("TailnetSnapshot{ID: %d, State: %s, UserState: %s, MagicDNSSuffix: %s, Hostname: %s, LoginURL: %s, Peers: %d, TerminalError: %s}", s.ID, s.State, s.UserState, s.MagicDNSSuffix, s.Hostname, s.LoginURL, len(s.Peers), s.TerminalError)
}

func NewTailnet(id int, tsnetStateDir string, hostname string, claimedMagicDNSSuffix string, terminalError string, userEnabled bool, socksPort int, observer TailnetObserver, newServer tsnetpkg.TSNetServerFactory) *Tailnet {
	if observer == nil {
		observer = noopObserver{}
	}

	t := &Tailnet{
		id:                    id,
		observer:              observer,
		newServer:             newServer,
		logger:                slog.Default().With("component", "tailnet", "tailnet_id", id),
		tsnetStateDir:         tsnetStateDir,
		userSetHostname:       hostname,
		socksPort:             socksPort,
		claimedMagicDNSSuffix: claimedMagicDNSSuffix,
		terminalError:         terminalError,
	}

	if claimedMagicDNSSuffix != "" {
		t.logger = t.logger.With("magic_dns_suffix", claimedMagicDNSSuffix)
	}

	if terminalError != "" {
		t.currentState = HasTerminalErrorState
		t.userState = UserDisabled
	} else {
		t.currentState = StoppedState
		if userEnabled {
			t.userState = UserEnabled
		} else {
			t.userState = UserDisabled
		}
	}

	return t
}

func (t *Tailnet) ID() int {
	return t.id
}

func (t *Tailnet) SocksAddr() string {
	return fmt.Sprintf("localhost:%d", t.socksPort)
}

func (t *Tailnet) Snapshot() TailnetSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.snapshotLocked()
}

func (t *Tailnet) snapshotLocked() TailnetSnapshot {
	hostname := t.userSetHostname
	if t.selfNodeHostname != "" {
		hostname = t.selfNodeHostname
	}

	return TailnetSnapshot{
		ID:             t.id,
		MagicDNSSuffix: t.claimedMagicDNSSuffix,
		Hostname:       hostname,
		LoginURL:       t.loginURL,
		Peers:          t.peers,
		TerminalError:  t.terminalError,
		State:          t.currentState,
		UserState:      t.userState,
	}
}

// Commands
func (t *Tailnet) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.currentState != StoppedState {
		return fmt.Errorf("unable to start: tailnet is in state %s", t.currentState)
	}

	// Record user's intent to enable the tailnet.
	t.userState = UserEnabled

	t.log().Debug("Starting tailnet")

	// Track started components; the deferred cleanup closes them on failure.
	var (
		proxy         *socks.Server
		server        tsnetpkg.TSNetServer
		serverStarted bool
	)
	success := false
	defer func() {
		if success {
			return
		}
		if proxy != nil {
			if err := proxy.Close(); err != nil {
				t.log().Error("cleanup: failed to close SOCKS5 proxy", slog.Any("error", err))
			}
		}
		if serverStarted {
			if err := server.Close(); err != nil {
				t.log().Error("cleanup: failed to close tsnet server", slog.Any("error", err))
			}
		}
		// TODO: Give some UI feedback that the server failed to start and the tailnet is non-functional, since the user might not understand why it's auto stopping.
		t.setState(StoppedState)
	}()

	t.log().Debug("Starting SOCKS5 proxy", slog.Int("port", t.socksPort))
	var err error
	proxy, err = socks.NewServer(t.Dial, t.socksPort)
	if err != nil {
		t.log().Error("failed to start SOCKS5 proxy", slog.Any("error", err))
		return err
	}
	proxy.Start()

	t.log().Debug("Starting tsnet server")
	server = t.newServer(tsnetpkg.TSNetServerConfig{
		Dir:      t.tsnetStateDir,
		Hostname: t.userSetHostname,
		Logf:     t.tsnetLogf(slog.LevelDebug),
		UserLogf: t.tsnetLogf(slog.LevelInfo),
	})
	if err = server.Start(); err != nil {
		t.log().Error("failed to start tsnet server", slog.Any("error", err))
		return err
	}
	serverStarted = true

	lc, err := server.LocalClient()
	if err != nil {
		t.log().Error("failed to get LocalClient for watcher", slog.Any("error", err))
		return err
	}

	// Start IPN watcher to observe state changes.
	// Since this function holds the mu lock, the watcher won't be able to trigger
	// any state changes until after Start returns, so it's safe to start it here.
	t.log().Debug("Starting IPN watcher")
	w, err := NewWatcher(lc, t.reactToIPNStateChange, t.id)
	if err != nil {
		return err
	}

	// Assign all fields before entering StartedState to prevent accessing them before they're set.
	t.server = server
	t.watcher = w
	t.socksProxy = proxy
	t.setState(StartedState)
	success = true

	return nil
}

func (t *Tailnet) Stop(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch t.currentState {
	case ConnectedState, StartedState, NeedsLoginState, NeedsMachineAuthState:
		// Allow stopping
	default:
		return fmt.Errorf("unable to stop: tailnet is in state %s", t.currentState)
	}

	// Record user's intent to disable the tailnet.
	t.userState = UserDisabled

	defer t.setState(StoppedState)

	t.log().Debug("Stopping tailnet")

	server := t.server
	watcher := t.watcher
	socksProxy := t.socksProxy

	if socksProxy != nil {
		t.log().Debug("Stopping SOCKS5 proxy")
		err := socksProxy.Close()
		if err != nil {
			t.log().Error("failed to close SOCKS5 proxy", slog.Any("error", err))
			// Mostly ignoring for now but if the proxy is stuck we get in trouble on start again due to the port being in use.
			return err
		}
		t.log().Debug("SOCKS5 proxy stopped")
	}

	if watcher != nil {
		t.log().Debug("Stopping watcher")
		err := watcher.Close()
		if err != nil {
			t.log().Error("failed to close watcher", slog.Any("error", err))
		}
		t.log().Debug("Watcher stopped")
	}

	if server != nil {
		t.log().Debug("Stopping tsnet server")
		err := server.Close()
		if err != nil {
			t.log().Error("failed to close tsnet server", slog.Any("error", err))
			// TODO: What should we do if the server fails to close? The tailnet is in a bad state either way.
			// Is it stopped, is it started, is it in a failed stop state that is non terminal?
			return err
		}
		t.log().Debug("tsnet server stopped")
	}

	t.log().Debug("Tailnet stopped successfully")

	t.server = nil
	t.watcher = nil
	t.socksProxy = nil

	return nil
}

// Logout logs out from the tailnet and cleans up local state.
// Note: The device may remain visible in the Tailscale admin console as "disconnected"
// until manually deleted or it expires. This is expected Tailscale behavior.
//
// This is different from stop, which just stops the local tsnet server but leaves the machine authenticated with the tailnet.
// After logout, the machine will no longer be able to connect to the tailnet until it's logged in again.
// logout will start the server if it's not already started, then log out from the tailnet.
// During all of this we stay in the LoggingOut state.
// No matter what happens, we transition to the Stopped state at the end, since if logout is successful we're logged out and if logout fails we're in a bad state and stopping is the safest option.
func (t *Tailnet) Logout(ctx context.Context) error {
	t.mu.Lock()

	switch t.currentState {
	case NeedsLoginState:
		// Logout is a no-op in the needs login state since the user is already effectively logged out.
		t.mu.Unlock()
		return nil
	case ConnectedState, StoppedState, StartedState, NeedsMachineAuthState:
		// Allow logout
	default:
		t.mu.Unlock()
		return fmt.Errorf("unable to logout: tailnet is in state %s", t.currentState)
	}

	// Record user's intent to disable the tailnet.
	t.userState = UserDisabled

	// Set logging out state before releasing the lock so the UI can show it
	// while the network call is in flight.
	t.setState(LoggingOutState)

	// Create server if needed before releasing the lock.
	if t.server == nil {
		t.server = t.newServer(tsnetpkg.TSNetServerConfig{
			Dir:      t.tsnetStateDir,
			Hostname: t.userSetHostname,
			Logf:     t.tsnetLogf(slog.LevelDebug),
			UserLogf: t.tsnetLogf(slog.LevelInfo),
		})
	}
	server := t.server

	t.mu.Unlock()

	// Ensure we transition to stopped state at the end regardless of outcome.
	// We re-acquire the lock here since we released it above to allow the UI
	// to observe LoggingOutState while the network call is in flight.
	defer func() {
		t.mu.Lock()
		t.setState(StoppedState)
		t.mu.Unlock()
	}()

	// Do expensive I/O without holding the lock so LoggingOutState is observable.
	lc, err := server.LocalClient()
	if err != nil {
		t.log().Error("failed to get LocalClient for logout", slog.Any("error", err))
		return err
	}

	t.log().Debug("Logging out from tailnet")

	// TODO: Does logout auto close the server?
	if err := lc.Logout(ctx); err != nil {
		t.log().Error("failed to logout", slog.Any("error", err))
		return err
	}

	t.log().Debug("Successfully logged out from tailnet (device may remain visible in admin console until manually deleted)")
	return nil
}

func (t *Tailnet) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.currentState != ConnectedState {
		return nil, fmt.Errorf("unable to dial: tailnet is in state %s", t.currentState)
	}

	return t.server.Dial(ctx, network, addr)
}

////
// Internal state management functions
////

// setState updates the current state and notifies the observer with a snapshot.
func (t *Tailnet) setState(state State) {
	t.currentState = state

	t.log().Debug("set state", slog.String("state", string(state)))

	t.observer.OnChange(t.snapshotLocked())
}

func (t *Tailnet) log() *slog.Logger {
	t.logMu.RLock()
	defer t.logMu.RUnlock()
	return t.logger
}

func (t *Tailnet) tsnetLogf(level slog.Level) func(string, ...any) {
	return func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		msg = strings.TrimRight(msg, "\n")

		attrs, cleanMsg := parseLogKeyValues(msg)
		if cleanMsg != "" {
			msg = cleanMsg
		}

		allAttrs := make([]any, 0, len(attrs)+1)
		allAttrs = append(allAttrs, slog.String("subcomponent", "tsnet"))
		for _, a := range attrs {
			allAttrs = append(allAttrs, a)
		}

		t.log().Log(context.Background(), level, msg, allAttrs...)
	}
}

func parseLogKeyValues(msg string) ([]slog.Attr, string) {
	var attrs []slog.Attr
	var cleanParts []string

	for _, token := range strings.Fields(msg) {
		key, val, found := strings.Cut(token, "=")
		if !found || key == "" || !isValidLogKey(key) {
			cleanParts = append(cleanParts, token)
			continue
		}

		attrs = append(attrs, slog.String(key, val))
	}
	return attrs, strings.Join(cleanParts, " ")
}

// isValidLogKey reports whether key contains only alphanumeric characters,
// underscores, hyphens, and dots (i.e. valid structured-log key characters).
func isValidLogKey(key string) bool {
	for _, r := range key {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}
