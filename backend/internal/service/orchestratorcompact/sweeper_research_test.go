package orchestratorcompact

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// researchHarness adds a recorded ClearResearch that also clears the
// harness's research flag, with the compaction or handoff order recorded.
func researchHarness() (*harness, *[]string) {
	h := newHarness()
	h.state.Research = true
	var order []string
	h.sweeper.deps.ClearResearch = func(context.Context, domain.SessionID) bool {
		order = append(order, "clear")
		was := h.state.Research
		h.state.Research = false
		return was
	}
	h.onCompact = func() error { order = append(order, "compact"); return nil }
	h.sweeper.deps.Handoff = func(context.Context, domain.SessionID) error { order = append(order, "handoff"); return nil }
	return h, &order
}

func TestIdleCompactionClearsResearchFirst(t *testing.T) {
	h, order := researchHarness()
	h.sweep(t)
	if got := *order; len(got) != 2 || got[0] != "clear" || got[1] != "compact" {
		t.Fatalf("order = %v, want [clear compact]", got)
	}
}

func TestIdleHandoffClearsResearchFirst(t *testing.T) {
	h, order := researchHarness()
	h.state.Handoff = true
	h.sweep(t)
	if got := *order; len(got) != 2 || got[0] != "clear" || got[1] != "handoff" {
		t.Fatalf("order = %v, want [clear handoff]", got)
	}
}

func TestResearchExpiresAfterAQuietHourOnly(t *testing.T) {
	h, order := researchHarness()
	h.state.ContextUsed = 10_000 // below the threshold: no compaction
	h.store.sessions[0] = session("orch", domain.KindOrchestrator, domain.ResearchQuietExpiry-time.Minute)
	h.sweep(t)
	if len(*order) != 0 {
		t.Fatalf("cleared %v before the quiet hour", *order)
	}
	h.store.sessions[0] = session("orch", domain.KindOrchestrator, domain.ResearchQuietExpiry)
	h.sweep(t)
	if got := *order; len(got) != 1 || got[0] != "clear" {
		t.Fatalf("order = %v, want one clear after the quiet hour", got)
	}
}

func TestResearchDoesNotExpireWhileBusy(t *testing.T) {
	h, order := researchHarness()
	h.state.Busy = true
	h.store.sessions[0] = session("orch", domain.KindOrchestrator, 2*time.Hour)
	h.sweep(t)
	if len(*order) != 0 {
		t.Fatalf("cleared %v while busy", *order)
	}
}
