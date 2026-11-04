package domain

// PRLifecycleState is the one rule: merged, closed, draft, else open.
func PRLifecycleState(merged, closed, draft bool) PRState {
	switch {
	case merged:
		return PRStateMerged
	case closed:
		return PRStateClosed
	case draft:
		return PRStateDraft
	default:
		return PRStateOpen
	}
}
