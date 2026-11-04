package project_test

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type recordingAccounts struct{ gotOverride string }

func (r *recordingAccounts) LoginForProject(_ context.Context, _ string, override string) string {
	r.gotOverride = override
	if override != "" {
		return override
	}
	return "picked-account"
}

func TestProjectReadModelCarriesGitHubAccount(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	accounts := &recordingAccounts{}
	m := project.NewWithDeps(project.Deps{Store: store, GitHubAccounts: accounts})
	id := "gha"
	if _, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: &id}); err != nil {
		t.Fatal(err)
	}
	got, err := m.Get(ctx, "gha")
	if err != nil || got.Project == nil {
		t.Fatalf("Get: %v %#v", err, got)
	}
	if got.Project.GitHubAccount != "picked-account" {
		t.Fatalf("githubAccount = %q, want the resolved login", got.Project.GitHubAccount)
	}
	// The project's githubUser override reaches the resolver.
	if _, err := m.UpdateSettings(ctx, "gha", project.UpdateSettingsInput{DisplayName: "gha", Config: domain.ProjectConfig{GitHubUser: "forced"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = m.Get(ctx, "gha")
	if accounts.gotOverride != "forced" || got.Project.GitHubAccount != "forced" {
		t.Fatalf("override = %q, account = %q", accounts.gotOverride, got.Project.GitHubAccount)
	}
}

type countingAccounts struct{ calls int }

func (c *countingAccounts) LoginForProject(context.Context, string, string) string {
	c.calls++
	return "x"
}

// Listing projects is summaries only: it must never run gh or the GitHub API.
func TestListDoesNotResolveGitHubAccounts(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	accounts := &countingAccounts{}
	m := project.NewWithDeps(project.Deps{Store: store, GitHubAccounts: accounts})
	id := "lst"
	if _, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: &id}); err != nil {
		t.Fatal(err)
	}
	accounts.calls = 0
	if _, err := m.List(ctx); err != nil {
		t.Fatal(err)
	}
	if accounts.calls != 0 {
		t.Fatalf("List resolved %d GitHub accounts, want 0", accounts.calls)
	}
}
