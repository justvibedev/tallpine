package skillassets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

func TestEmbeddedSkillFrontmatterIsValidYAML(t *testing.T) {
	body, err := files.ReadFile("using-tallpine/SKILL.md")
	if err != nil {
		t.Fatalf("read embedded SKILL.md: %v", err)
	}

	parts := strings.SplitN(string(body), "---", 3)
	if len(parts) != 3 {
		t.Fatal("embedded SKILL.md is missing YAML frontmatter delimiters")
	}

	var frontmatter struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Trigger     string `yaml:"trigger"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &frontmatter); err != nil {
		t.Fatalf("parse embedded SKILL.md frontmatter: %v", err)
	}
	if frontmatter.Name != SkillName {
		t.Fatalf("frontmatter name = %q, want %q", frontmatter.Name, SkillName)
	}
	if strings.TrimSpace(frontmatter.Description) == "" {
		t.Fatal("frontmatter description is empty")
	}
	if strings.TrimSpace(frontmatter.Trigger) == "" {
		t.Fatal("frontmatter trigger is empty")
	}
}

func TestEmbeddedPreviewGuidanceDoesNotScaffoldStaticSites(t *testing.T) {
	previewBody, err := files.ReadFile("using-tallpine/commands/preview.md")
	if err != nil {
		t.Fatalf("read embedded preview guidance: %v", err)
	}
	previewText := string(previewBody)
	normalizedPreviewText := strings.Join(strings.Fields(previewText), " ")
	for _, required := range []string{
		"Static HTML and Markdown do not need a development server",
		"Never create or modify `package.json`",
		"Do not create `.tallpine/launch.json` unless the user asks",
		"reuse the repository's existing dev command",
		"without waiting for a separate \"open it\" request",
		"Do not steal the browser from an active application",
		"open the primary requested artifact",
		"relative to the session workspace root, not the shell's current directory",
		"Do not compensate for the current directory with `../README.md`",
		"existing confined loopback preview origin",
		"not a deployment or a new development server",
		"Do not open workspace files with `file://`",
	} {
		if !strings.Contains(normalizedPreviewText, required) {
			t.Fatalf("preview guidance missing %q:\n%s", required, previewText)
		}
	}

	skillBody, err := files.ReadFile("using-tallpine/SKILL.md")
	if err != nil {
		t.Fatalf("read embedded SKILL.md: %v", err)
	}
	skillText := string(skillBody)
	normalizedSkillText := strings.Join(strings.Fields(skillText), " ")
	if !strings.Contains(normalizedSkillText, "[commands/preview.md](commands/preview.md) before acting") ||
		!strings.Contains(normalizedSkillText, "automatic-handoff rules are load-bearing") ||
		!strings.Contains(normalizedSkillText, "[commands/browser.md](commands/browser.md)") ||
		!strings.Contains(normalizedSkillText, "opt-in network policy") {
		t.Fatalf("skill catalog is missing focused preview/browser routing:\n%s", skillText)
	}
}

func TestEmbeddedBrowserGuidanceKeepsNetworkCaptureOptional(t *testing.T) {
	body, err := files.ReadFile("using-tallpine/commands/browser.md")
	if err != nil {
		t.Fatalf("read embedded browser guidance: %v", err)
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	for _, required := range []string{
		"Network capture is optional and disabled by default",
		"Do not enable it for routine navigation or interaction",
		"no request or response bodies, credentials, cookies, or query values",
		"`network status` and `network list` never enable capture",
		"The user can select or close these same tabs",
		"`viewport <width>x<height>` emulates a fixed CSS viewport",
		"`viewport reset` restores the panel's own size",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("browser guidance missing %q:\n%s", required, body)
		}
	}
}

func TestEmbeddedSessionGuidanceDocumentsCanonicalInAppLinks(t *testing.T) {
	body, err := files.ReadFile("using-tallpine/commands/session.md")
	if err != nil {
		t.Fatalf("read embedded session guidance: %v", err)
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	for _, required := range []string{
		"tallpine://sessions/{project-id}/{session-id}",
		"Never use display names as identity",
		"Encode either ID with URL percent encoding",
		"Do not append query strings, fragments, action names, credentials, authorities, or extra path segments",
		"only inside a currently running Tallpine desktop app",
		"never sends a message, executes a command, or performs another action",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("session-link guidance missing %q:\n%s", required, body)
		}
	}
}

