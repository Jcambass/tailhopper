package tailscale

import (
	"fmt"
)

// TailnetObserver defines the interface for observing tailnet lifecycle events
// and claiming MagicDNS suffixes.
//
// The primary implementation is the registry, which creates Tailnet instances
// and passes itself as the observer. This creates a runtime callback cycle
// (registry -> tailnet -> observer/registry) but avoids a package-level import
// cycle: the tailscale package depends only on this interface, not on the
// registry package.
type TailnetObserver interface {
	// OnChange is called when the tailnet's state changes.
	// The snapshot contains the full state at the time of the change.
	OnChange(snapshot TailnetSnapshot)
	// Claim attempts to claim the given MagicDNS suffix for the specified tailnet ID.
	// It returns an error if the suffix is already claimed by another tailnet.
	Claim(tailnetID int, suffix string) error
}

// noopObserver is a no-op implementation used when no observer is provided.
type noopObserver struct{}

func (noopObserver) OnChange(TailnetSnapshot) {}
func (noopObserver) Claim(int, string) error  { return nil }

type AlreadyClaimedError struct {
	Suffix string
}

func (e *AlreadyClaimedError) Error() string {
	return fmt.Sprintf("magic DNS suffix '%s' is already claimed by another tailnet", e.Suffix)
}
