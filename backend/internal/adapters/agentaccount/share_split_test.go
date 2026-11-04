package agentaccount

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func preparedClaude(t *testing.T) (c *Claude, device, home string) {
	t.Helper()
	device, home = t.TempDir(), filepath.Join(t.TempDir(), "acct")
	return &Claude{DeviceHome: device, DeviceConfig: filepath.Join(t.TempDir(), ".claude.json")}, device, home
}

func TestClaudePrepareSharesTeamsRulesWorkflowsAndMemory(t *testing.T) {
	c, device, home := preparedClaude(t)
	write(t, filepath.Join(device, "teams", "t1", "config.json"), "{}")
	if err := c.Prepare(home); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"teams", "rules", "workflows", "memory", "agent-memory"} {
		linksTo(t, filepath.Join(home, name), filepath.Join(device, name))
	}
	if _, err := os.Stat(filepath.Join(home, "teams", "t1", "config.json")); err != nil {
		t.Fatalf("device team not visible: %v", err)
	}
	// Claude 2.1.311 has no scheduled-tasks folder; it stays private.
	absent(t, filepath.Join(home, "scheduled-tasks"))
}

func TestClaudePrepareReportsAccountOwnHistoryAndKeepsIt(t *testing.T) {
	c, device, home := preparedClaude(t)
	write(t, filepath.Join(device, "projects", "-repo", "dev.jsonl"), "{}\n")
	write(t, filepath.Join(device, "tasks", "t.json"), "{}")
	write(t, filepath.Join(home, "projects", "-repo", "acct.jsonl"), "own\n")
	write(t, filepath.Join(home, "tasks", "t2.json"), "own")

	err := c.Prepare(home)
	var split *ports.SplitHistoryError
	if !errors.Is(err, ports.ErrAgentAccountSplitHistory) || !errors.As(err, &split) {
		t.Fatalf("Prepare = %v, want a split-history error", err)
	}
	if !slices.Equal(split.Dirs, []string{"projects", "tasks"}) || split.Home != home {
		t.Fatalf("split = %+v", split)
	}
	for _, want := range []string{home, "projects and tasks", "move the contents", "Removing the account and adding it again"} {
		if !strings.Contains(split.Error(), want) {
			t.Errorf("message lacks %q: %s", want, split.Error())
		}
	}
	if errors.Is(err, ports.ErrAgentAccountUnshared) {
		t.Fatal("a split must not read as the Developer Mode failure")
	}
	// Nothing is moved or deleted for the user.
	if data, rerr := os.ReadFile(filepath.Join(home, "projects", "-repo", "acct.jsonl")); rerr != nil || string(data) != "own\n" {
		t.Fatalf("account history touched: %q %v", data, rerr)
	}
	if got := c.HistorySplit(home); !slices.Equal(got, []string{"projects", "tasks"}) {
		t.Fatalf("HistorySplit = %v", got)
	}
}

