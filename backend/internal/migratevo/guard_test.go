package migratevo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckLegacyInstall(t *testing.T) {
	mk := func(t *testing.T, legacyDB, newHome bool) string {
		home := t.TempDir()
		if legacyDB {
			db := NewRoots(home).SourceDB()
			if err := os.MkdirAll(filepath.Dir(db), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(db, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if newHome {
			db := NewRoots(home).TargetDB()
			if err := os.MkdirAll(filepath.Dir(db), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(db, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return home
	}
	t.Run("legacy DB and no new home refuses", func(t *testing.T) {
		err := CheckLegacyInstall(mk(t, true, false))
		if !errors.Is(err, ErrLegacyInstall) || !strings.Contains(err.Error(), "migrate-from-vo") {
			t.Fatalf("err = %v, want ErrLegacyInstall pointing at migrate-from-vo", err)
		}
	})
	t.Run("app-created bare new home still refuses", func(t *testing.T) {
		home := mk(t, true, false)
		if err := os.MkdirAll(filepath.Join(NewRoots(home).Target, "electron"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := CheckLegacyInstall(home); !errors.Is(err, ErrLegacyInstall) {
			t.Fatalf("err = %v, want ErrLegacyInstall", err)
		}
	})
	for name, c := range map[string][2]bool{"fresh machine": {false, false}, "already migrated": {false, true}, "both DBs": {true, true}} {
		t.Run(name, func(t *testing.T) {
			if err := CheckLegacyInstall(mk(t, c[0], c[1])); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}

func TestCheckLegacyInstallRefusesUnfinishedMigration(t *testing.T) {
	// The real post-rename layout: target DB present, the old root holds only the README.
	home := t.TempDir()
	r := NewRoots(home)
	if err := os.MkdirAll(filepath.Dir(r.TargetDB()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.TargetDB(), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(r.Source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Source, readmeName), []byte("moved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckLegacyInstall(home); err != nil {
		t.Fatalf("no plan file: err = %v, want nil", err)
	}
	p := &Plan{Version: planVersion, Steps: map[string]bool{StepRename: true}}
	if err := r.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	if err := CheckLegacyInstall(home); !errors.Is(err, ErrLegacyInstall) {
		t.Fatalf("unfinished plan: err = %v, want ErrLegacyInstall", err)
	}
	p.Steps[StepDB], p.Steps[StepWorktrees] = true, true
	if err := r.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	if err := CheckLegacyInstall(home); err != nil {
		t.Fatalf("finished plan: err = %v, want nil", err)
	}
}
