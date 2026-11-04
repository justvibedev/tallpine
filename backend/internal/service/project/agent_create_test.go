package project_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func agentCreateFixture(t *testing.T, protected string) (*project.Service, *sqlite.Store) {
	t.Helper()
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return project.NewWithDeps(project.Deps{Store: store, ProtectedDirs: []string{protected}}), store
}

func creatorOf(t *testing.T, s *sqlite.Store, id string) (domain.ProjectID, bool) {
	t.Helper()
	got, ok, err := s.ProjectCreator(context.Background(), domain.ProjectID(id))
	if err != nil {
		t.Fatal(err)
	}
	return got, ok
}

// An agent create records the creator; removing the project drops it, so a
// person who later registers the same id never inherits the old creator.
func TestAgentCreateRecordsCreatorAndRemoveClearsIt(t *testing.T) {
	ctx := context.Background()
	m, store := agentCreateFixture(t, t.TempDir())
	repo := gitRepo(t)
	if _, err := m.Add(ctx, project.AddInput{Path: repo, ProjectID: ptr("lab"), CreatedBy: "app"}); err != nil {
		t.Fatal(err)
	}
	if got, ok := creatorOf(t, store, "lab"); !ok || got != "app" {
		t.Fatalf("creator = %q ok=%v, want app", got, ok)
	}
	if _, err := m.Remove(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	if _, ok := creatorOf(t, store, "lab"); ok {
		t.Fatal("remove left the creator row behind")
	}
	if _, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: ptr("lab")}); err != nil {
		t.Fatalf("person re-registers the id: %v", err)
	}
	if _, ok := creatorOf(t, store, "lab"); ok {
		t.Fatal("a person-registered project must have no creator")
	}
}

// An agent never adopts or revives an id that exists in any state.
func TestAgentCreateRefusesExistingIDs(t *testing.T) {
	ctx := context.Background()
	m, store := agentCreateFixture(t, t.TempDir())
	if _, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: ptr("mine")}); err != nil {
		t.Fatal(err)
	}
	_, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: ptr("mine"), CreatedBy: "app"})
	wantCode(t, err, "ID_ALREADY_REGISTERED")

	if _, err := m.Remove(ctx, "mine"); err != nil {
		t.Fatal(err)
	}
	_, err = m.Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: ptr("mine"), CreatedBy: "app"})
	wantCode(t, err, "ID_ALREADY_REGISTERED")
	if _, ok := creatorOf(t, store, "mine"); ok {
		t.Fatal("a refused claim must not record a creator")
	}

	// A derived id that is archived is skipped, never revived.
	dir := filepath.Join(t.TempDir(), "mine")
	if out, err := exec.Command("git", "init", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	commitEmpty(t, dir)
	p, err := m.Add(ctx, project.AddInput{Path: dir, CreatedBy: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "mine" {
		t.Fatal("an archived id was revived by an agent")
	}
}

// Agent creates may not use the daemon data dir or any registered path.
func TestAgentCreateRefusesProtectedAndRegisteredPaths(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	m, _ := agentCreateFixture(t, data)

	inData := filepath.Join(data, "worktrees", "wt")
	if err := os.MkdirAll(inData, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, inData)
	_, err := m.Add(ctx, project.AddInput{Path: inData, CreatedBy: "app"})
	wantCode(t, err, "PATH_NOT_ALLOWED")

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(inData, link); err != nil {
		t.Fatal(err)
	}
	_, err = m.Add(ctx, project.AddInput{Path: link, CreatedBy: "app"})
	wantCode(t, err, "PATH_NOT_ALLOWED")

	base := gitRepo(t)
	if _, err := m.Add(ctx, project.AddInput{Path: base, ProjectID: ptr("base")}); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(base, "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, nested)
	_, err = m.Add(ctx, project.AddInput{Path: nested, CreatedBy: "app"})
	wantCode(t, err, "PATH_NOT_ALLOWED")
	_, err = m.Add(ctx, project.AddInput{Path: base, CreatedBy: "app"})
	wantCode(t, err, "PATH_NOT_ALLOWED")

	// An archived project's path stays reserved.
	if _, err := m.Remove(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	_, err = m.Add(ctx, project.AddInput{Path: base, CreatedBy: "app"})
	wantCode(t, err, "PATH_NOT_ALLOWED")

	// A parent folder of a registered project is refused, symlinked or not.
	parent := gitRepo(t)
	kid := filepath.Join(parent, "kid")
	if err := os.MkdirAll(kid, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, kid)
	if _, err := m.Add(ctx, project.AddInput{Path: kid, ProjectID: ptr("kid")}); err != nil {
		t.Fatal(err)
	}
	_, err = m.Add(ctx, project.AddInput{Path: parent, CreatedBy: "app"})
	wantCode(t, err, "PATH_NOT_ALLOWED")
	parentLink := filepath.Join(t.TempDir(), "plink")
	if err := os.Symlink(parent, parentLink); err != nil {
		t.Fatal(err)
	}
	_, err = m.Add(ctx, project.AddInput{Path: parentLink, CreatedBy: "app"})
	wantCode(t, err, "PATH_NOT_ALLOWED")

	// A fresh repo elsewhere (also under the same temp root) is fine.
	if _, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), CreatedBy: "app"}); err != nil {
		t.Fatalf("a free path must be allowed: %v", err)
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	commitEmpty(t, dir)
}
