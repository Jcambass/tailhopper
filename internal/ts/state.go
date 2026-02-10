package ts

import (
	"context"
	"sync"
	"time"

	"github.com/jcambass/tailhopper/internal/logging"
)

// state represents the current tsnet connection state.
type state int

const (
	// StateDisabled indicates tsnet is manually disconnected
	StateDisabled state = iota
	// StateDisabling indicates tsnet is in the process of manually being disconnected.
	StateDisabling
	// StateConnecting is the initial state while tsnet is starting up.
	StateConnecting
	// StateConnectingSlow indicates connection is taking longer than expected.
	StateConnectingSlow
	// StateNeedsLogin indicates authentication is required.
	StateNeedsLogin
	// StateNeedsMachineAuth indicates the device needs admin approval.
	StateNeedsMachineAuth
	// StateConnected indicates tsnet is fully connected and operational.
	StateConnected
	// StateFailed indicates a fatal error occurred.
	StateFailed
)

func (s state) String() string {
	switch s {
	case StateDisabled:
		return "Disabled"
	case StateDisabling:
		return "Disabling"
	case StateConnecting:
		return "Connecting"
	case StateConnectingSlow:
		return "ConnectingSlow"
	case StateNeedsLogin:
		return "NeedsLogin"
	case StateNeedsMachineAuth:
		return "NeedsMachineAuth"
	case StateConnected:
		return "Connected"
	case StateFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

type stateMachine struct {
	state          state
	authURL        string // Set when State == StateNeedsLogin
	err            error  // Set when State == StateFailed
	magicDNSSuffix string // Set when State == StateConnected || StateNeedsMachineAuth
	slowTimer      *time.Timer
	mu             *sync.RWMutex
}

// newStateMachine creates a new state machine in the disabled state.
func newStateMachine() *stateMachine {
	sm := &stateMachine{
		mu: &sync.RWMutex{},
	}
	sm.SetDisabled(context.Background())
	return sm
}

// BestEffortMagicDNSSuffix returns the magicDNS suffix if available, otherwise returns an empty string.
// If you need to be sure that the magicDNS suffix is available, use the return values from Connected() or NeedsMachineAuth() instead.
func (sm *stateMachine) BestEffortMagicDNSSuffix() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.magicDNSSuffix
}

func (sm *stateMachine) Description() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.String()
}

func (sm *stateMachine) Disabling() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state == StateDisabling
}

func (sm *stateMachine) Disabled() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state == StateDisabled
}

func (sm *stateMachine) Connecting() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state == StateConnecting
}

func (sm *stateMachine) SlowConnecting() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state == StateConnectingSlow
}

func (sm *stateMachine) NeedsLogin() (bool, string) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.state == StateNeedsLogin {
		return true, sm.authURL
	}
	return false, ""
}
func (sm *stateMachine) NeedsMachineAuth() (bool, string) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.state == StateNeedsMachineAuth {
		return true, sm.magicDNSSuffix
	}
	return false, ""
}

func (sm *stateMachine) Connected() (bool, string) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.state == StateConnected {
		return true, sm.magicDNSSuffix
	}
	return false, ""
}

func (sm *stateMachine) Failed() (bool, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.state == StateFailed {
		return true, sm.err
	}
	return false, nil
}

// State transitions
func (sm *stateMachine) SetDisabling(ctx context.Context) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.state == StateDisabling {
		return // No transition needed
	}

	if sm.state == StateDisabled {
		panic("invalid state transition to Disabling from state " + sm.state.String())
	}

	sm.state = StateDisabling
	sm.authURL = ""
	sm.err = nil
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
		sm.slowTimer = nil
	}
}

func (sm *stateMachine) SetDisabled(ctx context.Context) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.state == StateDisabled {
		return // No transition needed
	}

	sm.state = StateDisabled
	sm.authURL = ""
	sm.err = nil
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
		sm.slowTimer = nil
	}
}

var slowTimeout = 10 * time.Second

func (sm *stateMachine) SetConnecting(ctx context.Context, reason string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	logger := logging.FromContext(ctx).With("component", "state")
	logger.Printf("State transition: %s -> Connecting (reason: %s)", sm.state.String(), reason)

	if sm.state == StateConnecting {
		return // No transition needed
	}

	// Trying to transition to connecting from slow connecting does noop.
	// Tailscale has no slow connecting state, so we treat slow connecting as a sub-state of connecting
	// From tailscales perspective we're transitioning from connecting to connecting, so we ignore the transition.
	// From our perspective we stay in slow connecting until we transition to a different state, at which point we reset the slow timer.
	if sm.state == StateConnectingSlow {
		return // No transition needed
	}

	// All other transitions to connecting are valid

	sm.state = StateConnecting

	sm.authURL = ""
	sm.err = nil
	// Cancel existing timer if any
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
		sm.slowTimer = nil
	}

	sm.slowTimer = time.AfterFunc(slowTimeout, func() {
		sm.setConnectingSlow(ctx, "slow timeout reached")
	})
}

