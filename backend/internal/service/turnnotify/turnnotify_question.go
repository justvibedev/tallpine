package turnnotify

import (
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// questionNotice is the needs-input text for an in-chat question. It carries
// the request id and the commands that list and answer it, so the orchestrator
// can act without opening the session.
func questionNotice(s ports.ActivitySignal, text string) string {
	out := "question: " + cmpOr(truncate(text, maxReasonCharacters), "no text given")
	rid := strings.TrimSpace(s.ApprovalRequestID)
	if rid == "" || utf8.RuneCountInString(rid) > maxRequestIDCharacters {
		return out
	}
	return out + "\nRequest id: " + rid + "\nList with `" + brand.CLIName + " session approvals <session>`, answer with `" +
		brand.CLIName + " session answer <session> <requestId> --action accept|decline|cancel`."
}
