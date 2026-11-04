package review

import (
	"encoding/json"
	"regexp"
	"strings"
)

// The two command shapes the reviewer prompt dictates. Both feed a
// single-quoted JSON literal through `printf '%s'`, so the shell performs no
// expansion on it. The prompt renders them from these templates and the
// headless-reviewer permission hook admits only what MatchSubmitCommand
// recognises, so the instruction and the allow rule cannot drift apart.
const (
	// SubmitCommandTemplate takes the JSON literal and the worker session id.
	SubmitCommandTemplate = "printf '%%s' %s | tallpine review submit --session %s --reviews -"
	// GitHubReviewCommandTemplate takes the JSON literal, owner, repo and PR number.
	GitHubReviewCommandTemplate = "printf '%%s' %s | gh api --method POST repos/%s/%s/pulls/%s/reviews --input - --jq '.id'"
)

// submitJSONLiteral is one shell single-quoted literal, with the
// quote-backslash-quote-quote escape for embedded single quotes.
const submitJSONLiteral = `'[^']*(?:'\\''[^']*)*'`

// Claude Code ≥ 2.1.257 prompts on any Bash command its analyzer cannot verify
// statically, and allow rules never match such commands, so a headless
// reviewer would hang. The shapes are safe to auto-allow: `printf '%s'`
// performs no format interpretation and a single-quoted operand cannot expand
// or run anything.
var submitCommandPattern = regexp.MustCompile(`^printf '%s' (?P<json>` + submitJSONLiteral + `) \| (?:` +
	`gh api --method POST repos/(?P<owner>[A-Za-z0-9_.-]+)/(?P<repo>[A-Za-z0-9_.-]+)/pulls/(?P<number>[0-9]+)/reviews --input - --jq '\.id'` +
	`|tallpine review submit --session (?P<session>[A-Za-z0-9_-]+) --reviews -)$`)

// SubmitCommand is a recognised reviewer submit command. Session is set for
// the Tallpine submit form; Owner, Repo and Number for the GitHub review POST.
type SubmitCommand struct {
	JSONLiteral string
	Session     string
	Owner       string
	Repo        string
	Number      string
}

// MatchSubmitCommand recognises the exact reviewer submit command shapes.
// Surrounding whitespace is ignored; any other variation does not match.
func MatchSubmitCommand(cmd string) (SubmitCommand, bool) {
	m := submitCommandPattern.FindStringSubmatch(strings.TrimSpace(cmd))
	if m == nil {
		return SubmitCommand{}, false
	}
	group := func(name string) string { return m[submitCommandPattern.SubexpIndex(name)] }
	return SubmitCommand{
		JSONLiteral: group("json"),
		Session:     group("session"),
		Owner:       group("owner"),
		Repo:        group("repo"),
		Number:      group("number"),
	}, true
}

// IsGitHubReview reports whether the command posts a GitHub review.
func (c SubmitCommand) IsGitHubReview() bool { return c.Number != "" }

// PRAllowed reports whether the command's PR (owner/repo/number) is one of the
// comma-separated owner/repo/number entries the run was launched with. GitHub
// owner and repo names are case-insensitive.
func (c SubmitCommand) PRAllowed(allowed string) bool {
	path := c.Owner + "/" + c.Repo + "/" + c.Number
	for _, entry := range strings.Split(allowed, ",") {
		if entry = strings.TrimSpace(entry); entry != "" && strings.EqualFold(entry, path) {
			return true
		}
	}
	return false
}

// EventAllowed unquotes the single-quoted shell literal and accepts a JSON
// object whose event is exactly COMMENT or REQUEST_CHANGES, never APPROVE.
func (c SubmitCommand) EventAllowed() bool {
	literal := c.JSONLiteral
	if len(literal) < 2 {
		return false
	}
	raw := strings.ReplaceAll(literal[1:len(literal)-1], `'\''`, `'`)
	// Walk the top-level tokens so duplicate keys and case variants of "event"
	// (which encoding/json would fold) cannot make this parser disagree with
	// GitHub's about which event wins.
	dec := json.NewDecoder(strings.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	event := ""
	for dec.More() {
		tok, err := dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok || seen[key] {
			return false
		}
		seen[key] = true
		var val json.RawMessage
		if dec.Decode(&val) != nil {
			return false
		}
		if key == "event" {
			if json.Unmarshal(val, &event) != nil {
				return false
			}
		} else if strings.EqualFold(key, "event") {
			return false
		}
	}
	return event == "COMMENT" || event == "REQUEST_CHANGES"
}