// NOTE: This is **special** and must only be called from SetConnecting!
func (sm *stateMachine) setConnectingSlow(ctx context.Context, reason string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	logger := logging.FromContext(ctx).With("component", "state")
	logger.Printf("State transition: %s -> ConnectingSlow (reason: %s)", sm.state.String(), reason)

	if sm.state == StateConnectingSlow {
		return // No transition needed
	}

	if sm.state != StateConnecting {
		return // Ignore and do nothing. The state may have changed in the meantime.
	}

	sm.state = StateConnectingSlow
	sm.authURL = ""
	sm.err = nil
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
		sm.slowTimer = nil
	}
}

func (sm *stateMachine) SetNeedsLogin(ctx context.Context, reason string, authURL string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	logger := logging.FromContext(ctx).With("component", "state")
	logger.Printf("State transition: %s -> NeedsLogin (reason: %s)", sm.state.String(), reason)

	if sm.state == StateNeedsLogin && sm.authURL == authURL {
		return // No transition needed
	}

	if sm.state != StateConnecting && sm.state != StateConnectingSlow {
		panic("invalid state transition to NeedsLogin from state " + sm.state.String())
	}

	if authURL == "" {
		panic("authURL cannot be empty when setting state to NeedsLogin")
	}

	// TODO: Should we reset magicDNSSuffix on require login?

	sm.state = StateNeedsLogin
	sm.authURL = authURL
	sm.err = nil
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
		sm.slowTimer = nil
	}
}

func (sm *stateMachine) SetNeedsMachineAuth(ctx context.Context, reason string, magicDNSSuffix string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	logger := logging.FromContext(ctx).With("component", "state")
	logger.Printf("State transition: %s -> NeedsMachineAuth (reason: %s)", sm.state.String(), reason)

	if sm.state == StateNeedsMachineAuth {
		return // No transition needed
	}

	// Transitioning to NeedsMachineAuth from NeedsLogin is possible if the user completes the login but the machine still needs auth.
	// It's also possible when we're logged in but the machine needs auth before we can fully connect, so we allow transitioning from connecting states as well.
	// The real reason why the above happens is because we can transition from this state to the connection state and then here again.
	// We should fix that but it's kinda hard.. This is ok for now!
	if sm.state != StateNeedsLogin && sm.state != StateConnecting && sm.state != StateConnectingSlow {
		panic("invalid state transition to NeedsMachineAuth from state " + sm.state.String())
	}

	if magicDNSSuffix == "" {
		panic("magicDNSSuffix cannot be empty when setting state to NeedsMachineAuth")
	}

	// Not reset on most transitions!
	sm.magicDNSSuffix = magicDNSSuffix

	sm.state = StateNeedsMachineAuth
	sm.authURL = ""
	sm.err = nil
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
		sm.slowTimer = nil
	}
}

func (sm *stateMachine) SetConnected(ctx context.Context, reason string, magicDNSSuffix string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	logger := logging.FromContext(ctx).With("component", "state")
	logger.Printf("State transition: %s -> Connected (reason: %s)", sm.state.String(), reason)

	if sm.state == StateConnected {
		return // No transition needed
	}

	if sm.state != StateConnecting && sm.state != StateConnectingSlow && sm.state != StateNeedsLogin && sm.state != StateNeedsMachineAuth {
		panic("invalid state transition to Connected from state " + sm.state.String())
	}

	if magicDNSSuffix == "" {
		panic("magicDNSSuffix cannot be empty when setting state to Connected")
	}

	// Not reset on most transitions!
	sm.magicDNSSuffix = magicDNSSuffix

	sm.state = StateConnected
	sm.authURL = ""
	sm.err = nil
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
		sm.slowTimer = nil
	}
}

func (sm *stateMachine) SetFailed(ctx context.Context, reason string, err error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	logger := logging.FromContext(ctx).With("component", "state")
	logger.Printf("State transition: %s -> Failed (reason: %s)", sm.state.String(), reason)

	if sm.state == StateFailed {
		return // No transition needed
	}

	if err == nil {
		panic("err cannot be nil when setting state to Failed")
	}

	sm.state = StateFailed
	sm.authURL = ""
	sm.err = err
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
		sm.slowTimer = nil
	}
}
