package brand

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// legacyLiteral matches the pre-rename (Vibe Orchestrator) identity. Anything
// that needs one on purpose (compat readers, the migrator, history docs) is
// listed in legacyAllowlist; everything else must derive from this package.
var legacyLiteral = regexp.MustCompile(strings.Join([]string{
	`Vibe Orchestrator`, `vibe-orchestrator`, `ee\.justdev\.vo\b`, `ee\.justdev\.vibe`,
	`(?:^|[^A-Za-z0-9_])VO_[A-Z]`, `(?:^|[^\w.-])\.vo(?:[^\w-]|$)`, `using-vo\b`, `\bvo-app\b`,
	`(?:^|[^\w.-])vo://`, `justvibedev/aox`, `"vo"`, `'vo'`, "`vo`",
	`(?:^|[^\w/.-])vo (?:hooks|session|spawn|report|send|start|stop|status|daemon|review|project|doctor|preview|browser|backlog|render|update|policy|cue|pr|link|mcp)\b`,
}, "|"))

// legacyAllowlist maps a repo-relative path prefix to why legacy names may
// appear under it.
var legacyAllowlist = map[string]string{
	// Compat readers and the migrator: they exist to recognise the old names.
	"backend/internal/brand/":                                      "defines the Legacy* identity and its tests",
	"backend/internal/migratevo/":                                  "the migrate-from-vo migrator",
	"backend/internal/cli/migrate_from_vo.go":                      "migrator command",
	"backend/internal/domain/repository_identity.go":               "old repository name (aox) for PR attribution",
	"backend/internal/service/session/action_activity":             "stored vo:// action links stay parseable",
	"backend/internal/storage/sqlite/migrations/":                  "historical migrations",
	"packages/mobile/lib/brand":                                    "legacy link schemes of the mobile app",
	"frontend/src/shared/session-link-parse":                       "vo:// links stay parseable",
	"packages/shared/chat/automation/worker-reports.test.ts":       "reads legacy vo:// and ao:// report links",
	"frontend/src/shared/brand":                                    "defines the legacy identity",
	"frontend/src/renderer/lib/session-links":                      "vo:// links stay parseable",
	"scripts/release-local":                                        "reads the legacy home, release.env and VO_* fallback",
	"scripts/lib/notify.sh":                                        "drops the legacy VO_SESSION_ID so a script never sends as a session",
	"backend/internal/adapters/agent/hooksjson/":                   "hook entries written under the legacy CLI name",
	"backend/internal/adapters/agent/codex/hooks":                  "hook entries written under the legacy CLI name",
	"backend/internal/adapters/agent/kiro/hooks":                   "hook entries written under the legacy CLI name",
	"backend/internal/adapters/agent/claudecode/hooks_migrate":     "migrator for hook entries written under the legacy CLI name",
	"backend/internal/adapters/container/dockerreap/":              "reaps legacy vo.* labelled containers",
	"backend/internal/observe/scm/":                                "legacy vo/ and ao/ session branches",
	"backend/internal/service/cleanup/":                            "legacy vo/ session branches",
	"backend/internal/service/systeminstall/":                      "removes legacy launchd/CLI installs",
	"backend/internal/officialimport/":                             "reads the official AO install",
	"backend/internal/httpd/mobile_link_rewrite":                   "phone-facing link rewrite",
	"backend/internal/adapters/agent/codex/managed_marker_test.go": "tests hook entries under the legacy CLI name",
	"backend/internal/adapters/agent/kiro/managed_marker_test.go":  "tests hook entries under the legacy CLI name",
	"backend/internal/daemon/legacy_guard":                         "the legacy-install startup guard",
	// Internal wire/format names kept on purpose.
	"frontend/forge.config.ts":            "VOBuild* Info.plist keys (internal stamp)",
	"frontend/src/docs/content/changelog": "changelog entries name the old product",
	"NOTICE":                              "attribution text",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func legacyAllowed(rel string) bool {
	for prefix := range legacyAllowlist {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

func TestNoLegacyBrandLiterals(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	root := repoRoot(t)
	cmd := exec.Command(git, "-C", root, "ls-files", "-z")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	var hits []string
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" || legacyAllowed(rel) || strings.HasSuffix(rel, "package-lock.json") {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, rel))
		if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if m := legacyLiteral.FindString(line); m != "" {
				hits = append(hits, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
	}
	sort.Strings(hits)
	if len(hits) > 0 {
		shown := hits
		if len(shown) > 60 {
			shown = shown[:60]
		}
		t.Fatalf("%d legacy brand literal(s) outside the allowlist (derive from the brand helpers, or allowlist with a reason):\n%s", len(hits), strings.Join(shown, "\n"))
	}
}

// agentFacingDirs hold text the product hands to agents or users verbatim.
// They must never name the upstream product or its state dir.
var agentFacingDirs = []string{
	"backend/internal/skillassets/",
	"backend/internal/session_manager/",
	"backend/internal/autoreview/",
	"backend/internal/reviewgateway/",
	"backend/internal/service/session/",
	"backend/internal/orchestrator/",
}

// goModulePath is the invisible Go import path; it is not user-facing.
const goModulePath = "tallpine.eu/tallpine/backend/"

var upstreamLiteral = regexp.MustCompile(`agent-orchestrator|~/\.ao\b|\$HOME/\.ao\b|aoagents/|Untrivial`)

func TestAgentFacingTextHasNoUpstreamBrand(t *testing.T) {
	root := repoRoot(t)
	var hits []string
	for _, dir := range agentFacingDirs {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			continue
		}
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(p, "_test.go") {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(data), "\n") {
				if upstreamLiteral.MatchString(strings.ReplaceAll(line, goModulePath, "")) {
					rel, _ := filepath.Rel(root, p)
					hits = append(hits, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(hits) > 0 {
		t.Fatalf("upstream brand in agent-facing text:\n%s", strings.Join(hits, "\n"))
	}
}
