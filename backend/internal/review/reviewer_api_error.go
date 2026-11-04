package review

import (
	"regexp"
	"strings"
	"time"
)

// ReviewerAPIErrorPrefix starts the body of a pass whose reviewer turn ended on
// a transient provider error (a 5xx or "overloaded" reply): the reviewer never
// looked at the commit, so the pass is retried like a usage-limit wait (B455).
const ReviewerAPIErrorPrefix = "reviewer API error: "

// reviewerAPIErrorGrace is how long a terminal reviewer idles on such an error
// before its pass is failed; the pane has already retried the call itself, so
// waiting out the full idle grace only delays the retry.
const reviewerAPIErrorGrace = time.Minute

// reviewerAPIErrorTailLines bounds the lines read from the bottom of the pane:
// the error is the last output before the idle prompt, while an older line on
// a reused pane, or text the reviewer is quoting, sits further up.
const reviewerAPIErrorTailLines = 8

// screenAPIError matches only a line that starts with Claude's own error text,
// after its result marker, never a quoted diff or file line.
var screenAPIError = regexp.MustCompile(`(?i)^[\s⎿●⏺]*(API Error: *(?:5\d\d\b|.*(?:overloaded|internal server error)).*)$`)

// apiErrorOnScreen finds the transient provider error Claude printed as the
// last output of the pane.
func apiErrorOnScreen(screen string) (string, bool) {
	lines := strings.Split(screen, "\n")
	lines = lines[max(0, len(lines)-reviewerAPIErrorTailLines):]
	found := ""
	for _, line := range lines {
		if m := screenAPIError.FindStringSubmatch(line); m != nil {
			found = strings.TrimSpace(m[1])
		}
	}
	if len(found) > 200 {
		found = strings.ToValidUTF8(found[:200], "")
	}
	return found, found != ""
}
