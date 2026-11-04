package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// SetProjectPermissions must not rewrite a locked role override, and what it
// returns is the effective (managed) config.
func TestSetProjectPermissionsKeepsLockedRoleOverrides(t *testing.T) {
	ctx := context.Background()
	s, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	own := domain.ProjectConfig{}
	own.Worker.AgentConfig.Permissions = domain.PermissionModeAcceptEdits
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: t.TempDir(), DisplayName: "p", RegisteredAt: time.Now(), Config: own}); err != nil {
		t.Fatal(err)
	}
	managed := true
	s.SetManaged(managedcfg.New(func() map[string]json.RawMessage {
		if !managed {
			return nil
		}
		return map[string]json.RawMessage{"project.worker": json.RawMessage(`{"agentConfig":{"permissions":"auto"}}`)}
	}))
	row, ok, err := s.SetProjectPermissions(ctx, "p", domain.PermissionModeBypassPermissions)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if row.Config.Worker.AgentConfig.Permissions != domain.PermissionModeAuto {
		t.Errorf("returned worker permissions = %q, want the managed auto", row.Config.Worker.AgentConfig.Permissions)
	}
	managed = false
	got, _, _ := s.GetProject(ctx, "p")
	if got.Config.Worker.AgentConfig.Permissions != domain.PermissionModeAcceptEdits || got.Config.AgentConfig.Permissions != domain.PermissionModeBypassPermissions {
		t.Fatalf("after the profile is removed: %+v, want the user's worker override kept and the project mode set", got.Config)
	}
}

// Workspace project writes must keep the user's value for a locked field even
// when the caller passes the overlaid (managed) config back.
func TestWorkspaceProjectWritesKeepLockedFields(t *testing.T) {
	ctx := context.Background()
	for name, write := range map[string]func(*sqlite.Store, domain.ProjectRecord) error{
		"upsert": func(s *sqlite.Store, r domain.ProjectRecord) error { return s.UpsertWorkspaceProject(ctx, r, nil) },
		"import": func(s *sqlite.Store, r domain.ProjectRecord) error { return s.ImportWorkspaceProject(ctx, r, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			s, err := sqlitetest.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			rec := domain.ProjectRecord{ID: "w", Path: t.TempDir(), DisplayName: "w", RegisteredAt: time.Now()}
			rec.Config.Worker.AgentConfig.Permissions = domain.PermissionModeAcceptEdits
			if err := write(s, rec); err != nil {
				t.Fatal(err)
			}
			managed := true
			s.SetManaged(managedcfg.New(func() map[string]json.RawMessage {
				if !managed {
					return nil
				}
				return map[string]json.RawMessage{"project.worker": json.RawMessage(`{"agentConfig":{"permissions":"auto"}}`)}
			}))
			got, _, _ := s.GetProject(ctx, "w")
			if got.Config.Worker.AgentConfig.Permissions != domain.PermissionModeAuto {
				t.Fatalf("overlay not applied: %+v", got.Config)
			}
			rec.Config = got.Config // the overlaid config echoed back
			if err := write(s, rec); err != nil {
				t.Fatal(err)
			}
			managed = false
			final, _, _ := s.GetProject(ctx, "w")
			if final.Config.Worker.AgentConfig.Permissions != domain.PermissionModeAcceptEdits {
				t.Fatalf("managed value persisted as the user's: %+v", final.Config)
			}
		})
	}
}

// A read error other than "no row" must fail the lookup, not fall back to the
// (possibly overlaid) incoming config.
func TestKeepLockedReturnsReadErrors(t *testing.T) {
	s, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.SetManaged(managedcfg.New(func() map[string]json.RawMessage {
		return map[string]json.RawMessage{"project.worker": json.RawMessage(`{"agentConfig":{"permissions":"auto"}}`)}
	}))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.KeepLockedForTest(canceled, "p", domain.ProjectConfig{}); err == nil {
		t.Fatal("read error swallowed")
	}
	if _, err := s.KeepLockedForTest(context.Background(), "missing", domain.ProjectConfig{}); err != nil {
		t.Fatalf("missing row must not error: %v", err)
	}
}
