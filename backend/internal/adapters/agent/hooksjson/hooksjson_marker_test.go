package hooksjson

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func markerManager(path string) Manager {
	return Manager{
		Label:         "test",
		CommandPrefix: "tallpine hooks test ",
		Timeout:       30,
		Path:          func(string) string { return path },
		Managed:       []HookSpec{{Event: "Notification", Command: "tallpine hooks test notification"}},
	}
}

const legacyEntryFile = `{"hooks":{"Notification":[{"hooks":[` +
	`{"type":"command","command":"vo hooks test notification","timeout":30},` +
	`{"type":"command","command":"user hook"}]}]}}`

func TestManagerRecognisesHooksWrittenUnderTheOtherCLIName(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "hooks.json")
	if err := os.WriteFile(path, []byte(legacyEntryFile), 0o600); err != nil {
		t.Fatal(err)
	}
	m := markerManager(path)

	if ok, err := m.AreInstalled(t.Context(), workspace); err != nil || !ok {
		t.Fatalf("AreInstalled = %v, %v; a legacy vo hook must count as installed", ok, err)
	}

	// Re-install replaces the entry instead of duplicating it.
	if err := m.Install(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if strings.Contains(got, "vo hooks") || strings.Count(got, "tallpine hooks test notification") != 1 || !strings.Contains(got, "user hook") {
		t.Fatalf("install did not replace the legacy entry cleanly:\n%s", got)
	}

	// Uninstall removes an entry written under the other name too.
	if err := os.WriteFile(path, []byte(legacyEntryFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "vo hooks") || !strings.Contains(string(data), "user hook") {
		t.Fatalf("uninstall left the legacy entry or removed the user hook:\n%s", data)
	}
}
