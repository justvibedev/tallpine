package settingswrite

import (
	"context"
	"encoding/json"
	"slices"

	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

// retiredSettings are keys that no longer exist, with where their value lives
// now. A write names the new home instead of failing as an unknown key.
var retiredSettings = map[settingsreg.Key]string{
	"app.reviewDefaults": "retired; review settings are per project: set project.autoReview, project.terminateOnPrMerge and project.reviewRisk",
}

// refuseKeys refuses a change naming a key no caller may write (a retired
// one) or one only the desktop app may write, before any other check.
func (w *Writer) refuseKeys(ctx context.Context, ch Change) error {
	retired, desktopOnly := map[string]string{}, map[string]string{}
	for _, k := range append(slices.Clone(ch.Unset), mapKeys(ch.Set)...) {
		if why, ok := retiredSettings[k]; ok {
			retired[string(k)] = why
		}
		if k == PeerCheckKey {
			if why := w.peerCheckRefusal(ctx); why != "" {
				desktopOnly[string(k)] = why
			}
		}
	}
	if len(retired) > 0 {
		return &Error{Code: CodeRetired, Message: "retired settings: " + joinKeys(retired), Keys: retired}
	}
	if len(desktopOnly) > 0 {
		return &Error{Code: CodeDesktopOnly, Message: "only the desktop app may change " + joinKeys(desktopOnly), Keys: desktopOnly}
	}
	return nil
}

func mapKeys(m map[settingsreg.Key]json.RawMessage) []settingsreg.Key {
	out := make([]settingsreg.Key, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
