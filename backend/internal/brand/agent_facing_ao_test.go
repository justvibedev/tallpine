package brand

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// promptDirs and promptFiles hold the text handed verbatim to agents (system
// prompts, spawn prompts, reviewer prompts, skill docs). The product is called
// Tallpine there; the upstream short name "AO" must not leak back in.
var promptDirs = []string{
	"backend/internal/skillassets/",
	"backend/internal/session_manager/",
	"backend/internal/autoreview/",
	"backend/internal/review/",
	"backend/internal/reviewgateway/",
	"backend/internal/orchestrator/",
	"backend/internal/adapters/reviewer/",
}

var promptFiles = []string{
	"backend/internal/adapters/chatdriver/acp/system_prompt_file.go",
	"backend/internal/service/chat/prompt_suggestion.go",
	"backend/internal/service/chat/prompt_suggestion_fork.go",
}

// aoWord matches AO / Ao / ao used as a standalone name. Path segments, dotted
// names and identifier parts are not matched (the character before must not be
// a word character, '.', '/', '~' or '-').
var aoWord = regexp.MustCompile(`(^|[^\w./~-])(AO|Ao|ao)($|[^\w])`)

// aoWireTokens are persisted or wire names that legitimately keep the old
// spelling; they are removed from a line before it is checked.
var aoWireTokens = strings.NewReplacer(
	"ao-handoff-request", "", "AO-HANDOFF-REQUEST", "", "ao_standing_instructions", "",
	"ao-replayed-conversation", "", "ao-report-delivery", "", "ao-session-delivery", "",
	"ao-standing-instructions", "", "ao-worker-reports", "",
)

// officialProductLine allowlists lines that deliberately name the OFFICIAL
// Agent Orchestrator product (detection of its hooks, env vars or state dir) or
// the legacy ao/ branch namespace.
var officialProductLine = regexp.MustCompile(`(?i)official|upstream|legacy|\bao hooks\b`)

func aoLeaks(text string) []int {
	var lines []int
	for i, line := range strings.Split(text, "\n") {
		line = aoWireTokens.Replace(line)
		if aoWord.MatchString(line) && !officialProductLine.MatchString(line) {
			lines = append(lines, i+1)
		}
	}
	return lines
}

func TestPromptTextNeverNamesAO(t *testing.T) {
	root := repoRoot(t)
	var hits []string
	check := func(p string) {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		rel, _ := filepath.Rel(root, p)
		for _, n := range aoLeaks(string(data)) {
			hits = append(hits, rel+":"+strconv.Itoa(n)+": "+strings.TrimSpace(lines[n-1]))
		}
	}
	for _, dir := range promptDirs {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(p, "_test.go") {
				return err
			}
			check(p)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range promptFiles {
		check(filepath.Join(root, f))
	}
	if len(hits) > 0 {
		t.Fatalf("agent-facing text names \"AO\" (say Tallpine, or mark an official-product reference with the word \"official\"):\n%s", strings.Join(hits, "\n"))
	}
}

// TestAOLeakDetectorMatchesPromptWording pins the detector itself, so a
// regex edit cannot silently turn the guard into a no-op.
func TestAOLeakDetectorMatchesPromptWording(t *testing.T) {
	for _, bad := range []string{
		"You are an AO worker.", "Use the AO session link", "ask the AO review gate", "AO's own server", "the ao CLI", "(AO)", "start AO.",
	} {
		if len(aoLeaks(bad)) == 0 {
			t.Errorf("not flagged: %q", bad)
		}
	}
	for _, ok := range []string{
		"Tallpine session", "the official AO install", "never touch the official `ao hooks` entries",
		"<ao-handoff-request>", "a chaos monkey", "Kaolin and dao", "workspace/.ao/attachments", "legacy ao/<id> branch", "AO_SESSION_ID is official",
	} {
		if got := aoLeaks(ok); len(got) != 0 {
			t.Errorf("false positive: %q", ok)
		}
	}
}
