package settingswrite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// app.reviewDefaults is retired (B411): a write names the project keys that
// replaced it, whether it sets or unsets the key, and refuses the whole change.
func TestApplyRefusesRetiredReviewDefaultsWithPointer(t *testing.T) {
	w := &Writer{} // refused before any store is read
	for name, ch := range map[string]Change{
		"set":   {Set: map[settingsreg.Key]json.RawMessage{"app.reviewDefaults": json.RawMessage(`{"autoReview":true}`)}},
		"unset": {Unset: []settingsreg.Key{"app.reviewDefaults"}},
		"mixed": {Set: map[settingsreg.Key]json.RawMessage{"app.versionRefresh": json.RawMessage(`false`), "app.reviewDefaults": json.RawMessage(`{}`)}},
	} {
		var refused *Error
		if err := w.Apply(context.Background(), ch); !errors.As(err, &refused) || refused.Code != CodeRetired {
			t.Fatalf("%s: err = %v, want %s", name, err, CodeRetired)
		}
		if !strings.Contains(refused.Keys["app.reviewDefaults"], "project.reviewRisk") {
			t.Errorf("%s: no pointer to the project keys: %v", name, refused.Keys)
		}
	}
}
