package tailscale

import (
	"fmt"
)

// MagicDNSSuffixRegistry defines the interface for claiming MagicDNS suffixes for tailnets.
// This helps to ensure that no two tailnets claim the same MagicDNS suffix.
type MagicDNSSuffixRegistry interface {
	// Claim attempts to claim the given MagicDNS suffix for the specified tailnet ID.
	// It returns an error if the suffix is already claimed by another tailnet or if there is a mismatch with an existing claim.
	Claim(tailnetID int, suffix string) error
}

type AlreadyClaimedError struct {
	Suffix string
}

func (e *AlreadyClaimedError) Error() string {
	return fmt.Sprintf("magic DNS suffix '%s' is already claimed by another tailnet", e.Suffix)
}
