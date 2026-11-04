package cli

// dto_drift_session_test.go extends the DTO-drift guard to the session command
// group. The CLI now decodes the daemon's responses into httpd/apitypes (the
// same types the controllers encode), so each subtest drives a real command
// over the real router and controllers and checks that every moved DTO's wire
// fields arrive intact. Renaming a json tag in apitypes breaks the decoded
// value here instead of silently emptying it.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

var driftSessionTime = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

func driftSession() domain.Session {
	return domain.Session{
		SessionRecord: domain.SessionRecord{
			ID: "mer-7", ProjectID: "mer", IssueID: "ISS-9", Harness: "codex",
			DisplayName: "drift worker", Kind: domain.KindWorker,
			CreatedAt: driftSessionTime, UpdatedAt: driftSessionTime,
			Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: driftSessionTime},
		},
		Status: domain.StatusWorking,
	}
}

type driftSessionService struct {
	controllers.SessionService
	renamed  string
	cleanups []domain.ProjectID
}

func (f *driftSessionService) List(context.Context, sessionsvc.ListFilter) ([]domain.Session, error) {
	return []domain.Session{driftSession()}, nil
}

func (f *driftSessionService) ListPage(context.Context, sessionsvc.ListFilter) (sessionsvc.ListPage, error) {
	return sessionsvc.ListPage{Sessions: []domain.Session{driftSession()}}, nil
}

func (f *driftSessionService) Get(context.Context, domain.SessionID) (domain.Session, error) {
	return driftSession(), nil
}

func (f *driftSessionService) Rename(_ context.Context, _ domain.SessionID, name string) error {
	f.renamed = name
	return nil
}

func (f *driftSessionService) Restore(context.Context, domain.SessionID) (sessionsvc.RestoreOutcome, error) {
	return sessionsvc.RestoreOutcome{Session: driftSession(), Mode: sessionsvc.RestoreModeViewSavedPrompt}, nil
}

func (f *driftSessionService) ExitAgent(context.Context, domain.SessionID) (sessionsvc.ExitAgentOutcome, error) {
	return sessionsvc.ExitAgentOutcome{Session: driftSession()}, nil
}

func (f *driftSessionService) ResumeAgent(context.Context, domain.SessionID) (sessionsvc.ResumeAgentOutcome, error) {
	return sessionsvc.ResumeAgentOutcome{Session: driftSession(), Mode: sessionsvc.RestoreModeViewFresh}, nil
}

func (f *driftSessionService) Cleanup(_ context.Context, p domain.ProjectID) (sessionsvc.CleanupOutcome, error) {
	f.cleanups = append(f.cleanups, p)
	return sessionsvc.CleanupOutcome{
		Cleaned:     []domain.SessionID{"mer-7"},
		AlreadyGone: []domain.SessionID{"mer-8"},
		Skipped:     []sessionsvc.CleanupSkipped{{SessionID: "mer-9", Reason: "dirty worktree"}},
	}, nil
}

func runDriftSession(t *testing.T, svc *driftSessionService, args ...string) []byte {
	t.Helper()
	startDriftTestDaemon(t, svc, &fakeProjectManager{})
	var out bytes.Buffer
	root := NewRootCommand(Deps{Out: &out, Err: &out, HTTPClient: &http.Client{}, ProcessAlive: func(int) bool { return true }})
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("%v: %v\noutput: %s", args, err, out.String())
	}
	return out.Bytes()
}

func TestE2E_SessionDTORoundTrip(t *testing.T) {
	t.Run("get decodes SessionResponse", func(t *testing.T) {
		out := runDriftSession(t, &driftSessionService{}, "session", "get", "mer-7", "--json")
		var res apitypes.SessionResponse
		if err := json.Unmarshal(out, &res); err != nil {
			t.Fatalf("decode: %v\n%s", err, out)
		}
		s := res.Session
		if s.ID != "mer-7" || s.ProjectID != "mer" || s.IssueID != "ISS-9" || s.Harness != "codex" ||
			s.DisplayName != "drift worker" || s.Status != domain.StatusWorking ||
			s.Activity.State != domain.ActivityActive || !s.Activity.LastActivityAt.Equal(driftSessionTime) ||
			s.Kind != domain.KindWorker || !s.CreatedAt.Equal(driftSessionTime) {
			t.Errorf("SessionView lost fields in round trip: %+v", s)
		}
	})
	t.Run("list decodes ListSessionsResponse", func(t *testing.T) {
		out := runDriftSession(t, &driftSessionService{}, "session", "ls", "--project", "mer", "--json")
		if !bytes.Contains(out, []byte(`"id": "mer-7"`)) && !bytes.Contains(out, []byte(`"id":"mer-7"`)) {
			t.Errorf("list output missing session id: %s", out)
		}
		if !bytes.Contains(out, []byte("drift worker")) {
			t.Errorf("list output missing display name (displayName tag drift?): %s", out)
		}
	})
	t.Run("rename sends RenameSessionRequest", func(t *testing.T) {
		svc := &driftSessionService{}
		runDriftSession(t, svc, "session", "rename", "mer-7", "new name")
		if svc.renamed != "new name" {
			t.Errorf("renamed = %q (RenameSessionRequest displayName tag drift?)", svc.renamed)
		}
	})
	t.Run("restore decodes RestoreSessionResponse", func(t *testing.T) {
		out := runDriftSession(t, &driftSessionService{}, "session", "restore", "mer-7")
		for _, want := range []string{"session mer-7 restored", "project: mer"} {
			if !bytes.Contains(out, []byte(want)) {
				t.Errorf("restore output missing %q: %s", want, out)
			}
		}
	})
	t.Run("exit-agent decodes ExitAgentResponse", func(t *testing.T) {
		out := runDriftSession(t, &driftSessionService{}, "session", "exit-agent", "mer-7", "--json")
		var res apitypes.ExitAgentResponse
		if err := json.Unmarshal(out, &res); err != nil || res.SessionID != "mer-7" || res.Session.ID != "mer-7" {
			t.Errorf("exit-agent response drifted: %v %+v", err, res)
		}
	})
	t.Run("resume-agent decodes ResumeAgentResponse", func(t *testing.T) {
		out := runDriftSession(t, &driftSessionService{}, "session", "resume-agent", "mer-7", "--json")
		var res apitypes.ResumeAgentResponse
		if err := json.Unmarshal(out, &res); err != nil || res.SessionID != "mer-7" || res.ResumeMode != domain.RestoreModeViewFresh {
			t.Errorf("resume-agent response drifted: %v %+v", err, res)
		}
	})
	t.Run("cleanup decodes CleanupSessionsResponse", func(t *testing.T) {
		svc := &driftSessionService{}
		out := runDriftSession(t, svc, "session", "cleanup", "--project", "mer", "--yes")
		for _, want := range []string{"mer-9", "dirty worktree"} {
			if !bytes.Contains(out, []byte(want)) {
				t.Errorf("cleanup output missing %q: %s", want, out)
			}
		}
		if len(svc.cleanups) != 1 {
			t.Errorf("Cleanup calls = %d, want 1", len(svc.cleanups))
		}
	})
}
