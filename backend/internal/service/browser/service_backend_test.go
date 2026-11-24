package browser

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeProjects struct {
	rec domain.ProjectRecord
	ok  bool
	err error
	ids []string
}

func (f *fakeProjects) GetProjectWithoutEnv(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	f.ids = append(f.ids, id)
	return f.rec, f.ok, f.err
}

// The daemon stamps the session's project browserBackend on every command
// (B849 a15), read per command so a toggle applies to the next one (a11).
func TestServiceStampsBackendFromProjectConfig(t *testing.T) {
	chrome, electron, empty := domain.BrowserBackendChrome, domain.BrowserBackendElectron, ""
	for _, tc := range []struct {
		name    string
		project domain.ProjectID
		cfg     *string
		ok      bool
		want    string
	}{
		{"chrome", "p1", &chrome, true, "chrome"},
		{"electron", "p1", &electron, true, "electron"},
		{"empty", "p1", &empty, true, "electron"},
		{"unset", "p1", nil, true, "electron"},
		{"missing project", "p1", &chrome, false, "electron"},
		{"standalone session", "", &chrome, true, "electron"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authority := NewAuthority()
			token, verifier, err := authority.Issue("s1")
			if err != nil {
				t.Fatal(err)
			}
			projects := &fakeProjects{rec: domain.ProjectRecord{Config: domain.ProjectConfig{BrowserBackend: tc.cfg}}, ok: tc.ok}
			runtime := &fakeRuntime{}
			service := New(fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
				ID: "s1", ProjectID: tc.project,
				Metadata: domain.SessionMetadata{BrowserCapabilityVerifier: verifier},
			}}}, runtime, authority, projects)
			if _, _, err := service.Execute(context.Background(), "s1", token, "snapshot", nil); err != nil {
				t.Fatal(err)
			}
			if runtime.backend != tc.want {
				t.Fatalf("backend = %q, want %q", runtime.backend, tc.want)
			}
			if tc.project != "" && (len(projects.ids) != 1 || projects.ids[0] != string(tc.project)) {
				t.Fatalf("project reads = %v, want [%s]", projects.ids, tc.project)
			}
			if _, b, err := service.Status(context.Background(), "s1", token); err != nil || b != tc.want {
				t.Fatalf("status backend = %q err=%v, want %q", b, err, tc.want)
			}
		})
	}
}

// A failed project read fails the command instead of silently driving the
// in-app browser of a Chrome-mode project.
func TestServiceFailsWhenProjectConfigUnreadable(t *testing.T) {
	authority := NewAuthority()
	token, verifier, _ := authority.Issue("s1")
	runtime := &fakeRuntime{}
	service := New(fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
		ID: "s1", ProjectID: "p1", Metadata: domain.SessionMetadata{BrowserCapabilityVerifier: verifier},
	}}}, runtime, authority, &fakeProjects{err: errors.New("db down")})
	if _, _, err := service.Execute(context.Background(), "s1", token, "snapshot", nil); err == nil || runtime.action != "" {
		t.Fatalf("err = %v, dispatched %q; want an error and no dispatch", err, runtime.action)
	}
}
