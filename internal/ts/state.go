package ts

import (
	"errors"
	"log"
	"sync"
	"time"
)

// state represents the current tsnet connection state.
type state int

const (
	// StateDisabled indicates tsnet is manually disconnected
	StateDisabled state = iota
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
	magicDNSSuffix string // Set when State == StateConnected || StateMachineAuthNeeded
	slowTimer      *time.Timer
	mu             *sync.RWMutex
}

// newStateMachine creates a new state machine in the disabled state.
func newStateMachine() *stateMachine {
	sm := &stateMachine{
		mu: &sync.RWMutex{},
	}
	sm.SetDisabled()
	return sm
}

func (sm *stateMachine) AuthURL() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.authURL
}

func (sm *stateMachine) Error() error {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.err
}

func (sm *stateMachine) MagicDNSSuffix() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.magicDNSSuffix
}

// State getters
func (sm *stateMachine) Current() state {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state
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

func (sm *stateMachine) NeedsLogin() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state == StateNeedsLogin
}

func (sm *stateMachine) NeedsMachineAuth() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state == StateNeedsMachineAuth
}

func (sm *stateMachine) Connected() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state == StateConnected
}

func (sm *stateMachine) Failed() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state == StateFailed
}

// State transitions

type StateTransitionError struct {
	From state
	To   state
}

func (e *StateTransitionError) Error() string {
	return "invalid state transition from " + e.From.String() + " to " + e.To.String()
}

func (sm *stateMachine) SetDisabled() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.state == StateDisabled {
		return nil // No transition needed
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.state = StateDisabled
	sm.authURL = ""
	sm.err = nil
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
		sm.slowTimer = nil
	}
	return nil
}

var slowTimeout = 10 * time.Second

func (sm *stateMachine) SetConnecting() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	log.Printf("State transition: %s -> Connecting\n", sm.state.String())

	if sm.state == StateConnecting {
		return nil // No transition needed
	}

	// Trying to transition to connecting from slow connecting does noop.
	// Tailscale has no slow connecting state, so we treat slow connecting as a sub-state of connecting
	// From tailscales perspective we're transitioning from connecting to connecting, so we ignore the transition.
	// From our perspective we stay in slow connecting until we transition to a different state, at which point we reset the slow timer.
	if sm.state == StateConnectingSlow {
		return nil // No transition needed
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
		sm.mu.RLock()
		defer sm.mu.RUnlock()

		if sm.Current() == StateConnecting {
			sm.SetConnectingSlow()
		}
	})

	return nil
}

func (sm *stateMachine) SetConnectingSlow() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	log.Printf("State transition: %s -> ConnectingSlow\n", sm.state.String())

	if sm.state == StateConnectingSlow {
		return nil // No transition needed
	}

	if sm.state != StateConnecting {
		return &StateTransitionError{From: sm.state, To: StateConnectingSlow}
	}

	sm.state = StateConnectingSlow
	sm.authURL = ""
	sm.err = nil
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
		sm.slowTimer = nil
	}
	return nil
}

func (sm *stateMachine) SetNeedsLogin(authURL string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	log.Printf("State transition: %s -> NeedsLogin\n", sm.state.String())

	if sm.state == StateNeedsLogin && sm.authURL == authURL {
		return nil // No transition needed
	}

	if sm.state != StateConnecting && sm.state != StateConnectingSlow {
		return &StateTransitionError{From: sm.state, To: StateNeedsLogin}
	}

	if authURL == "" {
		return errors.New("authURL cannot be empty when setting state to NeedsLogin")
	}

	// TODO: Should we reset magicDNSSuffix on require login?

	sm.state = StateNeedsLogin
	sm.authURL = authURL
	sm.err = nil
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
		sm.slowTimer = nil
	}
	return nil
}

func (sm *stateMachine) SetNeedsMachineAuth(magicDNSSuffix string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	log.Printf("State transition: %s -> NeedsMachineAuth\n", sm.state.String())

	if sm.state == StateNeedsMachineAuth {
		return nil // No transition needed
	}

	if sm.state != StateNeedsLogin {
		return &StateTransitionError{From: sm.state, To: StateNeedsMachineAuth}
	}

	if magicDNSSuffix == "" {
		return errors.New("magicDNSSuffix cannot be empty when setting state to NeedsMachineAuth")
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
	return nil
}

func (sm *stateMachine) SetConnected(magicDNSSuffix string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	log.Printf("State transition: %s -> Connected\n", sm.state.String())

	if sm.state == StateConnected {
		return nil // No transition needed
	}

	if sm.state != StateConnecting && sm.state != StateConnectingSlow && sm.state != StateNeedsLogin && sm.state != StateNeedsMachineAuth {
		return &StateTransitionError{From: sm.state, To: StateConnected}
	}

	if magicDNSSuffix == "" {
		return errors.New("magicDNSSuffix cannot be empty when setting state to Connected")
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
	return nil
}

func (sm *stateMachine) SetFailed(err error) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	log.Printf("State transition: %s -> Failed\n", sm.state.String())

	if sm.state == StateFailed {
		return nil // No transition needed
	}

	if err == nil {
		return errors.New("err cannot be nil when setting state to Failed")
	}

	sm.state = StateFailed
	sm.authURL = ""
	sm.err = err
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
		sm.slowTimer = nil
	}
	return nil
}
