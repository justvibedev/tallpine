package backlog

import (
	"errors"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	autoNotePrefix = "- auto "
	// maxAutoNotes bounds the automation lines kept in agent_notes; the oldest
	// goes first. Text that is not an auto line is never touched.
	maxAutoNotes = 5
)

var errNoNoteChange = errors.New("note already present")

// autoNoteLine renders "- auto 2026-10-27: <text>".
func autoNoteLine(now time.Time, text string) string {
	return autoNotePrefix + now.UTC().Format("2006-01-02") + ": " + text
}

func hasAutoNote(notes, text string) bool {
	for _, l := range strings.Split(notes, "\n") {
		if strings.HasPrefix(l, autoNotePrefix) && strings.HasSuffix(l, ": "+text) {
			return true
		}
	}
	return false
}

// withAutoNote returns notes with the auto line appended (once per text) and
// trimmed to the last maxAutoNotes auto lines. It reports errNoNoteChange when
// the text is already there or the result would exceed the notes limit.
func withAutoNote(notes string, now time.Time, text string) (string, error) {
	if hasAutoNote(notes, text) {
		return notes, errNoNoteChange
	}
	lines := strings.Split(notes, "\n")
	if strings.TrimSpace(notes) == "" {
		lines = nil
	}
	lines = append(lines, autoNoteLine(now, text))
	for count := countAuto(lines); count > maxAutoNotes; count-- {
		for i, l := range lines {
			if strings.HasPrefix(l, autoNotePrefix) {
				lines = append(lines[:i], lines[i+1:]...)
				break
			}
		}
	}
	out := strings.Join(lines, "\n")
	if len([]rune(out)) > domain.MaxBacklogAgentNotes {
		return notes, errNoNoteChange
	}
	return out, nil
}

func countAuto(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, autoNotePrefix) {
			n++
		}
	}
	return n
}
