package lifecycle

import (
	"fmt"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// commentNudgeKey identifies one review comment's nudge. It must be unique per
// comment, not per thread: the observer expands a thread into one comment row
// each (observer.go), all sharing the thread id, so keying on the thread would
// put several comments with several signatures back in one dedup slot -- the
// rotation this key exists to prevent. A comment with no id falls back to its
// thread, which is still better than colliding with every other comment.
func commentNudgeKey(prURL string, comment ports.PRCommentObservation) string {
	id := strings.TrimSpace(comment.ID)
	if id == "" {
		id = strings.TrimSpace(comment.ThreadID)
	}
	return "comment:" + prURL + ":" + id
}

func hasUnresolvedComments(comments []ports.PRCommentObservation) bool {
	for _, c := range comments {
		if !c.Resolved {
			return true
		}
	}
	return false
}

func failedPRChecks(checks []ports.PRCheckObservation) []ports.PRCheckObservation {
	failed := make([]ports.PRCheckObservation, 0, len(checks))
	for _, ch := range checks {
		if ch.Status == domain.PRCheckFailed {
			failed = append(failed, ch)
		}
	}
	return failed
}

func ciFailureSignature(checks []ports.PRCheckObservation) string {
	parts := make([]string, 0, len(checks))
	for _, ch := range checks {
		parts = append(parts, strings.Join([]string{ch.Name, ch.CommitHash, string(ch.Status), ch.URL, ch.LogTail}, "\x00"))
	}
	return strings.Join(parts, "\x01")
}

func formatCIFailureMessage(checks []ports.PRCheckObservation) string {
	var msg strings.Builder
	msg.WriteString("CI is failing on your PR.\n")
	for _, ch := range checks {
		name := domain.SanitizeControlChars(ch.Name)
		if strings.TrimSpace(name) == "" {
			name = "unnamed check"
		}
		status := domain.SanitizeControlChars(string(ch.Status))
		if strings.TrimSpace(status) == "" {
			status = "failed"
		}
		fmt.Fprintf(&msg, "\nFailed: %s (%s)", name, status)
		if ch.URL != "" {
			fmt.Fprintf(&msg, "\nFailure URL: %s", domain.SanitizeControlChars(ch.URL))
		}
		if ch.LogTail != "" {
			// LogTail is raw CI job output; sanitize before it reaches the
			// agent's live pane so embedded escape sequences can't drive the
			// terminal (the dedup signature stays on the raw bytes). The fence
			// grows to contain embedded backtick fences without mutating logs.
			tail := domain.SanitizeControlChars(ch.LogTail)
			fence := markdownCodeFence(tail)
			lineCount := len(strings.Split(tail, "\n"))
			lineLabel := "lines"
			if lineCount == 1 {
				lineLabel = "line"
			}
			fmt.Fprintf(&msg, "\n\nLog tail (last %d %s):\n%s\n%s\n%s", lineCount, lineLabel, fence, tail, fence)
		}
		msg.WriteString("\n")
	}
	msg.WriteString("\nUse the included log tail and failure URL first; fetch full CI logs only if you need additional context. Fix the issues and push again.")
	return msg.String()
}

func unresolvedReviewComments(comments []ports.PRCommentObservation) []ports.PRCommentObservation {
	unresolved := make([]ports.PRCommentObservation, 0, len(comments))
	for _, c := range comments {
		if c.Resolved {
			continue
		}
		unresolved = append(unresolved, c)
	}
	return unresolved
}

func reviewCommentsSignature(comments []ports.PRCommentObservation) string {
	parts := make([]string, 0, len(comments))
	for _, c := range comments {
		id := strings.TrimSpace(c.ID)
		threadID := strings.TrimSpace(c.ThreadID)
		if id == "" && threadID == "" {
			continue
		}
		parts = append(parts, threadID+"\x00"+id)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}

func formatReviewChangesRequestedMessage(review domain.PullRequestReview) string {
	var msg strings.Builder
	author := domain.SanitizeControlChars(review.Author)
	if strings.TrimSpace(author) == "" {
		author = "unknown reviewer"
	}
	fmt.Fprintf(&msg, "A changes-requested review from @%s is on your PR.", author)
	if body := domain.SanitizeControlChars(review.Body); strings.TrimSpace(body) != "" {
		fmt.Fprintf(&msg, "\n\nReview body:\n%s", body)
	}
	if review.URL != "" {
		fmt.Fprintf(&msg, "\n\nReview URL: %s", domain.SanitizeControlChars(review.URL))
	}
	if review.ID != "" {
		fmt.Fprintf(&msg, "\nReview ID: %s", domain.SanitizeControlChars(review.ID))
	}
	msg.WriteString("\n\nAddress the requested changes and push. You should not need to re-fetch the review unless you need additional context beyond what Tallpine has provided here.")
	return msg.String()
}

func formatReviewCommentsMessage(comments []ports.PRCommentObservation) string {
	if len(comments) == 0 {
		return "A reviewer left feedback on your PR. Address it and push. Fetch the review details only if you need additional context beyond what Tallpine has provided here."
	}
	var msg strings.Builder
	fmt.Fprintf(&msg, "The following %d unresolved review comment(s) are on your PR as of just now. You should not need to re-fetch this data unless you need additional context.\n", len(comments))
	for i, c := range comments {
		location := "(general)"
		if c.File != "" {
			location = domain.SanitizeControlChars(c.File)
			if c.Line > 0 {
				location = fmt.Sprintf("%s:%d", location, c.Line)
			}
		}
		author := domain.SanitizeControlChars(c.Author)
		if strings.TrimSpace(author) == "" {
			author = "unknown reviewer"
		}
		// Comment bodies are attacker-influenced (anyone who can comment on the
		// PR) and get pasted into the agent's live pane; strip control/escape
		// chars before formatting them.
		body := domain.SanitizeControlChars(c.Body)
		fmt.Fprintf(&msg, "\n%d. %s (@%s):\n%s", i+1, location, author, body)
		if c.URL != "" {
			fmt.Fprintf(&msg, "\n   %s", domain.SanitizeControlChars(c.URL))
		}
		if c.ThreadID != "" {
			fmt.Fprintf(&msg, "\n   Thread ID: %s", domain.SanitizeControlChars(c.ThreadID))
		}
		msg.WriteString("\n")
	}
	msg.WriteString("\nAddress each comment and push fixes. Use the thread ID to resolve each thread directly after pushing when available. You should not need to re-fetch review data unless you need additional context beyond what is provided here.")
	return msg.String()
}

func markdownCodeFence(s string) string {
	maxRun := 0
	run := 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > maxRun {
				maxRun = run
			}
			continue
		}
		run = 0
	}
	if maxRun < 3 {
		return "```"
	}
	return strings.Repeat("`", maxRun+1)
}
