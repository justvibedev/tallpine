package domain

import "fmt"

// AutoHandoffNote is the fixed bootstrap note an idle orchestrator's successor
// reads (B374). It names no state itself: each line is a command that fetches
// live state from the daemon, so nothing is lost to a summary.
func AutoHandoffNote(prev SessionID) string {
	return fmt.Sprintf(`# Orchestrator handoff (automatic)

You replaced orchestrator %s, which was idle with nothing in flight. Its chat is
retired but stays readable. Rebuild your state from the daemon, not from memory:

1. tallpine status
2. tallpine session ls            (live workers and what each is doing)
3. tallpine review ls --session <worker>   (for every worker with a PR)
4. tallpine merges                (what merged, what is in flight, same-file conflicts)
5. gh pr list -R <repo> --state open   (the open PR queue)
6. tallpine backlog ls            (items in progress and next)
7. tallpine orchestrator intent ls   (the owner's open intents and standing decisions)

Then read the previous chat only for owner decisions you cannot find above:
tallpine session history %s

Do not repeat work a worker or merge already finished.
`, prev, prev)
}
