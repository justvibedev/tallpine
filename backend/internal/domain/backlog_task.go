package domain

import (
	"fmt"
	"strings"
)

// BacklogPromptMarker starts the backlog section of a worker's task prompt.
const BacklogPromptMarker = "## Backlog item"

// BacklogPromptHeader is the heading and lead-in that precede the item text in
// a worker's task prompt.
const BacklogPromptHeader = BacklogPromptMarker + "\n\nThis task comes from the project backlog. Treat the item below as the task description and acceptance criteria.\n\n"

// TaskContext is the item text a worker is given: title, note, done-when and
// the orchestrator's notes. Agent notes reach the item's workers only; they are
// never exported.
func (i BacklogItem) TaskContext() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Backlog item %s: %s\n", i.Ref(), i.Title)
	if n := strings.TrimSpace(i.Note); n != "" {
		fmt.Fprintf(&b, "\nNote:\n%s\n", n)
	}
	if d := strings.TrimSpace(i.DoneWhen); d != "" {
		fmt.Fprintf(&b, "\nDone when:\n%s\n", d)
	}
	if a := strings.TrimSpace(i.AgentNotes); a != "" {
		fmt.Fprintf(&b, "\nOrchestrator notes:\n%s\n", a)
	}
	return strings.TrimRight(b.String(), "\n")
}
