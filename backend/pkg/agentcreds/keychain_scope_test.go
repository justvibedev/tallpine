package agentcreds

import (
	"context"
	"testing"
)

// serviceNames runs the keychain rung under a fake runner and returns the
// `-s` service of every `security` call it made.
func serviceNames(t *testing.T, commandEnv map[string]string, processEnv map[string]string) []string {
	t.Helper()
	var names []string
	_, _ = ResolveLocal(context.Background(), ProviderFirstParty, ResolveOptions{
		Env: envFrom(processEnv), ConfigDir: t.TempDir(), GOOS: "darwin", AllowKeychain: true,
		CommandEnv: commandEnv,
		Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			for i, arg := range args {
				if arg == "-s" && i+1 < len(args) {
					names = append(names, args[i+1])
				}
			}
			return nil, nil
		},
	})
	return names
}

func equalNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Claude Code 2.1.311 names an account folder's login "Claude Code-credentials-"
// plus the first 8 hex digits of sha256(folder). sha256("/tmp/acct-b") starts
// f10c181c (computed independently with Python's hashlib).
func TestKeychainReadsTheAccountFoldersOwnItems(t *testing.T) {
	got := serviceNames(t, map[string]string{
		"CLAUDE_CONFIG_DIR": "/tmp/acct-b", "CLAUDE_SECURESTORAGE_CONFIG_DIR": "/tmp/acct-b",
	}, nil)
	want := []string{"Claude Code-credentials-f10c181c", "Claude Code-f10c181c"}
	if !equalNames(got, want) {
		t.Fatalf("services = %v, want %v", got, want)
	}
}

func TestKeychainNeverReadsTheDeviceItemWhenAFolderIsSet(t *testing.T) {
	for _, env := range []map[string]string{
		{"CLAUDE_CONFIG_DIR": "/tmp/acct-b"},
		{"CLAUDE_CONFIG_DIR": "/tmp/acct-b", "CLAUDE_SECURESTORAGE_CONFIG_DIR": "/tmp/acct-b"},
	} {
		for _, name := range serviceNames(t, env, nil) {
			if name == "Claude Code-credentials" || name == "Claude Code" {
				t.Fatalf("device item %q requested for env %v", name, env)
			}
		}
	}
}

// CLAUDE_SECURESTORAGE_CONFIG_DIR wins over CLAUDE_CONFIG_DIR, as in Claude.
func TestKeychainSecureStorageDirOverridesConfigDir(t *testing.T) {
	got := serviceNames(t, map[string]string{
		"CLAUDE_CONFIG_DIR": "/somewhere/else", "CLAUDE_SECURESTORAGE_CONFIG_DIR": "/tmp/acct-b",
	}, nil)
	if len(got) == 0 || got[0] != "Claude Code-credentials-f10c181c" {
		t.Fatalf("services = %v, want the secure-storage folder's hash", got)
	}
}

func TestKeychainDeviceItemsWhenLaunchEnvNamesNoFolder(t *testing.T) {
	want := []string{"Claude Code-credentials", "Claude Code"}
	if got := serviceNames(t, nil, nil); !equalNames(got, want) {
		t.Fatalf("services = %v, want %v", got, want)
	}
	// Only the launch env decides: a daemon that runs with the variable set
	// itself keeps the device items here.
	if got := serviceNames(t, nil, map[string]string{"CLAUDE_CONFIG_DIR": "/tmp/acct-b"}); !equalNames(got, want) {
		t.Fatalf("services = %v, want %v", got, want)
	}
	// Claude treats an explicitly empty secure-storage folder as the device.
	if got := serviceNames(t, map[string]string{"CLAUDE_CONFIG_DIR": "/tmp/acct-b", "CLAUDE_SECURESTORAGE_CONFIG_DIR": ""}, nil); !equalNames(got, want) {
		t.Fatalf("services = %v, want %v", got, want)
	}
}
