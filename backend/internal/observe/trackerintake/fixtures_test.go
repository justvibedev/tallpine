package trackerintake

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// intakeProject is a project with tracker intake enabled for assignee alice.
func intakeProject(id string) domain.ProjectRecord {
	return domain.ProjectRecord{
		ID:            id,
		RepoOriginURL: "https://github.com/acme/" + id + ".git",
		Config:        domain.ProjectConfig{TrackerIntake: domain.TrackerIntakeConfig{Enabled: true, Assignee: "alice"}},
	}
}

// demoStore holds the single intake-enabled "demo" project.
func demoStore() *fakeStore {
	return &fakeStore{projects: []domain.ProjectRecord{intakeProject("demo")}}
}

// ghIssue is an acme GitHub issue; native is e.g. "acme/demo#2".
func ghIssue(native, title string, state domain.NormalizedIssueState, assignees ...string) domain.Issue {
	return domain.Issue{ID: domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: native}, Title: title, State: state, Assignees: assignees}
}

// pollDefault runs one Poll with the default config and fails the test on error.
func pollDefault(t *testing.T, tracker ports.Tracker, store *fakeStore, spawner *fakeSpawner) {
	t.Helper()
	if err := New(singleResolver(tracker), store, spawner, Config{Logger: discardLogger()}).Poll(context.Background()); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
}
