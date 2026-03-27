package tailscale

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jcambass/tailhopper/internal/socks"
	tsnetpkg "github.com/jcambass/tailhopper/internal/tsnet"
	"tailscale.com/ipn"
)

func (t *Tailnet) reactToIPNStateChange(ctx context.Context, ipnState IPNState) {
	t.log().Debug("Reacting to IPN state change", slog.String("ipn_state", ipnState.String()))

	// Hold locks for all the processing but release before notifying to keep lock time in check.
	t.mu.Lock()

	state := t.currentState
	var changed bool
	var terminalCleanup terminalCleanup

	switch state {
	case ConnectedState:
		changed = ProcessIPN(ipnState).
			Always(
				t.maybeClaimMagicDNSSuffixLocked,
				t.updatePeersLocked,
				t.maybeUpdateSelfNodeHostnameLocked,
			).
			OneOf(
				t.maybeTransitionToNeedsLoginLocked,
				t.maybeTransitionToNeedsMachineAuthLocked,
			).
			Process()

	case StartedState:
		changed = ProcessIPN(ipnState).
			Always(
				t.maybeUpdateSelfNodeHostnameLocked,
			).
			OneOf(
				t.maybeTransitionToNeedsLoginLocked,
				t.maybeTransitionToNeedsMachineAuthLocked,
				t.maybeTransitionToConnectedLocked,
			).
			Process()

	case NeedsLoginState:
		changed = ProcessIPN(ipnState).
			Always(
				t.maybeUpdateSelfNodeHostnameLocked,
			).
			OneOf(
				t.maybeTransitionToNeedsMachineAuthLocked,
				t.maybeTransitionToConnectedLocked,
			).
			Process()

	case NeedsMachineAuthState:
		changed = ProcessIPN(ipnState).
			Always(
				t.maybeClaimMagicDNSSuffixLocked,
				t.maybeUpdateSelfNodeHostnameLocked,
			).
			OneOf(
				t.maybeTransitionToNeedsLoginLocked,
				t.maybeTransitionToConnectedLocked,
			).
			Process()

	default:
		// Simply ignore IPN state changes in any other state.
	}

	if t.currentState == HasTerminalErrorState {
		terminalCleanup = t.prepareTerminalErrorCleanupLocked()
	}

	var snapshot TailnetSnapshot
	if changed {
		snapshot = t.snapshotLocked()
	}

	t.mu.Unlock()

	// Run cleanup outside the lock: closing the server, watcher, and SOCKS proxy
	// involves blocking I/O that must not hold mu.
	terminalCleanup.run(t)

	// Notify after releasing lock to keep lock time minimal.
	if changed {
		t.observer.OnChange(snapshot)
	}
}

// Locked helpers below: require t.mu to be held by caller.

func (t *Tailnet) maybeClaimMagicDNSSuffixLocked(ipnState IPNState) bool {
	// Guard: both suffixes are set but differ — the tailnet was moved to a
	// different account or the suffix changed underneath us.
	if ipnState.MagicDNSSuffix != "" && t.claimedMagicDNSSuffix != "" && ipnState.MagicDNSSuffix != t.claimedMagicDNSSuffix {
		// TODO: Handle case where the MagicDNS suffix changes while we're running.
		t.log().Error("MagicDNS suffix changed unexpectedly", slog.String("old_suffix", t.claimedMagicDNSSuffix), slog.String("new_suffix", ipnState.MagicDNSSuffix))
		return false
	}

	if ipnState.MagicDNSSuffix == "" || t.claimedMagicDNSSuffix != "" {
		return false
	}

	if err := t.observer.Claim(t.id, ipnState.MagicDNSSuffix); err != nil {
		if claimErr, ok := errors.AsType[*AlreadyClaimedError](err); ok {
			t.log().Error("magic DNS suffix claim error", slog.Any("error", claimErr))
			// A duplicate claim is fatal: disable the tailnet and keep it in an
			// unrecoverable error state until the user deletes/recreates it.
			return t.setTerminalErrorLocked(claimErr.Error())
		}

		t.log().Error("failed to claim MagicDNS suffix", slog.String("suffix", ipnState.MagicDNSSuffix), slog.Any("error", err))
		return false
	}

	// Successfully claimed the MagicDNS suffix.
	t.claimedMagicDNSSuffix = ipnState.MagicDNSSuffix

	// Update logger with the new suffix
	t.logMu.Lock()
	t.logger = t.logger.With(slog.String("magic_dns_suffix", ipnState.MagicDNSSuffix))
	t.logMu.Unlock()

	return true
}

func (t *Tailnet) maybeTransitionToNeedsLoginLocked(ipnState IPNState) bool {
	if ipnState.State == nil || *ipnState.State != ipn.NeedsLogin {
		return false
	}

	if ipnState.BrowseToURL == nil || *ipnState.BrowseToURL == "" {
		return false
	}

	t.loginURL = *ipnState.BrowseToURL
	t.currentState = NeedsLoginState
	return true
}

func (t *Tailnet) maybeTransitionToNeedsMachineAuthLocked(ipnState IPNState) bool {
	if ipnState.State == nil || *ipnState.State != ipn.NeedsMachineAuth {
		return false
	}

	t.currentState = NeedsMachineAuthState
	return true
}

func (t *Tailnet) maybeTransitionToConnectedLocked(ipnState IPNState) bool {
	if ipnState.State == nil || *ipnState.State != ipn.Running {
		return false
	}

	t.currentState = ConnectedState
	return true
}

func (t *Tailnet) updatePeersLocked(ipnState IPNState) bool {
	t.peers = ipnState.Peers
	return true
}

func (t *Tailnet) maybeUpdateSelfNodeHostnameLocked(ipnState IPNState) bool {
	if !ipnState.SelfNode.Valid() {
		return false
	}
	hostname := ipnState.SelfNode.ComputedName()
	if hostname == "" || hostname == t.selfNodeHostname {
		return false
	}
	t.selfNodeHostname = hostname
	return true
}

func (t *Tailnet) setTerminalErrorLocked(errMsg string) bool {
	changed := false

	if t.terminalError != errMsg {
		t.terminalError = errMsg
		changed = true
	}

	if t.userState != UserDisabled {
		t.userState = UserDisabled
		changed = true
	}

	if t.currentState != HasTerminalErrorState {
		t.currentState = HasTerminalErrorState
		changed = true
	}

	return changed
}

type terminalCleanup struct {
	server     tsnetpkg.TSNetServer
	watcher    *watcher
	socksProxy *socks.Server
}

func (t *Tailnet) prepareTerminalErrorCleanupLocked() terminalCleanup {
	cleanup := terminalCleanup{
		server:     t.server,
		watcher:    t.watcher,
		socksProxy: t.socksProxy,
	}
	t.server = nil
	t.watcher = nil
	t.socksProxy = nil
	return cleanup
}

func (c terminalCleanup) run(t *Tailnet) {
	if c.socksProxy != nil {
		if err := c.socksProxy.Close(); err != nil {
			t.log().Error("failed to close SOCKS5 proxy after terminal error", slog.Any("error", err))
		}
	}

	if c.watcher != nil {
		if err := c.watcher.Close(); err != nil {
			t.log().Error("failed to close watcher after terminal error", slog.Any("error", err))
		}
	}

	if c.server != nil {
		if err := c.server.Close(); err != nil {
			t.log().Error("failed to close tsnet server after terminal error", slog.Any("error", err))
		}
	}
}