func TestClaudePrepareDoesNotFlagWhatLinkingResolves(t *testing.T) {
	c, device, home := preparedClaude(t)
	write(t, filepath.Join(device, "projects", "-repo", "dev.jsonl"), "{}\n")
	// Only the account has plans: linking moves it to the computer's folder.
	write(t, filepath.Join(home, "plans", "p.md"), "plan")
	// An empty folder the CLI made before the link did is replaced.
	if err := os.MkdirAll(filepath.Join(home, "todos"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(device, "todos", "x"), "x")
	if err := c.Prepare(home); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got := c.HistorySplit(home); len(got) != 0 {
		t.Fatalf("HistorySplit = %v, want none", got)
	}
	if _, err := os.Stat(filepath.Join(device, "plans", "p.md")); err != nil {
		t.Fatalf("account-only plans not moved to the computer: %v", err)
	}
}

func TestClaudePrepareLeavesAnExistingLinkUntouched(t *testing.T) {
	c, device, home := preparedClaude(t)
	write(t, filepath.Join(device, "projects", "-repo", "dev.jsonl"), "{}\n")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// A correct link, spelled unusually: rewriting it would normalise the text.
	odd := device + string(os.PathSeparator) + "." + string(os.PathSeparator) + "projects"
	if err := os.Symlink(odd, filepath.Join(home, "projects")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := c.Prepare(home); err != nil {
		t.Fatal(err)
	}
	linksTo(t, filepath.Join(home, "projects"), odd)
	if got := c.HistorySplit(home); len(got) != 0 {
		t.Fatalf("HistorySplit = %v for a linked folder", got)
	}
}

func TestClaudePrepareRemovesDanglingLinkToADeletedEntry(t *testing.T) {
	c, device, home := preparedClaude(t)
	write(t, filepath.Join(device, "debug", "d.log"), "x")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(device, "debug"), filepath.Join(home, "debug")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(device, "debug")); err != nil {
		t.Fatal(err)
	}
	if err := c.Prepare(home); err != nil {
		t.Fatal(err)
	}
	absent(t, filepath.Join(home, "debug"))
}

// HistorySplit is read-only and runs before any linking, as the accounts list does.
func TestHistorySplitIgnoresEmptyFoldersAndWhatOnlyTheAccountHas(t *testing.T) {
	c, device, home := preparedClaude(t)
	write(t, filepath.Join(device, "todos", "x"), "x")
	write(t, filepath.Join(device, "projects", "-repo", "dev.jsonl"), "{}\n")
	if err := os.MkdirAll(filepath.Join(home, "todos"), 0o700); err != nil { // empty: linking replaces it
		t.Fatal(err)
	}
	write(t, filepath.Join(home, "plans", "p.md"), "plan") // the computer has no plans
	if got := c.HistorySplit(home); len(got) != 0 {
		t.Fatalf("HistorySplit = %v, want none", got)
	}
	write(t, filepath.Join(home, "projects", "-repo", "own.jsonl"), "own\n")
	if got := c.HistorySplit(home); !slices.Equal(got, []string{"projects"}) {
		t.Fatalf("HistorySplit = %v, want [projects]", got)
	}
	if _, err := os.Lstat(filepath.Join(home, "todos")); err != nil || absentLink(filepath.Join(home, "todos")) == false {
		t.Fatalf("HistorySplit changed the folder: %v", err)
	}
}

// absentLink reports that path is still a real directory, not a link.
func absentLink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink == 0
}

// A running Claude may write into a folder between the empty check and the
// removal. That file must survive, and the folder is then the account's own copy.
func TestClaudePrepareNeverDeletesWhatIsWrittenAfterTheEmptyCheck(t *testing.T) {
	c, device, home := preparedClaude(t)
	write(t, filepath.Join(device, "memory", "personal", "dev.md"), "dev")
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o700); err != nil { // empty at check time
		t.Fatal(err)
	}
	beforeEmptyDirRemove = func(path string) {
		if filepath.Base(path) == "memory" {
			write(t, filepath.Join(path, "late.md"), "written just now")
		}
	}
	t.Cleanup(func() { beforeEmptyDirRemove = nil })

	err := c.Prepare(home)
	var split *ports.SplitHistoryError
	if !errors.As(err, &split) || !slices.Equal(split.Dirs, []string{"memory"}) {
		t.Fatalf("Prepare = %v, want a split on memory", err)
	}
	if data, rerr := os.ReadFile(filepath.Join(home, "memory", "late.md")); rerr != nil || string(data) != "written just now" {
		t.Fatalf("late write lost: %q %v", data, rerr)
	}
}

func TestClaudePrepareStillReplacesAFolderThatStaysEmpty(t *testing.T) {
	c, device, home := preparedClaude(t)
	write(t, filepath.Join(device, "memory", "personal", "dev.md"), "dev")
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := c.Prepare(home); err != nil {
		t.Fatal(err)
	}
	linksTo(t, filepath.Join(home, "memory"), filepath.Join(device, "memory"))
}
