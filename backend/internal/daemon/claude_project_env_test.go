package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeProjectLister []domain.ProjectRecord

func (f fakeProjectLister) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	return f, nil
}

func projectWithClaudeDir(id, dir string) domain.ProjectRecord {
	return domain.ProjectRecord{ID: id, Config: domain.ProjectConfig{Env: map[string]string{claudeConfigDirKey: dir}}}
}

func signedInClaudeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	record := `{"oauthAccount":{"accountUuid":"abc"}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// B439: the device-wide checks see a project-level Claude sign-in only when the
// computer's own login holds none.
func TestClaudeEnvWithProjectLogin(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no device login
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", claudeConfigDirKey} {
		t.Setenv(name, "")
	}
	signedIn := signedInClaudeDir(t)
	signedOut := t.TempDir()
	projects := fakeProjectLister{projectWithClaudeDir("a", signedOut), projectWithClaudeDir("b", signedIn)}

	if got := claudeEnvWithProjectLogin(t.Context(), projects, nil)[claudeConfigDirKey]; got != signedIn {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the signed-in project folder %q", got, signedIn)
	}
	if got := claudeEnvWithProjectLogin(t.Context(), fakeProjectLister{projectWithClaudeDir("a", signedOut)}, nil); got[claudeConfigDirKey] != "" {
		t.Fatalf("env = %v, want the base env when no project folder is signed in", got)
	}
	account := map[string]string{claudeConfigDirKey: "/account"}
	if got := claudeEnvWithProjectLogin(t.Context(), projects, account); got[claudeConfigDirKey] != "/account" {
		t.Fatalf("an account folder was replaced: %v", got)
	}

	cleared := map[string]string{claudeConfigDirKey: ""}
	if got := claudeEnvWithProjectLogin(t.Context(), projects, cleared); got[claudeConfigDirKey] != "" {
		t.Fatalf("an explicit device-account clear was replaced: %v", got)
	}

	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"dev"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeEnvWithProjectLogin(t.Context(), projects, nil); got[claudeConfigDirKey] != "" {
		t.Fatalf("env = %v, want the computer's own login to win when signed in", got)
	}
}
