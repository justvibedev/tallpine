package sessionmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B686: plans, specs, handoff notes and screenshots piled up in repos because
// skills (superpowers writing-plans, brainstorming) name their own repo save
// paths. The artifact prompt must override those paths by name.
func TestArtifactPrompt_OverridesSkillDefaultSavePaths(t *testing.T) {
	t.Parallel()

	m := &Manager{dataDir: t.TempDir()}
	for _, chat := range []bool{false, true} {
		sp := m.artifactPrompt("mer-7", chat)
		for _, want := range []string{
			"overrides any default save path a skill or plugin names",
			"`docs/superpowers/plans/`",
			"`docs/superpowers/specs/`",
			"`docs/plans/`",
			"`.plan/`",
			"`docs/memories/`",
			"you do not commit them",
			"Screenshots and audit images go here when they are a deliverable and into the system temp directory when they are scratch",
			"delete it in the pull request that finishes the work",
		} {
			if !strings.Contains(sp, want) {
				t.Fatalf("artifact prompt (chat=%v) missing %q:\n%s", chat, want, sp)
			}
		}
	}
}

func TestKill_RemovesEmptyArtifactDirButKeepsOneWithFiles(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	m.dataDir = t.TempDir()
	st.sessions["mer-1"] = mkLive("mer-1")
	st.sessions["mer-2"] = mkLive("mer-2")
	empty := m.reserveArtifactDir("mer-1")
	full := m.reserveArtifactDir("mer-2")
	if err := os.WriteFile(filepath.Join(full, "plan.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.SessionID{"mer-1", "mer-2"} {
		if _, err := m.Kill(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Fatalf("empty artifact dir survived kill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(full, "plan.md")); err != nil {
		t.Fatalf("artifact file removed on kill: %v", err)
	}
}

func TestRestore_RecreatesArtifactDirDroppedByKill(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	m.dataDir = t.TempDir()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	dir := m.artifactDir("mer-1")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("precondition: dir should be absent: %v", err)
	}
	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("artifact dir not recreated on restore: %v", err)
	}
}

// A session created before the per-project layout keeps its flat dir across a
// restore: the restored agent is told, and the dir is recreated, at the path
// its row names (H1).
func TestRestore_LegacyFlatArtifactDirStaysFlat(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newManager()
	m.dataDir = t.TempDir()
	flat := filepath.Join(m.dataDir, "artifacts", "mer-1")
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x", ArtifactDir: flat})
	if got := m.artifactDir("mer-1"); got != flat {
		t.Fatalf("artifactDir = %q, want the stored flat path %q", got, flat)
	}
	if !strings.Contains(m.artifactPrompt("mer-1", false), filepath.ToSlash(flat)) {
		t.Fatal("prompt must name the flat path")
	}
	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(flat); err != nil || !info.IsDir() {
		t.Fatalf("flat dir not recreated on restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.dataDir, "artifacts", "mer")); !os.IsNotExist(err) {
		t.Fatalf("a canonical dir was created for a legacy session: %v", err)
	}
}
