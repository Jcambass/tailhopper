package ts

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