func TestEmbeddedSpawnGuidanceListsOnlySupportedHarnesses(t *testing.T) {
	body, err := files.ReadFile("using-tallpine/commands/spawn.md")
	if err != nil {
		t.Fatalf("read embedded spawn guidance: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "`claude-code`, `codex`, `kiro`") {
		t.Fatalf("spawn guidance does not list the supported harnesses:\n%s", text)
	}
	if strings.Contains(text, "`opencode-v2`") {
		t.Fatalf("spawn guidance still lists a removed harness:\n%s", text)
	}
}

func TestEmbeddedReviewGuidanceSeparatesExternalReviewFromOwnership(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{"using-tallpine/commands/review.md", "normal review-only worker without claiming or attaching the target PR"},
		{"using-tallpine/commands/spawn.md", "Spawn a normal worker to review someone else's PR without taking ownership"},
		{"using-tallpine/commands/session.md", "Reviewing someone else's PR"},
	} {
		body, err := files.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(strings.Fields(string(body)), " "), tc.want) {
			t.Fatalf("%s missing %q", tc.path, tc.want)
		}
	}
	spawn, _ := files.ReadFile("using-tallpine/commands/spawn.md")
	if strings.Contains(string(spawn), `--name "review-pr-88" --claim-pr`) {
		t.Fatal("spawn guidance still claims the review target")
	}
}

// TestInstall_WritesSkillAndIsIdempotent: Install must lay down the embedded
// skill (SKILL.md plus a commands file) under <dataDir>/skills/using-tallpine, and a
// second run must clobber cleanly, leaving no stale files. This is the whole
// contract the daemon boot hook relies on.
func TestInstall_WritesSkillAndIsIdempotent(t *testing.T) {
	dataDir := t.TempDir()

	if err := Install(dataDir); err != nil {
		t.Fatalf("Install: %v", err)
	}

	skillFile := filepath.Join(Dir(dataDir), "SKILL.md")
	if b, err := os.ReadFile(skillFile); err != nil {
		t.Fatalf("read %s: %v", skillFile, err)
	} else if len(b) == 0 {
		t.Fatalf("SKILL.md is empty")
	}
	if _, err := os.Stat(filepath.Join(Dir(dataDir), "commands", "spawn.md")); err != nil {
		t.Fatalf("commands/spawn.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(Dir(dataDir), "commands", "cue.md")); err != nil {
		t.Fatalf("commands/cue.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(Dir(dataDir), "commands", "browser.md")); err != nil {
		t.Fatalf("commands/browser.md missing: %v", err)
	}

	// A stale file inside the skill dir must not survive a reinstall (clobber).
	stale := filepath.Join(Dir(dataDir), "stale.md")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}
	if err := Install(dataDir); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file survived reinstall (err=%v)", err)
	}
}

// TestMaterialize_WritesIntoArbitraryDest covers the opencode adapter path:
// materialize the skill into .opencode/skills/using-tallpine (not the data-dir layout).
func TestMaterialize_WritesIntoArbitraryDest(t *testing.T) {
	dest := filepath.Join(t.TempDir(), ".opencode", "skills", SkillName)
	if err := Materialize(dest); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	} else if len(b) == 0 {
		t.Fatal("SKILL.md is empty")
	}
	if _, err := os.Stat(filepath.Join(dest, "commands", "spawn.md")); err != nil {
		t.Fatalf("commands/spawn.md missing: %v", err)
	}
}

func TestEmbeddedReportGuidanceDistinguishesDeliverablesFromDiagnostics(t *testing.T) {
	body, err := files.ReadFile("using-tallpine/commands/report.md")
	if err != nil {
		t.Fatal(err)
	}
	guidance := strings.Join(strings.Fields(string(body)), " ")
	for _, want := range []string{
		"working material, not deliverables",
		"Do not attach them unless requested or needed to explain an actionable failure",
		"Summarize validation in the report note",
		"not external publishing authorization",
		"In a chat session, an HTML file from the artifact directory that you attach with `--artifact` also shows in the thread.",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("report guidance missing %q", want)
		}
	}
}

func TestEmbeddedBacklogDocsCoverDeferAndBacklog(t *testing.T) {
	report, err := files.ReadFile("using-tallpine/commands/report.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"`--defer \"<one line>\"`", "at most 3 per report", "1-200 characters", "inbox"} {
		if !strings.Contains(strings.Join(strings.Fields(string(report)), " "), want) {
			t.Fatalf("report doc missing %q", want)
		}
	}
	backlog, err := files.ReadFile("using-tallpine/commands/backlog.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tallpine backlog next", "one level", "blocked_by", "every PR linked so far", "split it into child items"} {
		if !strings.Contains(string(backlog), want) {
			t.Fatalf("backlog doc missing %q", want)
		}
	}
	if strings.Contains(string(backlog), "--defer") {
		t.Fatal("--defer must be documented only in report.md")
	}
}

func TestEmbeddedRenderGuidanceSeparatesRendersFromArtifacts(t *testing.T) {
	body, err := files.ReadFile("using-tallpine/commands/render.md")
	if err != nil {
		t.Fatal(err)
	}
	guidance := strings.Join(strings.Fields(string(body)), " ")
	for _, want := range []string{
		"## tallpine render or a session artifact",
		"when the page answers a question in the thread",
		"attach it with `tallpine report --artifact <path>`",
		"In a chat session, an HTML file from the artifact directory that you attach with `--artifact` also shows in the thread.",
		"add `--artifact` to `tallpine render` (or set `artifact: true` in `html_render`). Do this only when the user asks to keep the page.",
	} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("render guidance missing %q", want)
		}
	}
}
