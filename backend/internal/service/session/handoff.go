package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// HandoffOrchestrator replaces the orchestrator session id with a fresh one for
// the same project, seeded to read the state note at notePath and continue.
// The note must be a file inside the retiring orchestrator's artifact
// directory. Workers are untouched and their reports follow the newest active
// orchestrator. Repeating the call for the same id returns the same successor.
//
// The work runs detached from ctx: the retiring orchestrator is usually the
// caller, and retiring it must not cancel the handoff halfway.
func (s *Service) HandoffOrchestrator(ctx context.Context, id domain.SessionID, notePath string) (domain.Session, error) {
	ctx = context.WithoutCancel(ctx)
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()

	old, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if old.Kind != domain.KindOrchestrator {
		return domain.Session{}, apierr.Invalid("NOT_AN_ORCHESTRATOR", "Only an orchestrator session can be handed off", nil)
	}
	if old.IsTerminated {
		if next := s.handoffSuccessors[id]; next != "" {
			return s.Get(ctx, next)
		}
		return domain.Session{}, apierr.Conflict("ORCHESTRATOR_NOT_ACTIVE", "This orchestrator has already ended", nil)
	}
	project, err := s.requireProject(ctx, old.ProjectID)
	if err != nil {
		return domain.Session{}, err
	}
	// Checked before anything is retired: a refusal must leave the current
	// orchestrator running.
	if project.Config.Orchestrator.Harness == "" {
		return domain.Session{}, apierr.Conflict("ORCHESTRATOR_AGENT_NOT_CONFIGURED",
			"This project has no orchestrator agent configured; set one in the project settings before handing off", nil)
	}
	if strings.TrimSpace(notePath) == "" && !agentRunning(old) && newestNote(sessionartifacts.Dir(s.dataDir, id)) == "" {
		// Nobody can write the note: asking a stopped agent fails, and one whose
		// saved conversation is gone never comes back to read it (B673). Seed
		// the successor from the daemon's bootstrap note instead, as the idle
		// handoff does.
		if notePath, err = s.writeAutoHandoffNote(id); err != nil {
			return domain.Session{}, err
		}
	}
	note, err := s.handoffNote(id, notePath)
	if err != nil {
		return domain.Session{}, err
	}
	// The successor takes the old orchestrator's lane as recorded, without
	// ResolveLane: that refuses a closed lane item, and a handoff for a lane
	// whose item was closed since must still work.
	var lane string
	if ls, ok := s.store.(ports.LaneReader); ok {
		sl, err := ls.SessionLane(ctx, id)
		if err != nil {
			return domain.Session{}, err
		}
		lane = sl.LaneItemID
	}
	sess, err := s.spawnOrchestrator(ctx, project, true, old.Mode, "", orchestratorSpawnOptions{
		LaneItemID:       lane,
		Prompt:           fmt.Sprintf("Read %s and continue. You replaced orchestrator %s; worker reports now come to you.", note, id),
		SkipRetireNotice: true,
	})
	if err != nil {
		return domain.Session{}, err
	}
	if s.handoffSuccessors == nil {
		s.handoffSuccessors = make(map[domain.SessionID]domain.SessionID)
	}
	s.handoffSuccessors[id] = sess.ID
	return sess, nil
}

// agentRunning reports an orchestrator that can be asked to write its own state
// note. A hibernated one counts: a send wakes it, and one whose wake fails is
// left exited.
func agentRunning(rec domain.SessionRecord) bool {
	return rec.Activity.State != domain.ActivityExited
}

// handoffNote returns the cleaned absolute path of an existing regular file
// inside the session's artifact directory, symlinks resolved so a link cannot
// point the new orchestrator outside it.
func (s *Service) handoffNote(id domain.SessionID, notePath string) (string, error) {
	invalid := func(msg string) error {
		return apierr.Invalid("HANDOFF_NOTE_INVALID", msg, nil)
	}
	notePath = strings.TrimSpace(notePath)
	dir := sessionartifacts.Dir(s.dataDir, id)
	if notePath == "" {
		latest := newestNote(dir)
		if latest == "" {
			return "", apierr.Conflict("HANDOFF_NOTE_REQUIRED", "The orchestrator has no state note in its artifact directory yet; ask it to write one first", nil)
		}
		notePath = latest
	}
	if dir == "" || !filepath.IsAbs(notePath) {
		return "", invalid("The state note must be an absolute path inside the orchestrator's artifact directory")
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", invalid("The orchestrator's artifact directory does not exist")
	}
	realNote, err := filepath.EvalSymlinks(filepath.Clean(notePath))
	if err != nil {
		return "", invalid("The state note does not exist")
	}
	rel, err := filepath.Rel(realDir, realNote)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", invalid("The state note must be inside the orchestrator's artifact directory")
	}
	if info, err := os.Stat(realNote); err != nil || !info.Mode().IsRegular() {
		return "", invalid("The state note must be a regular file")
	}
	return realNote, nil
}

// newestNote is the most recently modified text note (.md or .txt) directly
// inside dir, or "" when there is none. Artifact dirs also hold screenshots and
// other files that must never seed a successor.
func newestNote(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var bestTime time.Time
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || strings.HasPrefix(e.Name(), ".") || !isNoteFile(e.Name()) {
			continue
		}
		if best == "" || info.ModTime().After(bestTime) {
			best, bestTime = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	return best
}

func isNoteFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".txt":
		return true
	}
	return false
}
