package agentpolicy

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readSettingsFile(t *testing.T, content string, convert func(string) ([]byte, error)) ManagedSettings {
	t.Helper()
	path := filepath.Join(t.TempDir(), "managed.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if convert == nil {
		convert = func(p string) ([]byte, error) { return os.ReadFile(p) }
	}
	return NewManagedSettingsReaderFor(path, convert, time.Now).Read()
}

func TestManagedSettingsReader(t *testing.T) {
	if got := readSettingsFile(t, `{"settings":{"app.providerEventRetentionDays":7}}`, nil); got.Unreadable || string(got.Values["app.providerEventRetentionDays"]) != "7" {
		t.Errorf("valid file: %+v", got)
	}
	if got := readSettingsFile(t, `{"rules":{}}`, nil); got.Unreadable || len(got.Values) != 0 {
		t.Errorf("file without settings: %+v", got)
	}
	for name, content := range map[string]string{"malformed": `{not json`, "settings not an object": `{"settings":[1]}`} {
		if got := readSettingsFile(t, content, nil); !got.Unreadable {
			t.Errorf("%s must fail closed: %+v", name, got)
		}
	}
	if got := readSettingsFile(t, `{}`, func(string) ([]byte, error) { return nil, errors.New("plutil failed") }); !got.Unreadable {
		t.Error("a converter failure must fail closed")
	}
	if got := readSettingsFile(t, `{}`, func(string) ([]byte, error) { return nil, fs.ErrNotExist }); got.Unreadable {
		t.Error("a vanished file is no file")
	}
	if got := NewManagedSettingsReaderFor(filepath.Join(t.TempDir(), "absent"), nil, time.Now).Read(); got.Unreadable || got.Values != nil {
		t.Errorf("missing file: %+v", got)
	}
}

func TestManagedSettingsReaderCaches(t *testing.T) {
	now := time.Now()
	reads := 0
	path := filepath.Join(t.TempDir(), "m.json")
	_ = os.WriteFile(path, []byte(`{}`), 0o600)
	r := NewManagedSettingsReaderFor(path, func(string) ([]byte, error) { reads++; return []byte(`{}`), nil }, func() time.Time { return now })
	r.Read()
	r.Read()
	now = now.Add(managedTTL + time.Second)
	r.Read()
	if reads != 2 {
		t.Errorf("reads = %d, want 2", reads)
	}
}
