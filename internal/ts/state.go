package ts

// UserState represents the user's desired on/off state for a tailnet.
type UserState string

const (
	// UserEnabled means the user has turned the tailnet on.
	UserEnabled UserState = "enabled"
	// UserDisabled means the user has turned the tailnet off.
	UserDisabled UserState = "disabled"
)

// State represents the internal connection state of a tailnet.
type State string

const (
	ConnectedState        State = "ConnectedState"
	HasTerminalErrorState State = "HasTerminalErrorState"
	NeedsLoginState       State = "NeedsLoginState"
	NeedsMachineAuthState State = "NeedsMachineAuthState"
	StartedState          State = "StartedState"
	StartingState         State = "StartingState"
	StoppedState          State = "StoppedState"
	StoppingState         State = "StoppingState"
	LoggingOutState       State = "LoggingOutState"
)
