package backlog

import "regexp"

// ownerFlagRE matches the markers a planner writes in decisions.md when a
// choice is not the owner's: "not confirmed by owner", or any form of
// "overrides owner answer" / "contradicts the owner". Auto-approve must leave
// such a plan to the owner. The planner prompt prescribes these markers.
var ownerFlagRE = regexp.MustCompile(`(?i)(?:not|un)[\s-]*confirmed\s+by\s+(?:the\s+|an\s+)?owner|(?:overrid\w*|overrode|contradict\w*)\s+(?:the\s+|an\s+)?owner`)

// decisionsNeedOwner reports whether the decisions mark a choice as not
// confirmed by the owner or as overriding an owner answer (B514).
func decisionsNeedOwner(decisions string) bool {
	return ownerFlagRE.MatchString(decisions)
}
