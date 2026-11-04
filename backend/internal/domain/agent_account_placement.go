package domain

import "strings"

// AccountPlacement is how a new session without a chosen account picks one of
// its provider's usable accounts.
type AccountPlacement string

const (
	// AccountPlacementDefault starts the session on the provider's default
	// account, as before accounts could be pooled. It is the setting's default.
	AccountPlacementDefault AccountPlacement = "default"
	// AccountPlacementRandom picks any usable account that is not at a limit.
	AccountPlacementRandom AccountPlacement = "random"
	// AccountPlacementMostHeadroom picks the account furthest from its fullest
	// usage window.
	AccountPlacementMostHeadroom AccountPlacement = "most-headroom"
	// AccountPlacementLeastUsed picks the account with the fewest tokens used
	// through Tallpine in the last day.
	AccountPlacementLeastUsed AccountPlacement = "least-used"
)

// ParseAccountPlacement reads a setting value; empty means the default.
func ParseAccountPlacement(s string) (AccountPlacement, bool) {
	switch p := AccountPlacement(strings.ToLower(strings.TrimSpace(s))); p {
	case "":
		return AccountPlacementDefault, true
	case AccountPlacementDefault, AccountPlacementRandom, AccountPlacementMostHeadroom, AccountPlacementLeastUsed:
		return p, true
	}
	return "", false
}
