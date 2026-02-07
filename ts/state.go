package ts

import (
	"sync"
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

// StateObserver is called whenever the state changes.
type StateObserver func(StateData)

// StateMachine manages tsnet connection state transitions.
type StateMachine struct {
	mu        sync.RWMutex
	current   StateData
	observers []StateObserver
}

// NewStateMachine creates a new state machine in the Connecting state.
func NewStateMachine() *StateMachine {
	return &StateMachine{
		current: StateData{State: StateConnecting},
	}
}

// Current returns the current state data.
func (sm *StateMachine) Current() StateData {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.current
}

// OnChange registers an observer to be called on state changes.
// The observer is also called immediately with the current state.
func (sm *StateMachine) OnChange(observer StateObserver) {
	sm.mu.Lock()
	sm.observers = append(sm.observers, observer)
	current := sm.current
	sm.mu.Unlock()

	// Call immediately with current state
	observer(current)
}

// transition changes the state and notifies observers.
func (sm *StateMachine) transition(newState StateData) {
	sm.mu.Lock()
	if sm.current.State == newState.State &&
		sm.current.AuthURL == newState.AuthURL &&
		sm.current.Error == newState.Error {
		sm.mu.Unlock()
		return // No change
	}
	sm.current = newState
	observers := make([]StateObserver, len(sm.observers))
	copy(observers, sm.observers)
	sm.mu.Unlock()

	for _, obs := range observers {
		obs(newState)
	}
}

// SetConnecting transitions to the Connecting state.
func (sm *StateMachine) SetConnecting() {
	sm.transition(StateData{State: StateConnecting})
}

// SetConnectingSlow transitions to the ConnectingSlow state.
func (sm *StateMachine) SetConnectingSlow() {
	sm.mu.RLock()
	current := sm.current.State
	sm.mu.RUnlock()

	// Only transition to slow if still in connecting state
	if current == StateConnecting {
		sm.transition(StateData{State: StateConnectingSlow})
	}
}

// SetNeedsLogin transitions to the NeedsLogin state with an auth URL.
func (sm *StateMachine) SetNeedsLogin(authURL string) {
	sm.transition(StateData{State: StateNeedsLogin, AuthURL: authURL})
}

// SetRunning transitions to the Running state.
func (sm *StateMachine) SetRunning() {
	sm.transition(StateData{State: StateRunning})
}

// SetError transitions to the Error state.
func (sm *StateMachine) SetError(err error) {
	sm.transition(StateData{State: StateError, Error: err})
}

// IsReady returns true if the state machine is in the Running state.
func (sm *StateMachine) IsReady() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.current.State == StateRunning
}
