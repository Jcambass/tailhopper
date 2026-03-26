package tailscale

import (
	"fmt"
)

// TailnetObserver defines the interface for observing tailnet lifecycle events
// and claiming MagicDNS suffixes.
type TailnetObserver interface {
	// OnBroadcast is called when the tailnet's state changes and listeners should be notified.
	OnBroadcast(tailnetID int)
	// OnUserStateChange is called when the user's desired state changes.
	OnUserStateChange(tailnetID int, state UserState)
	// OnTerminalErrorChange is called when a fatal terminal error is set.
	OnTerminalErrorChange(tailnetID int, err string)
	// Claim attempts to claim the given MagicDNS suffix for the specified tailnet ID.
	// It returns an error if the suffix is already claimed by another tailnet.
	Claim(tailnetID int, suffix string) error
}

// noopObserver is a no-op implementation used when no observer is provided.
type noopObserver struct{}

func (noopObserver) OnBroadcast(int)                   {}
func (noopObserver) OnUserStateChange(int, UserState)  {}
func (noopObserver) OnTerminalErrorChange(int, string) {}
func (noopObserver) Claim(int, string) error           { return nil }

type AlreadyClaimedError struct {
	Suffix string
}

func (e *AlreadyClaimedError) Error() string {
	return fmt.Sprintf("magic DNS suffix '%s' is already claimed by another tailnet", e.Suffix)
}
