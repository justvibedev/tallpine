package contextbudget

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// A No on the weekly digest covers the gaps it showed, not later ones: the
// finder records the gap keys it last put on the card, the handler turns them
// into declined gaps on a No, and the finder leaves those out for 30 days.
const (
	shownGapsFile    = "routing-shown.json"
	declinedGapsFile = "routing-declined.json"
)

// routingDir is where a project's routing state lives, beside its findings' artifact directories.
func routingDir(root string, project domain.ProjectID) string {
	return filepath.Join(root, string(project), "context-health")
}

func readJSON(path string, into any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, into) == nil
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// declinedGaps is the gap keys declined less than domain.ContextHealthSuppressFor ago.
func declinedGaps(dir string, now time.Time) map[string]bool {
	var at map[string]time.Time
	out := map[string]bool{}
	if dir == "" || !readJSON(filepath.Join(dir, declinedGapsFile), &at) {
		return out
	}
	for k, t := range at {
		if now.Before(t.Add(domain.ContextHealthSuppressFor)) {
			out[k] = true
		}
	}
	return out
}

// recordDecline is the one place a declined digest writes its gaps down, for a
// No and for a withdrawn card alike. Other findings need nothing.
func recordDecline(f domain.ContextHealthFinding, now time.Time, log *slog.Logger) {
	if !isDigest(f.Fingerprint) || f.ArtifactDir == "" {
		return
	}
	if err := declineShownGaps(f.ArtifactDir, now); err != nil && log != nil {
		log.Warn("context health: declined gaps not recorded", "err", err)
	}
}

// pinShownGaps copies the finder's current gap keys into the card's own
// directory when the card is filed: the owner decides on what the card showed
// then, so a No must decline exactly those, not gaps that appear while it is open.
func pinShownGaps(artifactDir string) {
	var shown []string
	if artifactDir != "" && readJSON(filepath.Join(filepath.Dir(artifactDir), shownGapsFile), &shown) {
		_ = writeJSON(filepath.Join(artifactDir, shownGapsFile), shown)
	}
}

// declineShownGaps records the gaps the card was filed with as declined at now.
func declineShownGaps(artifactDir string, now time.Time) error {
	var shown []string
	if !readJSON(filepath.Join(artifactDir, shownGapsFile), &shown) {
		return nil
	}
	dir := filepath.Dir(artifactDir)
	at := map[string]time.Time{}
	readJSON(filepath.Join(dir, declinedGapsFile), &at)
	for _, k := range shown {
		at[k] = now
	}
	return writeJSON(filepath.Join(dir, declinedGapsFile), at)
}

// withoutDeclined drops the gaps the owner declined and records the rest as
// the gaps found now (pinned to the card when it is filed).
func (r RoutingFinder) withoutDeclined(proj domain.ProjectRecord, gs []gap) []gap {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	dir := ""
	if r.ArtifactRoot != "" {
		dir = routingDir(r.ArtifactRoot, domain.ProjectID(proj.ID))
	}
	declined := declinedGaps(dir, now())
	var kept []gap
	var keys []string
	for _, g := range gs {
		if !declined[g.Fingerprint] {
			kept, keys = append(kept, g), append(keys, g.Fingerprint)
		}
	}
	if dir != "" && len(kept) > 0 {
		_ = writeJSON(filepath.Join(dir, shownGapsFile), keys)
	}
	return kept
}

// clearDrafts removes what an earlier card of the digest left in dir: the
// analyst's draft, the drafted patch and the pinned gaps. Evidence is rewritten
// when the next card is filed.
func clearDrafts(dir string) {
	if dir == "" {
		return
	}
	for _, name := range []string{AnalystNotesFile, AnalystPatchFile, MemoryActionsFile, patchFile, shownGapsFile} {
		_ = os.Remove(filepath.Join(dir, name))
	}
}
