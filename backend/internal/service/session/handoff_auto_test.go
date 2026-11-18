package session

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// The idle handoff writes the fixed bootstrap note itself and then runs the
// normal handoff: the old orchestrator retires, a successor spawns.
func TestAutoHandoffOrchestratorWritesTheTemplateAndHandsOff(t *testing.T) {
	f := newHandoffFixture(t, domain.HarnessClaudeCode)
	if err := f.svc.AutoHandoffOrchestrator(context.Background(), "mer-1"); err != nil {
		t.Fatalf("AutoHandoffOrchestrator: %v", err)
	}
	if len(f.fc.retired) != 1 || f.fc.retired[0] != "mer-1" {
		t.Fatalf("retired = %v, want [mer-1]", f.fc.retired)
	}
	entries, err := os.ReadDir(sessionartifacts.Dir(f.svc.dataDir, "mer-1"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "auto-handoff-") {
			continue
		}
		found = true
		b, _ := os.ReadFile(sessionartifacts.Dir(f.svc.dataDir, "mer-1") + "/" + e.Name())
		if !strings.Contains(string(b), "tallpine merges") || !strings.Contains(string(b), "mer-1") {
			t.Fatalf("note lacks live-state commands or the previous id:\n%s", b)
		}
	}
	if !found {
		t.Fatal("no auto-handoff note was written")
	}
}

// A refused handoff (no orchestrator agent configured) leaves the old
// orchestrator running.
func TestAutoHandoffOrchestratorRefusalKeepsTheOldOne(t *testing.T) {
	f := newHandoffFixture(t, "")
	if err := f.svc.AutoHandoffOrchestrator(context.Background(), "mer-1"); err == nil {
		t.Fatal("handoff without a configured orchestrator agent succeeded")
	}
	if len(f.fc.retired) != 0 {
		t.Fatalf("retired = %v, want none", f.fc.retired)
	}
}

// B673: an orchestrator whose agent cannot start (its saved provider
// conversation is gone, so every resume fails and it is left exited) has no
// state note and cannot be asked to write one. Reset hands off on the daemon's
// bootstrap note instead of refusing with HANDOFF_NOTE_REQUIRED, which left the
// orchestrator stuck.
func TestHandoffOrchestratorWithoutANoteUsesTheBootstrapNoteWhenTheAgentExited(t *testing.T) {
	f := newHandoffFixture(t, domain.HarnessClaudeCode)
	if err := os.Remove(f.note); err != nil {
		t.Fatal(err)
	}
	rec := f.st.sessions["mer-1"]
	rec.Activity.State = domain.ActivityExited
	f.st.sessions["mer-1"] = rec
	got, err := f.svc.HandoffOrchestrator(context.Background(), "mer-1", "")
	if err != nil {
		t.Fatalf("HandoffOrchestrator: %v", err)
	}
	if got.ID != "mer-2" || len(f.fc.retired) != 1 || f.fc.retired[0] != "mer-1" {
		t.Fatalf("successor = %q, retired = %v; want mer-2 replacing mer-1", got.ID, f.fc.retired)
	}
	if !strings.Contains(f.fc.spawnedCfg.Prompt, "auto-handoff-") {
		t.Fatalf("seed prompt = %q, want the bootstrap note", f.fc.spawnedCfg.Prompt)
	}
}

// A running or hibernated orchestrator can still write its own note (a send
// wakes a hibernated one), so Reset keeps asking it to rather than retiring it
// on the generic bootstrap note.
func TestHandoffOrchestratorWithoutANoteAsksALiveOrSleepingOrchestrator(t *testing.T) {
	for name, set := range map[string]func(*domain.SessionRecord){
		"running":    func(*domain.SessionRecord) {},
		"hibernated": func(r *domain.SessionRecord) { now := time.Now(); r.HibernatedAt = &now },
	} {
		t.Run(name, func(t *testing.T) {
			f := newHandoffFixture(t, domain.HarnessClaudeCode)
			if err := os.Remove(f.note); err != nil {
				t.Fatal(err)
			}
			rec := f.st.sessions["mer-1"]
			set(&rec)
			f.st.sessions["mer-1"] = rec
			if _, err := f.svc.HandoffOrchestrator(context.Background(), "mer-1", ""); err == nil || !strings.Contains(err.Error(), "no state note") {
				t.Fatalf("err = %v, want a note-required refusal", err)
			}
			if len(f.fc.retired) != 0 {
				t.Fatalf("retired = %v, want none", f.fc.retired)
			}
		})
	}
}

// unreattachedCommander reports what the manager reports for an agent the
// daemon could not reattach after a restart.
type unreattachedCommander struct{ *fakeCommander }

func (unreattachedCommander) SessionStatusReadiness(domain.SessionRecord) string {
	return "unavailable"
}

// B810: after a restart the reattach failed, so the orchestrator kept its idle
// activity but had no controller. Handoff asked for its note, the UI then sent
// it a message, and that failed with "no live chat controller": a dead end. It
// cannot write a note, so the handoff seeds the successor from the bootstrap note.
func TestHandoffOrchestratorWithoutANoteUsesTheBootstrapNoteWhenReattachFailed(t *testing.T) {
	f := newHandoffFixture(t, domain.HarnessClaudeCode)
	if err := os.Remove(f.note); err != nil {
		t.Fatal(err)
	}
	f.svc.manager = unreattachedCommander{f.fc}
	got, err := f.svc.HandoffOrchestrator(context.Background(), "mer-1", "")
	if err != nil {
		t.Fatalf("HandoffOrchestrator: %v", err)
	}
	if got.ID != "mer-2" || len(f.fc.retired) != 1 || f.fc.retired[0] != "mer-1" {
		t.Fatalf("successor = %q, retired = %v; want mer-2 replacing mer-1", got.ID, f.fc.retired)
	}
	if !strings.Contains(f.fc.spawnedCfg.Prompt, "auto-handoff-") {
		t.Fatalf("seed prompt = %q, want the bootstrap note", f.fc.spawnedCfg.Prompt)
	}
}
