package review

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func failureSubject(run domain.ReviewRun, prNumber int) string {
	var b strings.Builder
	b.WriteString(brand.ProductName + " review of ")
	if prNumber > 0 {
		fmt.Fprintf(&b, "PR #%d", prNumber)
	} else {
		b.WriteString(run.PRURL)
	}
	if sha := run.TargetSHA; sha != "" {
		if len(sha) > 12 {
			sha = sha[:12]
		}
		fmt.Fprintf(&b, " (head %s)", sha)
	}
	if run.Harness != "" {
		fmt.Fprintf(&b, " by %s", run.Harness)
		if run.Model != "" {
			fmt.Fprintf(&b, " (%s)", run.Model)
		}
	}
	return b.String()
}

func failureVerb(run domain.ReviewRun) string {
	if run.Status == domain.ReviewRunCancelled {
		return "was cancelled"
	}
	return "failed"
}

func failureReason(run domain.ReviewRun) string {
	reason := strings.Join(strings.Fields(domain.SanitizeControlChars(run.Body)), " ")
	if reason == "" {
		return "no reason was recorded"
	}
	return strings.TrimSuffix(truncateNotice(reason, 500), ".")
}

func truncateNotice(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
