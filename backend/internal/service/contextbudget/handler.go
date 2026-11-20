package contextbudget

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ItemInput is the backlog item a Yes files.
type ItemInput struct {
	Title    string
	Note     string
	DoneWhen string
}

// Item is a filed backlog item.
type Item struct{ ID, Ref string }

// Work files the backlog item and starts a worker linked to it. The worker has
// no parent session; it reports like any project worker.
type Work interface {
	FileItem(ctx context.Context, project domain.ProjectID, in ItemInput) (Item, error)
	StartWorker(ctx context.Context, project domain.ProjectID, item Item, brief string) (domain.SessionID, error)
}

// HandlerStore is what answering a card reads and writes.
type HandlerStore interface {
	FindingByAsk(ctx context.Context, askID string) (domain.ContextHealthFinding, bool, error)
	SaveFindingTransition(ctx context.Context, f domain.ContextHealthFinding, from domain.ContextHealthState) (bool, error)
}

// Handler answers context-health cards for the daemon. It never calls an
// orchestrator: Yes starts a worker, No suppresses the finding.
type Handler struct {
	Store HandlerStore
	Work  Work
	Now   func() time.Time
	Log   *slog.Logger
}

// Answered is the ask service's AnswerHandlers entry for domain.AskOriginContextHealth.
func (h *Handler) Answered(ctx context.Context, ask domain.OwnerAsk) error {
	f, ok, err := h.Store.FindingByAsk(ctx, ask.ID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no context health finding for ask %s", ask.Ref)
	}
	if f.State != domain.ContextFindingOpen || ask.Answer == nil {
		return nil // already handled
	}
	now := h.now()
	if ask.Answer.OptionID != "yes" {
		// No, or free text, which counts as No and is kept as the reason.
		f.State, f.DeclinedAt, f.DeclinedTokens, f.UpdatedAt = domain.ContextFindingDeclined, now, f.MeasuredTokens, now
		f.DeclineReason = ask.Answer.Other
		_, err := h.Store.SaveFindingTransition(ctx, f, domain.ContextFindingOpen)
		if err == nil {
			recordDecline(f, now, h.Log)
		}
		return err
	}
	in, brief := Brief(f)
	item, err := h.Work.FileItem(ctx, f.ProjectID, in)
	if err != nil {
		return err
	}
	// The item is recorded before the worker starts, so a failed start leaves
	// the owner's yes visible as an open backlog item.
	f.State, f.BacklogItemID, f.UpdatedAt = domain.ContextFindingAccepted, item.ID, now
	if ok, err := h.Store.SaveFindingTransition(ctx, f, domain.ContextFindingOpen); err != nil || !ok {
		return err
	}
	if _, err := h.Work.StartWorker(ctx, f.ProjectID, item, brief); err != nil {
		return fmt.Errorf("filed %s but its worker did not start: %w", item.Ref, err)
	}
	return nil
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Brief is the backlog item and the worker brief for a finding: the evidence,
// and the rule that behavior must survive the change.
func Brief(f domain.ContextHealthFinding) (ItemInput, string) {
	var in ItemInput
	var b strings.Builder
	switch f.Kind {
	case domain.ContextMemoryBudget:
		in = ItemInput{
			Title:    fmt.Sprintf("Archive stale Claude memory entries (index %s tokens, budget %s)", k(f.MeasuredTokens), k(f.BudgetTokens)),
			DoneWhen: fmt.Sprintf("MEMORY.md is under %s tokens; archived entries moved to memory/archive/, none deleted.", k(f.BudgetTokens)),
		}
		b.WriteString("Shrink the Claude orchestrator memory index. Move stale entries to memory/archive/ (never delete). Entries of type feedback or user are only proposed to the owner, not moved. At most 10 changes.\n")
	case domain.ContextRoutingPointer:
		in = ItemInput{
			Title:    "Add the week's doc pointers to the instruction files",
			DoneWhen: "The instruction files point sessions at it in one short line; no behavioral rule changed.",
		}
		b.WriteString("Sessions of this project went looking for the same docs or answers this week. Add the pointer lines the evidence lists, one short line per gap, to the instruction file it names (apply the drafted patch when there is one, preferring the analyst draft). Do not move or weaken any rule.\n")
	default:
		in = ItemInput{
			Title:    fmt.Sprintf("Trim always-loaded instruction files (%s tokens, budget %s)", k(f.MeasuredTokens), k(f.BudgetTokens)),
			DoneWhen: fmt.Sprintf("The always-loaded instruction files total under %s tokens with every behavioral rule intact.", k(f.BudgetTokens)),
		}
		b.WriteString("Reduce the always-loaded instruction files. Never drop or weaken a behavioral rule: only move per-module detail into on-demand docs linked from the instruction file (keep the rule and a pointer), or delete text that is provably dead.\n")
	}
	b.WriteString("Do not message an orchestrator about this; report through your normal PR flow.\n\n")
	b.WriteString(f.Evidence)
	if f.ArtifactDir != "" {
		b.WriteString("\nEvidence file: " + f.ArtifactDir + "/evidence.md\n")
		if f.Kind == domain.ContextRoutingPointer {
			b.WriteString("Drafted pointer patch, when there is one: " + f.ArtifactDir + "/" + patchFile + "\n")
		}
	}
	if f.ArtifactDir != "" {
		for _, name := range []string{AnalystNotesFile, AnalystPatchFile, MemoryActionsFile} {
			if _, err := os.Stat(filepath.Join(f.ArtifactDir, name)); err == nil {
				b.WriteString("Analyst draft file: " + f.ArtifactDir + "/" + name + "\n")
			}
		}
	}
	in.Note = f.Evidence
	return in, b.String()
}
