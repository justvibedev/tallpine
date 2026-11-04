package chat

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type windowCatalog map[string]int64

func (c windowCatalog) Models(context.Context, string, string, bool) (ports.AgentModelCatalog, error) {
	var out ports.AgentModelCatalog
	for id, window := range c {
		out.Models = append(out.Models, ports.AgentModelInfo{ID: id, ContextWindow: window})
	}
	return out, nil
}

// A percent resolves against the model being launched, from the catalog, so it
// applies on a brand-new chat and never borrows another model's window.
func TestCompactModelWindowResolvesFromTheLaunchingModel(t *testing.T) {
	ctx := context.Background()
	s := &Service{modelCatalog: windowCatalog{"sonnet": 200_000, "opus": 1_000_000}}
	fresh := domain.ConversationRecord{}
	if got := s.compactModelWindow(ctx, StartConfig{Harness: domain.HarnessClaudeCode}, fresh, "sonnet"); got != 200_000 {
		t.Fatalf("new chat, no usage yet = %d, want the catalog's 200000", got)
	}
	// After a provider switch the old usage (a 400k Codex model) must not leak in.
	codexUsage := domain.ConversationRecord{Usage: &domain.ConversationUsage{ContextWindow: 400_000}}
	switched := StartConfig{Harness: domain.HarnessClaudeCode, ProviderSwitch: &domain.ChatProviderSwitch{}}
	if got := s.compactModelWindow(ctx, switched, codexUsage, "sonnet"); got != 200_000 {
		t.Fatalf("after a switch = %d, want the target model's 200000", got)
	}
	unlisted := &Service{modelCatalog: windowCatalog{}}
	if got := unlisted.compactModelWindow(ctx, switched, codexUsage, "custom-model"); got != 0 {
		t.Fatalf("switch with no catalog window = %d, want 0 (not the previous model's)", got)
	}
}

// The agent's reported window is never capacity: once Tallpine sets a compaction env,
// Claude Code reports that limit as its window. Reading it back made every
// restart apply the percent to the previous limit (400k -> 160k -> 64k).
func TestCompactModelWindowIgnoresTheReportedWindowAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	s := &Service{modelCatalog: windowCatalog{}}
	record := domain.ConversationRecord{Settings: domain.ConversationSettings{Model: "custom-model"}}
	cfg := StartConfig{Harness: domain.HarnessClaudeCode}
	for restart := 0; restart < 2; restart++ {
		if got := s.compactModelWindow(ctx, cfg, record, "custom-model"); got != 0 {
			t.Fatalf("restart %d: capacity = %d, want 0 (unknown, never the reported window)", restart, got)
		}
		record.Usage = &domain.ConversationUsage{ContextWindow: 400_000}
	}
}

// With the alias "opus" saved, the threshold for 40% stays 400k of 1M across
// restarts even though the agent reports 400k as its window.
func TestCompactThresholdStaysStableAcrossRestartsForAlias(t *testing.T) {
	ctx := context.Background()
	s := &Service{modelCatalog: windowCatalog{}}
	record := domain.ConversationRecord{Settings: domain.ConversationSettings{Model: "opus", AutoCompactWindow: "40%"}}
	cfg := StartConfig{Harness: domain.HarnessClaudeCode}
	for restart := 0; restart < 3; restart++ {
		capacity := s.compactModelWindow(ctx, cfg, record, "opus")
		if got := domain.AutoCompactTokenCount("40%", capacity); got != 400_000 {
			t.Fatalf("restart %d: threshold = %d, want 400000", restart, got)
		}
		record.Usage = &domain.ConversationUsage{ContextWindow: 400_000}
	}
}

// The settings overlay carries the resolved capacity, threshold and name, so a
// client never derives them from the window the agent reports.
func TestOverlayCompactionViewResolvesCapacityAndThreshold(t *testing.T) {
	ctx := context.Background()
	s := &Service{modelCatalog: windowCatalog{}}
	c := &Controller{harness: domain.HarnessClaudeCode}
	c.usage.ContextWindow = 400_000
	settings := domain.ConversationSettings{Model: "opus", AutoCompactWindow: "40%"}
	s.overlayCompactionView(ctx, &settings, c, false)
	if settings.ContextCapacity != 1_000_000 || settings.CompactThreshold != 400_000 || settings.ModelLabel != "Opus" {
		t.Fatalf("view = capacity %d threshold %d label %q, want 1000000 400000 Opus",
			settings.ContextCapacity, settings.CompactThreshold, settings.ModelLabel)
	}
	unknown := domain.ConversationSettings{Model: "custom-model", AutoCompactWindow: "40%"}
	s.overlayCompactionView(ctx, &unknown, c, false)
	if unknown.ContextCapacity != 0 || unknown.CompactThreshold != 0 {
		t.Fatalf("unknown model view = %+v, want no capacity and no threshold", unknown)
	}
}
