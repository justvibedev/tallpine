package skillbridge

// Visibility of one skill to one harness.
const (
	StatusNative     = "native"     // the harness loads it from its own directory
	StatusMissing    = "missing"    // the harness cannot see it (an offer may fix that)
	StatusPending    = "pending"    // in the checkout but not committed: sessions do not get it yet
	StatusShadowed   = "shadowed"   // another skill with this name is used instead
	StatusUnverified = "unverified" // the harness is not one we check
)

// Statuses reports, for every skill in inv.Skills (same order), how harness
// sees it. The first same-name skill is the one an offer would link.
func Statuses(inv Inventory, harness string) []string {
	out := make([]string, len(inv.Skills))
	if harness != HarnessClaude && harness != HarnessCodex {
		for i := range out {
			out[i] = StatusUnverified
		}
		return out
	}
	groups := map[string][]int{}
	for i, s := range inv.Skills {
		groups[s.Name] = append(groups[s.Name], i)
	}
	for _, idx := range groups {
		nativeIdx := -1
		for _, i := range idx {
			if Native(harness, inv.Skills[i].Scope, inv.Skills[i].Convention) {
				nativeIdx = i
				break
			}
		}
		for _, i := range idx {
			switch {
			case nativeIdx >= 0 && i == nativeIdx && inv.Skills[i].Pending:
				out[i] = StatusPending
			case nativeIdx >= 0 && i == nativeIdx:
				out[i] = StatusNative
			case nativeIdx < 0 && i == idx[0]:
				out[i] = StatusMissing
			default:
				out[i] = StatusShadowed
			}
		}
	}
	return out
}

// InstructionStatus says how harness sees the project instruction file:
// native (its own file exists), missing (the other convention's file exists and an
// offer can link it), none, or unverified.
func InstructionStatus(in Instructions, harness string) string {
	switch harness {
	case HarnessClaude:
		if in.ClaudeMD {
			if in.ClaudeMDPending {
				return StatusPending
			}
			return StatusNative
		}
		if in.AgentsMD {
			return StatusMissing
		}
	case HarnessCodex:
		if in.AgentsMD {
			if in.AgentsMDPending {
				return StatusPending
			}
			return StatusNative
		}
		if in.RootClaudeMD {
			return StatusMissing
		}
	default:
		return StatusUnverified
	}
	return "none"
}
