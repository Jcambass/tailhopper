package ts

import (
	"sync"
	"time"
)

// State represents the current tsnet connection state.
type State int

const (
	// StateConnecting is the initial state while tsnet is starting up.
	StateConnecting State = iota
	// StateConnectingSlow indicates connection is taking longer than expected.
	StateConnectingSlow
	// StateNeedsLogin indicates authentication is required.
	StateNeedsLogin
	// StateNeedsMachineAuth indicates the device needs admin approval.
	StateNeedsMachineAuth
	// StateRunning indicates tsnet is fully connected and operational.
	StateRunning
	// StateError indicates a fatal error occurred.
	StateError
)

func (s State) String() string {
	switch s {
	case StateConnecting:
		return "Connecting"
	case StateConnectingSlow:
		return "ConnectingSlow"
	case StateNeedsLogin:
		return "NeedsLogin"
	case StateNeedsMachineAuth:
		return "NeedsMachineAuth"
	case StateRunning:
		return "Running"
	case StateError:
		return "Error"
	default:
		return "Unknown"
	}
}

// StateData contains the current state and associated data.
type StateData struct {
	State   State
	AuthURL string // Set when State == StateNeedsLogin
	Error   error  // Set when State == StateError
}

var slowTimeout = 10 * time.Second

// StateMachine manages tsnet connection state transitions.
type StateMachine struct {
	mu        sync.RWMutex
	current   StateData
	slowTimer *time.Timer
}

// NewStateMachine creates a new state machine in the Connecting state.
func NewStateMachine() *StateMachine {
	sm := &StateMachine{
		current: StateData{State: StateConnecting},
	}
	sm.startSlowTimer()
	return sm
}

// Current returns the current state data.
func (sm *StateMachine) Current() StateData {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.current
}

// transition changes the state.
func (sm *StateMachine) transition(newState StateData) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.current.State == newState.State &&
		sm.current.AuthURL == newState.AuthURL &&
		sm.current.Error == newState.Error {
		return // No change
	}
	sm.current = newState
}

// startSlowTimer starts the timer that triggers ConnectingSlow state.
func (sm *StateMachine) startSlowTimer() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Cancel existing timer if any
	if sm.slowTimer != nil {
		sm.slowTimer.Stop()
	}

	sm.slowTimer = time.AfterFunc(slowTimeout, func() {
		if sm.Current().State == StateConnecting {
			sm.SetConnectingSlow()
		}
	})
}

// SetConnecting transitions to the Connecting state and starts the slow timer.
func (sm *StateMachine) SetConnecting() {
	// Do not regress to connecting if already in slow connecting state
	if sm.Current().State == StateConnectingSlow {
		return
	}

	sm.transition(StateData{State: StateConnecting})
	sm.startSlowTimer()
}

// SetConnectingSlow transitions to the ConnectingSlow state.
func (sm *StateMachine) SetConnectingSlow() {
	sm.transition(StateData{State: StateConnectingSlow})
}

// SetNeedsLogin transitions to the NeedsLogin state with an auth URL.
func (sm *StateMachine) SetNeedsLogin(authURL string) {
	sm.transition(StateData{State: StateNeedsLogin, AuthURL: authURL})
}

// SetNeedsMachineAuth transitions to the NeedsMachineAuth state.
func (sm *StateMachine) SetNeedsMachineAuth() {
	sm.transition(StateData{State: StateNeedsMachineAuth})
}

// SetRunning transitions to the Running state.
func (sm *StateMachine) SetRunning() {
	sm.transition(StateData{State: StateRunning})
}

// SetError transitions to the Error state.
func (sm *StateMachine) SetError(err error) {
	sm.transition(StateData{State: StateError, Error: err})
}
