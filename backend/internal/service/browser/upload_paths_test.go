package browser

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/browserruntime"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

type argsRuntime struct {
	fakeRuntime
	args map[string]interface{}
}

func (f *argsRuntime) ExecuteOn(ctx context.Context, id domain.SessionID, backend, action string, args map[string]interface{}) (browserruntime.Result, error) {
	f.args = args
	return f.fakeRuntime.ExecuteOn(ctx, id, backend, action, args)
}

type uploadFixture struct {
	service  *Service
	runtime  *argsRuntime
	token    string
	worktree string
	temp     string
	outside  string
}

func newUploadFixture(t *testing.T) uploadFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "data")
	worktree := filepath.Join(root, "worktree")
	temp := sessionscratch.TempDir(dataDir, "s1")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{worktree, temp, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	authority := NewAuthority()
	token, verifier, err := authority.Issue("s1")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &argsRuntime{}
	service := New(fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
		ID:       "s1",
		Metadata: domain.SessionMetadata{BrowserCapabilityVerifier: verifier, WorkspacePath: worktree},
	}}}, runtime, authority, nil).WithDataDir(dataDir)
	return uploadFixture{service: service, runtime: runtime, token: token, worktree: worktree, temp: temp, outside: outside}
}

func writeFile(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (f uploadFixture) upload(paths ...any) error {
	_, _, err := f.service.Execute(context.Background(), "s1", f.token, "upload", map[string]interface{}{"ref": "e1", "paths": paths})
	return err
}

func TestUploadAcceptsWorktreeAndTempFilesAndSendsResolvedPaths(t *testing.T) {
	f := newUploadFixture(t)
	inWorktree := writeFile(t, filepath.Join(f.worktree, "a.txt"))
	inTemp := writeFile(t, filepath.Join(f.temp, "b.png"))
	// A path spelled through ".." that still lands in the worktree is fine, and
	// is sent resolved.
	dotted := filepath.Join(f.worktree, "sub", "..", "a.txt")
	if err := f.upload(inWorktree, inTemp, dotted); err != nil {
		t.Fatalf("upload: %v", err)
	}
	got, _ := f.runtime.args["paths"].([]string)
	want := []string{inWorktree, inTemp, inWorktree}
	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paths = %v, want %v", got, want)
		}
	}
	if f.runtime.args["ref"] != "e1" {
		t.Fatalf("ref not forwarded: %v", f.runtime.args)
	}
}

func TestUploadRefusesPathsOutsideTheSession(t *testing.T) {
	f := newUploadFixture(t)
	secret := writeFile(t, filepath.Join(f.outside, "id_rsa"))
	link := filepath.Join(f.worktree, "innocent.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]any{
		"outside file":          {secret},
		"symlink out":           {link},
		"dot-dot out":           {filepath.Join(f.worktree, "..", "outside", "id_rsa")},
		"directory":             {f.worktree},
		"missing":               {filepath.Join(f.worktree, "missing.txt")},
		"one bad among good":    {writeFile(t, filepath.Join(f.worktree, "ok.txt")), secret},
		"sibling prefix of tmp": {writeFile(t, filepath.Join(f.temp+"-other.txt"))},
	}
	for name, paths := range cases {
		t.Run(name, func(t *testing.T) {
			f.runtime.action = ""
			if err := f.upload(paths...); apiErrorCode(err) != "BROWSER_UPLOAD_PATH_DENIED" {
				t.Fatalf("err = %v", err)
			}
			if f.runtime.action != "" {
				t.Fatalf("a refused upload reached the desktop")
			}
		})
	}
}

func TestUploadRejectsMalformedPaths(t *testing.T) {
	f := newUploadFixture(t)
	tooMany := make([]any, maxUploadFiles+1)
	for i := range tooMany {
		tooMany[i] = writeFile(t, filepath.Join(f.worktree, "f"+string(rune('a'+i%26))+".txt"))
	}
	for name, paths := range map[string][]any{
		"relative": {"a.txt"},
		"empty":    {},
		"non-text": {42},
		"too many": tooMany,
	} {
		t.Run(name, func(t *testing.T) {
			if err := f.upload(paths...); apiErrorCode(err) != "INVALID_ARGUMENT" {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestPageActionsAreSupported(t *testing.T) {
	f := newUploadFixture(t)
	for _, action := range []string{"back", "forward", "reload", "eval"} {
		if _, got, err := f.service.Execute(context.Background(), "s1", f.token, action, map[string]interface{}{}); err != nil || f.runtime.action != action {
			t.Fatalf("%s: action=%q runtime=%q err=%v", action, got, f.runtime.action, err)
		}
	}
}
