package tailscale

// TailnetObserver defines the interface for observing tailnet lifecycle events.
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
}

// noopObserver is a no-op implementation used when no observer is provided.
type noopObserver struct{}

func (noopObserver) OnChange(TailnetSnapshot) {}
