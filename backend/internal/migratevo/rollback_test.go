package migratevo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollbackRestoresTreeAndWorktrees(t *testing.T) {
	f := newFixture(t)
	e := f.env
	ctx := context.Background()
	if err := e.Migrate(ctx, Options{CopyHarnessHistory: false}); err != nil {
		t.Fatalf("migrate: %v\n%s", err, f.out)
	}
	if err := e.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v\n%s", err, f.out)
	}
	if _, err := os.Lstat(e.Target); err == nil {
		t.Error("new home still exists after rollback")
	}
	if _, err := os.Lstat(filepath.Join(e.Source, readmeName)); err == nil {
		t.Error("README stub left in the restored home")
	}
	if e.FindPlanFile() != "" {
		t.Error("plan file must be set aside")
	}
	if _, err := os.Lstat(e.earlyPlanFile() + ".rolledback"); err != nil {
		t.Errorf("rolled-back plan missing: %v", err)
	}
	for _, wt := range f.wts {
		if _, err := e.Run(ctx, wt, "git", "status"); err != nil {
			t.Errorf("git status in %s after rollback: %v", wt, err)
		}
		if out, _ := e.Run(ctx, f.repo, "git", "worktree", "list", "--porcelain"); !strings.Contains(string(out), wt) {
			t.Errorf("repo does not list %s after rollback:\n%s", wt, out)
		}
	}
}

func TestRollbackRefusesWhenOldRootHoldsData(t *testing.T) {
	f := newFixture(t)
	e := f.env
	ctx := context.Background()
	if err := e.Migrate(ctx, Options{}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(e.Source, "precious"), "x")
	if err := e.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "precious") {
		t.Fatalf("Rollback = %v, want refusal naming the stray entry", err)
	}
	if _, err := os.Lstat(e.Target); err != nil {
		t.Error("refused rollback must leave the new home in place")
	}
}

func TestBootoutLegacyLaunchd(t *testing.T) {
	f := newFixture(t)
	agents := t.TempDir()
	for _, n := range []string{"ee.justdev.vo.mobile-wake.plist", "ee.justdev.vo.daemon.plist", "com.other.plist", "ee.justdev.tallpine.mobile-wake.plist"} {
		writeFile(t, filepath.Join(agents, n), "<plist/>")
	}
	var calls []string
	f.env.Run = func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	if err := f.env.BootoutLegacyLaunchd(context.Background(), agents, 501); err != nil {
		t.Fatal(err)
	}
	want := []string{"launchctl bootout gui/501/ee.justdev.vo.daemon", "launchctl bootout gui/501/ee.justdev.vo.mobile-wake"}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
	left, _ := filepath.Glob(filepath.Join(agents, "*.plist"))
	if len(left) != 2 {
		t.Errorf("remaining plists = %v, want the unrelated and the new-prefix ones", left)
	}
}
