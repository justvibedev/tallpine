package settingsaudit

import (
	"strings"
	"testing"

	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// The app settings flattener covers exactly the registry's app keys (cleanup
// keys come from the cleanup policy flattener).
func TestAppSettingsValuesMatchRegistry(t *testing.T) {
	values := AppSettings(settingssvc.Snapshot{})
	want := 0
	for _, s := range settingsreg.Default().Scope(settingsreg.ScopeApp) {
		if strings.HasPrefix(string(s.Key), "app.cleanup.") {
			continue
		}
		want++
		if _, ok := values[s.Key]; !ok {
			t.Errorf("%s is not flattened", s.Key)
		}
	}
	if len(values) != want {
		t.Errorf("flattener has %d keys, registry %d", len(values), want)
	}
}
