package agentpolicy

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sync"
	"time"
)

// ManagedSettings is the "settings" dictionary of the managed file: registry
// key -> value (B212). It sits beside the agent-policy "rules" and is read from
// the same file, with the same location, cache age and fail-closed handling.
type ManagedSettings struct {
	Values map[string]json.RawMessage
	// Unreadable is true when the file exists but cannot be read or parsed. The
	// admin's intent is unknown, so every lockable key is then locked read-only.
	Unreadable bool
}

// ManagedSettingsReader reads the managed file at most once per managedTTL.
type ManagedSettingsReader struct {
	path    string
	convert func(string) ([]byte, error)
	now     func() time.Time

	mu     sync.Mutex
	at     time.Time
	cached ManagedSettings
}

// NewManagedSettingsReader reads the platform's managed file.
func NewManagedSettingsReader() *ManagedSettingsReader {
	return &ManagedSettingsReader{path: managedPath(), convert: loadManagedFile, now: time.Now}
}

// NewManagedSettingsReaderFor reads a given file with a given loader (tests).
func NewManagedSettingsReaderFor(path string, convert func(string) ([]byte, error), now func() time.Time) *ManagedSettingsReader {
	return &ManagedSettingsReader{path: path, convert: convert, now: now}
}

// Read returns the managed settings. A missing file has none.
func (r *ManagedSettingsReader) Read() ManagedSettings {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.at.IsZero() && r.now().Sub(r.at) < managedTTL {
		return r.cached
	}
	r.cached, r.at = readManagedSettings(r.path, r.convert), r.now()
	return r.cached
}

func readManagedSettings(path string, convert func(string) ([]byte, error)) ManagedSettings {
	if path == "" {
		return ManagedSettings{}
	}
	if _, err := os.Stat(path); err != nil {
		return ManagedSettings{Unreadable: errors.Is(err, fs.ErrPermission)}
	}
	data, err := convert(path)
	if err != nil {
		return ManagedSettings{Unreadable: !errors.Is(err, fs.ErrNotExist)}
	}
	var doc struct {
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return ManagedSettings{Unreadable: true}
	}
	if len(doc.Settings) == 0 || string(doc.Settings) == "null" {
		return ManagedSettings{}
	}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(doc.Settings, &values); err != nil {
		return ManagedSettings{Unreadable: true}
	}
	return ManagedSettings{Values: values}
}
