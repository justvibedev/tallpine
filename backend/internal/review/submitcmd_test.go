package review

import (
	"fmt"
	"strings"
	"testing"
)

const testJSONLiteral = `'{"event":"COMMENT","body":"it'\''s fine"}'`

func TestSubmitCommandTemplatesRoundTripThroughMatcher(t *testing.T) {
	submit := fmt.Sprintf(SubmitCommandTemplate, testJSONLiteral, "worker-1")
	got, ok := MatchSubmitCommand(submit)
	if !ok || got.Session != "worker-1" || got.JSONLiteral != testJSONLiteral || got.IsGitHubReview() {
		t.Fatalf("submit render did not match: ok=%v %+v", ok, got)
	}
	gh := fmt.Sprintf(GitHubReviewCommandTemplate, testJSONLiteral, "Acme", "repo.x", "42")
	got, ok = MatchSubmitCommand("  " + gh + "\n")
	if !ok || !got.IsGitHubReview() || got.Owner != "Acme" || got.Repo != "repo.x" || got.Number != "42" {
		t.Fatalf("github render did not match: ok=%v %+v", ok, got)
	}
	if !got.PRAllowed("other/x/1, acme/REPO.x/42") || got.PRAllowed("acme/repo.x/43") || got.PRAllowed("") {
		t.Fatal("PRAllowed must be case-insensitive and exact")
	}
}

func TestMatchSubmitCommandRejectsVariants(t *testing.T) {
	good := fmt.Sprintf(SubmitCommandTemplate, testJSONLiteral, "worker-1")
	variants := map[string]string{
		"double space":    strings.Replace(good, "review submit", "review  submit", 1),
		"extra flag":      strings.Replace(good, "--reviews -", "--reviews - --force", 1),
		"flag reordered":  fmt.Sprintf("printf '%%s' %s | tallpine review submit --reviews - --session worker-1", testJSONLiteral),
		"chained command": good + "; rm -rf /",
		"subshell":        strings.Replace(good, "worker-1", "$(id)", 1),
		"double quoted":   `printf '%s' "{}" | tallpine review submit --session w --reviews -`,
		"echo not printf": strings.Replace(good, "printf '%s'", "echo", 1),
		"gh extra flag":   fmt.Sprintf(GitHubReviewCommandTemplate, testJSONLiteral, "o", "r", "1") + " --verbose",
		"gh other path":   strings.Replace(fmt.Sprintf(GitHubReviewCommandTemplate, testJSONLiteral, "o", "r", "1"), "/pulls/1/reviews", "/issues/1/comments", 1),
	}
	for name, cmd := range variants {
		if _, ok := MatchSubmitCommand(cmd); ok {
			t.Errorf("%s: matched %q", name, cmd)
		}
	}
}

func TestSubmitCommandEventAllowed(t *testing.T) {
	tests := map[string]bool{
		`'{"event":"COMMENT"}'`:                       true,
		`'{"event":"REQUEST_CHANGES","body":"x"}'`:    true,
		`'{"event":"APPROVE"}'`:                       false,
		`'{"body":"x"}'`:                              false,
		`'{"event":"COMMENT","event":"APPROVE"}'`:     false,
		`'{"event":"COMMENT","Event":"APPROVE"}'`:     false,
		`'["event"]'`:                                 false,
		`''`:                                          false,
		`'{"event":"COMMENT","body":"it'\''s fine"}'`: true,
	}
	for literal, want := range tests {
		if got := (SubmitCommand{JSONLiteral: literal}).EventAllowed(); got != want {
			t.Errorf("EventAllowed(%s) = %v, want %v", literal, got, want)
		}
	}
}

func TestReviewPromptRendersSubmitTemplates(t *testing.T) {
	prompt, _ := reviewTexts(LaunchSpec{WorkerID: "worker-9"})
	for _, want := range []string{
		`    printf '%s' '{ "event": "COMMENT", "body": "<summary>", "comments": [ { "path": "<file>", "line": <n>, "body": "<finding>" } ] }' | gh api --method POST repos/{owner}/{repo}/pulls/{number}/reviews --input - --jq '.id'`,
		` | tallpine review submit --session worker-9 --reviews -`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
