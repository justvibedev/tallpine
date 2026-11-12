package domain

import (
	"fmt"
	"strings"
)

// orchestratorRebuildSteps are the commands an orchestrator that starts without
// its previous context runs to fetch live state from the daemon.
const orchestratorRebuildSteps = `1. tallpine status
2. tallpine session ls            (live workers and what each is doing)
3. tallpine review ls --session <worker>   (for every worker with a PR)
4. tallpine merges                (what merged, what is in flight, same-file conflicts)
5. gh pr list -R <repo> --state open   (the open PR queue)
6. tallpine backlog ls            (items in progress and next)
7. tallpine orchestrator intent ls   (the owner's open intents and standing decisions)
`

// lostConversationBriefCap bounds the original brief quoted in the note.
const lostConversationBriefCap = 4_000

// AutoHandoffNote is the fixed bootstrap note an idle orchestrator's successor
// reads (B374). It names no state itself: each line is a command that fetches
// live state from the daemon, so nothing is lost to a summary.
func AutoHandoffNote(prev SessionID) string {
	return fmt.Sprintf(`# Orchestrator handoff (automatic)

You replaced orchestrator %s, which was idle with nothing in flight. Its chat is
retired but stays readable. Rebuild your state from the daemon, not from memory:

%s
Then read the previous chat only for owner decisions you cannot find above:
tallpine session history %s

Do not repeat work a worker or merge already finished.
`, prev, orchestratorRebuildSteps, prev)
}

// LostConversationNote seeds an orchestrator whose saved provider conversation
// is gone (B678): the agent reported it does not exist, so the session goes on
// in a fresh one. Like AutoHandoffNote it points at the daemon's state; it also
// names the session's own Tallpine history, its artifact directory (where its
// state notes live) and the brief it was started with.
func LostConversationNote(id SessionID, artifactDir, brief string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `# Orchestrator recovery (automatic)

Tallpine could not resume your saved agent conversation: the agent reports it no
longer exists. You are still orchestrator %s, now in a fresh conversation.
Rebuild your state from the daemon, not from memory:

%s
Then read your own earlier chat for owner decisions you cannot find above:
tallpine session history %s
`, id, orchestratorRebuildSteps, id)
	if dir := strings.TrimSpace(artifactDir); dir != "" {
		fmt.Fprintf(&b, "\nYour latest state or handoff note, if you wrote one, is in %s.\n", dir)
	}
	if brief = strings.TrimSpace(brief); brief != "" {
		if len(brief) > lostConversationBriefCap {
			brief = strings.ToValidUTF8(brief[:lostConversationBriefCap], "") + "\n…[clipped]…"
		}
		fmt.Fprintf(&b, "\nThe brief you were started with:\n\n%s\n", brief)
	}
	b.WriteString("\nDo not repeat work a worker or merge already finished.\n")
	return b.String()
}
